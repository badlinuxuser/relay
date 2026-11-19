package oauth

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SignInTimeout is how long a sign-in may take before it is given up.
const SignInTimeout = 5 * time.Minute

type flow struct {
	id       string
	cfg      Config
	verifier string
	redirect string
	created  time.Time
	done     chan error // buffered: the result is kept even if nobody waits
	mu       sync.Mutex
	life     *Lifecycle
	ctx      context.Context
	cancel   context.CancelFunc
}

func (f *flow) finish(err error) {
	select {
	case f.done <- err:
	default:
	}
}

func (f *flow) transition(state LifecycleState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.life.Transition(state)
}

func (f *flow) terminate(state LifecycleState, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	current := f.life.State()
	if current == StateAuthenticated || current == StateUserDeclined || current == StateCancelled || current == StateExpired || current == StateFailed {
		return
	}
	if f.cancel != nil {
		f.cancel()
	}
	if f.life != nil {
		_ = f.life.Transition(state)
	}
	f.finish(err)
}

// commit serializes persistence with cancellation/expiry. A callback that loses this race
// cannot leave a usable token behind after the waiting agent has already been cancelled.
func (f *flow) commit(save func() error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.life.State() != StateTokenExchangeInProgress {
		return errors.New("authorization session was cancelled before credentials could be committed")
	}
	if err := save(); err != nil {
		_ = f.life.Transition(StateFailed)
		if f.cancel != nil {
			f.cancel()
		}
		f.finish(err)
		return err
	}
	if err := f.life.Transition(StateAuthenticated); err != nil {
		if f.cancel != nil {
			f.cancel()
		}
		f.finish(err)
		return err
	}
	f.finish(nil)
	if f.cancel != nil {
		f.cancel()
	}
	return nil
}

// Broker runs the sign-ins. Begin starts one and returns the address for the browser; the
// callback address of the Relay daemon (HandleCallback) completes it.
type Broker struct {
	store *Store
	hc    *http.Client

	// OnSignedIn is called after a token was saved (the manager reconnects the server).
	OnSignedIn func(id string)

	mu        sync.Mutex
	beginMu   sync.Mutex        // serializes registration/session creation across concurrent agent runs
	flows     map[string]*flow  // by state
	active    map[string]string // server id -> active state; remains set during callback exchange
	refreshMu sync.Mutex
}

func NewBroker(store *Store) *Broker {
	return &Broker{store: store, hc: newHTTP(), flows: map[string]*flow{}, active: map[string]string{}}
}

func (b *Broker) Store() *Store { return b.store }

func sameClientRegistration(previous, next Config) bool {
	return previous.Issuer == next.Issuer && previous.AuthorizeURL == next.AuthorizeURL && previous.TokenURL == next.TokenURL && previous.Registration == next.Registration && previous.CIMDURL == next.CIMDURL
}

// Begin prepares a sign-in. persist says whether to remember cfg (discovered settings and the
// registered client) for refreshing later; settings that come from mcp.json are not copied.
// wait blocks until the user finished, the context ends, or SignInTimeout passes.
func (b *Broker) Begin(ctx context.Context, id string, cfg Config, persist bool, redirect string) (string, func(context.Context) error, error) {
	b.beginMu.Lock()
	defer b.beginMu.Unlock()
	b.mu.Lock()
	for staleState, stale := range b.flows {
		if time.Since(stale.created) > 2*SignInTimeout {
			delete(b.flows, staleState)
			if b.active[stale.id] == staleState {
				delete(b.active, stale.id)
			}
			stale.terminate(StateExpired, errors.New("authorization session expired"))
		}
	}
	if b.active[id] != "" {
		b.mu.Unlock()
		return "", nil, fmt.Errorf("OAuth authorization for %q is already in progress", id)
	}
	b.mu.Unlock()
	if !cfg.PKCES256Supported {
		return "", nil, errors.New("OAuth provider must explicitly support PKCE S256; Relay will not start an authorization flow without it")
	}
	if err := ValidateParams(cfg.Params); err != nil {
		return "", nil, err
	}

	if cfg.RequireIssuer && cfg.Issuer == "" {
		return "", nil, errors.New("oauth.require_issuer requires a configured or discovered issuer")
	}
	if cfg.Resource == "" || cfg.TargetURL == "" {
		return "", nil, errors.New("OAuth credentials must be bound to an explicit resource and target URL")
	}
	if strings.HasPrefix(cfg.TargetURL, "stdio://") {
		if !ResourceMatchesTarget(cfg.Resource, cfg.Resource) {
			return "", nil, errors.New("stdio OAuth resource must be a valid absolute HTTP(S) URI")
		}
	} else if !ResourceMatchesTarget(cfg.Resource, cfg.TargetURL) {
		return "", nil, errors.New("OAuth resource does not identify the target URL")
	}

	if err := checkURL(cfg.AuthorizeURL, "authorize_url"); err != nil {
		return "", nil, err
	}
	if err := checkURL(cfg.TokenURL, "token_url"); err != nil {
		return "", nil, err
	}
	if err := checkURL(redirect, "the callback address"); err != nil {
		return "", nil, err
	}
	rec, err := b.store.Load(id)
	if err != nil {
		return "", nil, err
	}
	if cfg.ClientID == "" {
		switch {
		case rec.Config != nil && rec.Config.ClientID != "" && rec.RedirectURI == redirect && sameClientRegistration(*rec.Config, cfg):
			cfg.ClientID = rec.Config.ClientID
			if cfg.TokenAuthMethod == "" {
				cfg.TokenAuthMethod = rec.Config.TokenAuthMethod
			}
			if cfg.ClientSecret == "" && (cfg.TokenAuthMethod == "client_secret_basic" || cfg.TokenAuthMethod == "client_secret_post") {
				cfg.ClientSecret = rec.Config.ClientSecret
			}
		case cfg.CIMDURL != "" && cfg.CIMDSupported:
			if err := checkURL(cfg.CIMDURL, "client ID metadata document"); err != nil {
				return "", nil, err
			}
			cfg.ClientID = cfg.CIMDURL
			if cfg.TokenAuthMethod == "" {
				cfg.TokenAuthMethod = "none"
			}
		case cfg.Registration != "":
			cid, secret, method, err := RegisterClient(ctx, b.hc, cfg.Registration, redirect, "Relay", cfg.TokenAuthMethod)
			if err != nil {
				return "", nil, err
			}
			cfg.ClientID, cfg.ClientSecret, cfg.TokenAuthMethod = cid, secret, method
		default:
			return "", nil, errors.New("OAuth client setup is required: add a Client ID in Connections, configure cimd_url, or use an advertised dynamic registration endpoint")
		}
	}
	if err := cfg.validateClientAuth(); err != nil {
		return "", nil, err
	}
	verifier, challenge := pkce()
	state := randomString(24)
	authURL, err := cfg.AuthURL(redirect, state, challenge)
	if err != nil {
		return "", nil, err
	}
	flowCtx, flowCancel := context.WithCancel(context.Background())
	f := &flow{id: id, cfg: cfg, verifier: verifier, redirect: redirect, created: time.Now(), done: make(chan error, 1),
		life: NewLifecycle(StateAwaitingUserConsent), ctx: flowCtx, cancel: flowCancel}

	if err := f.life.Transition(StateAwaitingBrowserAuthorization); err != nil {
		flowCancel()
		return "", nil, err
	}
	b.mu.Lock()
	b.flows[state] = f
	b.active[id] = state
	b.mu.Unlock()

	if persist {
		c := cfg
		rec.Config, rec.RedirectURI = &c, redirect
		if err := b.store.Save(id, rec); err != nil {
			b.drop(state)
			return "", nil, err
		}
	}
	wait := func(wctx context.Context) error {
		t := time.NewTimer(SignInTimeout)
		defer t.Stop()
		select {
		case err := <-f.done:
			return err
		case <-wctx.Done():
			if f.life.State() == StateAuthenticated {
				return nil
			}
			f.terminate(StateCancelled, wctx.Err())
			b.drop(state)
			b.clearActive(f.id, state)
			return wctx.Err()
		case <-t.C:
			if f.life.State() == StateAuthenticated {
				return nil
			}
			err := errors.New("the sign-in was not finished within 5 minutes")
			f.terminate(StateExpired, err)
			b.drop(state)
			b.clearActive(f.id, state)
			return err
		}
	}
	return authURL, wait, nil
}

func (b *Broker) drop(state string) {
	b.mu.Lock()
	f := b.flows[state]
	delete(b.flows, state)
	if f != nil && b.active[f.id] == state {
		delete(b.active, f.id)
	}
	b.mu.Unlock()
	if f != nil {
		f.terminate(StateCancelled, context.Canceled)
	}
}

func (b *Broker) clearActive(id, state string) {
	b.mu.Lock()
	if b.active[id] == state {
		delete(b.active, id)
	}
	if f := b.flows[state]; f != nil && f.id == id {
		delete(b.flows, state)
	}
	b.mu.Unlock()
}

func page(w http.ResponseWriter, code int, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Relay</title>
<body style="background:#0C1510;color:#E6E8EA;font:16px ui-monospace,Consolas,monospace;display:grid;place-items:center;height:100vh;margin:0">
<div style="max-width:520px;padding:24px"><h1 style="color:#67DD5D;font-size:22px">%s</h1><p>%s</p></div>`,
		html.EscapeString(title), html.EscapeString(text))
}

// HandleCallback serves the address that the service redirects the browser to.
func (b *Broker) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/oauth/callback" {
		page(w, http.StatusMethodNotAllowed, "Invalid callback", "Use the callback URL opened by Relay.")
		return
	}
	q := r.URL.Query()
	if len(q["state"]) != 1 || len(q["code"]) > 1 || len(q["error"]) > 1 || len(q["iss"]) > 1 || (q.Get("error") != "" && q.Get("code") != "") {
		page(w, http.StatusBadRequest, "Sign-in failed", "The authorization response contains ambiguous parameters.")
		return
	}
	state := q.Get("state")
	b.mu.Lock()
	f := b.flows[state]
	b.mu.Unlock()
	if f == nil || state == "" {
		page(w, http.StatusBadRequest, "Unknown sign-in", "This link is not valid any more. Start the sign-in again from Relay.")
		return
	}
	if err := f.transition(StateCallbackReceived); err != nil {
		page(w, http.StatusBadRequest, "Expired sign-in", "This authorization session is no longer active. Start again from Relay.")
		return
	}
	defer b.clearActive(f.id, state)
	gotIssuer := q.Get("iss")
	if f.cfg.RequireIssuer && gotIssuer == "" {
		f.terminate(StateFailed, errors.New("the authorization response omitted its required issuer"))
		page(w, http.StatusBadRequest, "Sign-in failed", "The authorization server omitted the required issuer parameter.")
		return
	}
	if gotIssuer != "" && f.cfg.Issuer == "" {
		f.terminate(StateFailed, errors.New("the authorization response included an issuer but no expected issuer was configured"))
		page(w, http.StatusBadRequest, "Sign-in failed", "The authorization server sent an issuer that Relay cannot validate.")
		return
	}
	if gotIssuer != "" && gotIssuer != f.cfg.Issuer {
		f.terminate(StateFailed, errors.New("the authorization response came from an unexpected issuer"))
		page(w, http.StatusBadRequest, "Sign-in failed", "The authorization server identity did not match the discovered issuer.")
		return
	}
	if e := q.Get("error"); e != "" {
		description := q.Get("error_description")
		f.terminate(StateUserDeclined, fmt.Errorf("the service did not allow the sign-in (%s)", e))
		if description == "" {
			description = e
		}
		page(w, http.StatusOK, "Sign-in cancelled", description)
		return
	}
	code := q.Get("code")
	if code == "" {
		f.terminate(StateFailed, errors.New("the service sent no code"))
		page(w, http.StatusBadRequest, "Sign-in failed", "The service sent no code.")
		return
	}
	if err := f.transition(StateTokenExchangeInProgress); err != nil {
		f.terminate(StateFailed, err)
		page(w, http.StatusBadRequest, "Sign-in failed", "The authorization session is no longer active.")
		return
	}
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Second)
	defer cancel()
	tok, err := f.cfg.Exchange(ctx, b.hc, f.redirect, code, f.verifier)
	if err != nil {
		f.terminate(StateFailed, err)
		page(w, http.StatusBadGateway, "Sign-in failed", err.Error())
		return
	}
	rec, err := b.store.Load(f.id)
	if err == nil {
		rec.Token = tok
		rec.Resource = f.cfg.Resource
		rec.TargetURL = f.cfg.TargetURL
		rec.Config = &f.cfg
		if f.cfg.UserInfoURL != "" {
			if account, accountErr := f.cfg.ResolveAccount(ctx, b.hc, tok.AccessToken); accountErr == nil {
				rec.Account = account
			}
		}
		err = b.store.Save(f.id, rec)
	}
	if err != nil {
		f.terminate(StateFailed, err)
		page(w, http.StatusInternalServerError, "Sign-in failed", err.Error())
		return
	}
	if err := f.commit(func() error { return b.store.Save(f.id, rec) }); err != nil {
		page(w, http.StatusInternalServerError, "Sign-in failed", err.Error())
		return
	}
	if b.OnSignedIn != nil {
		go b.OnSignedIn(f.id)
	}
	page(w, http.StatusOK, "Signed in", "You can close this tab and go back to Relay.")
}

// CancelAuthorization stops any pending browser flow for a connection before it is disconnected or revoked.
func (b *Broker) CancelAuthorization(id string) {
	b.cancelActive(id, "connection authorization was cancelled")
}

func (b *Broker) cancelActive(id, reason string) {
	b.mu.Lock()
	state := b.active[id]
	f := b.flows[state]
	if state != "" {
		delete(b.active, id)
		delete(b.flows, state)
	}
	b.mu.Unlock()
	if f != nil {
		f.terminate(StateCancelled, errors.New(reason))
	}
}

// MarkUsed records a successful MCP tool invocation, not merely a token lookup/refresh.
func (b *Broker) MarkUsed(id string) error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	rec, err := b.store.Load(id)
	if err != nil {
		return err
	}
	if rec.Token == nil {
		return nil
	}
	rec.LastUsed = time.Now()
	return b.store.Save(id, rec)
}

// HasBoundCredential checks a stored credential binding without refreshing or changing it.
func (b *Broker) HasBoundCredential(id, resource, targetURL string) bool {
	rec, err := b.store.Load(id)
	if err != nil || rec.Token == nil {
		return false
	}
	bound := rec.Resource
	if bound == "" && rec.Config != nil {
		bound = rec.Config.Resource
	}
	if resource == "" {
		resource = bound
	}
	if bound == "" || resource != bound || targetURL == "" || rec.TargetURL == "" || rec.TargetURL != targetURL {
		return false
	}
	return rec.Token.Valid() || rec.Token.RefreshToken != ""
}

// TokenForResource returns a credential only when its stored resource indicator exactly
// matches the destination resource. This prevents a changed/reused MCP server ID from
// inheriting a bearer token granted to a different audience.
func (b *Broker) TokenForResource(ctx context.Context, id string, static *Config, resource, targetURL string) (string, bool, error) {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	rec, err := b.store.Load(id)
	if err != nil {
		return "", false, err
	}
	bound := rec.Resource
	if bound == "" && rec.Config != nil {
		bound = rec.Config.Resource
	}
	if resource == "" {
		resource = bound
	}
	if resource == "" || bound == "" || bound != resource || targetURL == "" || rec.TargetURL == "" || rec.TargetURL != targetURL {
		return "", false, nil
	}
	return b.tokenLocked(ctx, id, static)
}

func (b *Broker) tokenLocked(ctx context.Context, id string, static *Config) (token string, ok bool, err error) {
	rec, err := b.store.Load(id)
	if err != nil {
		return "", false, err
	}
	if rec.Token.Valid() {
		return rec.Token.AccessToken, true, nil
	}
	if rec.Token == nil || rec.Token.RefreshToken == "" {
		return "", false, nil
	}
	cfg := static
	if cfg == nil {
		cfg = rec.Config
	}
	if cfg == nil {
		return "", false, nil
	}
	c := *cfg
	if c.ClientID == "" && rec.Config != nil {
		c.ClientID, c.ClientSecret = rec.Config.ClientID, rec.Config.ClientSecret
	}
	nt, err := c.Refresh(ctx, b.hc, rec.Token.RefreshToken)
	if err != nil {
		var oe *Error
		if errors.As(err, &oe) {
			rec.Token = nil
			_ = b.store.Save(id, rec)
			return "", false, nil
		}
		return "", false, err // network problem: keep the token, try later
	}
	if nt.Scope == "" {
		nt.Scope = rec.Token.Scope
	}
	rec.Token = nt
	if err := b.store.Save(id, rec); err != nil {
		return "", false, err
	}
	return nt.AccessToken, true, nil
}
