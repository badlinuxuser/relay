package oauth

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
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
}

func (f *flow) finish(err error) {
	select {
	case f.done <- err:
	default:
	}
}

// Broker runs the sign-ins. Begin starts one and returns the address for the browser; the
// callback address of the Relay daemon (HandleCallback) completes it.
type Broker struct {
	store *Store
	hc    *http.Client

	// OnSignedIn is called after a token was saved (the manager reconnects the server).
	OnSignedIn func(id string)

	mu        sync.Mutex
	flows     map[string]*flow // by state
	refreshMu sync.Mutex
}

func NewBroker(store *Store) *Broker {
	return &Broker{store: store, hc: newHTTP(), flows: map[string]*flow{}}
}

func (b *Broker) Store() *Store { return b.store }

// Begin prepares a sign-in. persist says whether to remember cfg (discovered settings and the
// registered client) for refreshing later; settings that come from mcp.json are not copied.
// wait blocks until the user finished, the context ends, or SignInTimeout passes.
func (b *Broker) Begin(ctx context.Context, id string, cfg Config, persist bool, redirect string) (string, func(context.Context) error, error) {
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
		case rec.Config != nil && rec.Config.ClientID != "" && rec.RedirectURI == redirect:
			cfg.ClientID, cfg.ClientSecret = rec.Config.ClientID, rec.Config.ClientSecret
		case cfg.Registration != "":
			cid, secret, err := Register(ctx, b.hc, cfg.Registration, redirect, "Relay")
			if err != nil {
				return "", nil, err
			}
			cfg.ClientID, cfg.ClientSecret = cid, secret
		default:
			return "", nil, errors.New("there is no client_id: register an OAuth app at the service and put its client_id into the \"oauth\" block")
		}
	}
	verifier, challenge := pkce()
	state := randomString(24)
	authURL, err := cfg.AuthURL(redirect, state, challenge)
	if err != nil {
		return "", nil, err
	}
	f := &flow{id: id, cfg: cfg, verifier: verifier, redirect: redirect, created: time.Now(), done: make(chan error, 1)}

	b.mu.Lock()
	for st, old := range b.flows {
		if old.id == id {
			delete(b.flows, st)
			old.finish(errors.New("a newer sign-in was started"))
		} else if time.Since(old.created) > 2*SignInTimeout {
			delete(b.flows, st)
		}
	}
	b.flows[state] = f
	b.mu.Unlock()

	if persist {
		c := cfg
		rec.Config, rec.RedirectURI = &c, redirect
		if err := b.store.Save(id, rec); err != nil {
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
			b.drop(state)
			return wctx.Err()
		case <-t.C:
			b.drop(state)
			return errors.New("the sign-in was not finished within 5 minutes")
		}
	}
	return authURL, wait, nil
}

func (b *Broker) drop(state string) {
	b.mu.Lock()
	delete(b.flows, state)
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
	q := r.URL.Query()
	state := q.Get("state")
	b.mu.Lock()
	f := b.flows[state]
	delete(b.flows, state)
	b.mu.Unlock()
	if f == nil || state == "" {
		page(w, http.StatusBadRequest, "Unknown sign-in", "This link is not valid any more. Start the sign-in again from Relay.")
		return
	}
	if e := q.Get("error"); e != "" {
		msg := e
		if d := q.Get("error_description"); d != "" {
			msg += ": " + d
		}
		f.finish(fmt.Errorf("the service did not allow the sign-in (%s)", msg))
		page(w, http.StatusOK, "Sign-in cancelled", msg)
		return
	}
	code := q.Get("code")
	if code == "" {
		f.finish(errors.New("the service sent no code"))
		page(w, http.StatusBadRequest, "Sign-in failed", "The service sent no code.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	tok, err := f.cfg.Exchange(ctx, b.hc, f.redirect, code, f.verifier)
	if err != nil {
		f.finish(err)
		page(w, http.StatusBadGateway, "Sign-in failed", err.Error())
		return
	}
	rec, err := b.store.Load(f.id)
	if err == nil {
		rec.Token = tok
		err = b.store.Save(f.id, rec)
	}
	if err != nil {
		f.finish(err)
		page(w, http.StatusInternalServerError, "Sign-in failed", err.Error())
		return
	}
	f.finish(nil)
	if b.OnSignedIn != nil {
		go b.OnSignedIn(f.id)
	}
	page(w, http.StatusOK, "Signed in", "You can close this tab and go back to Relay.")
}

// Token returns a valid access token for id, refreshing it first when it has expired. ok is
// false when the user has to sign in. static is the configuration from mcp.json (nil: use the
// discovered one that was saved at the sign-in).
func (b *Broker) Token(ctx context.Context, id string, static *Config) (token string, ok bool, err error) {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
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
		if errors.As(err, &oe) { // the service no longer accepts the refresh token: sign in again
			rec.Token = nil
			_ = b.store.Save(id, rec)
			return "", false, nil
		}
		return "", false, err // a network problem: keep the token, try later
	}
	rec.Token = nt
	if err := b.store.Save(id, rec); err != nil {
		return "", false, err
	}
	return nt.AccessToken, true, nil
}
