// Package router выбирает модель по режиму, делает фолбэк при ошибках и лимитах
// и ведёт учёт расхода.
package router

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"relay/internal/catalog"
	"relay/internal/config"
	"relay/internal/provider"
	"relay/internal/usage"
)

type Router struct {
	cfg     *config.Config
	clients map[string]provider.Client
	keys    map[string]*provider.Keys
	usage   *usage.Tracker
	cat     *catalog.Catalog
}

func New(cfg *config.Config, tr *usage.Tracker, src provider.Source) *Router {
	r := &Router{
		cfg:     cfg,
		clients: map[string]provider.Client{},
		keys:    map[string]*provider.Keys{},
		usage:   tr,
	}
	for _, p := range cfg.Providers {
		k := provider.NewKeys(p.KeyName(), p.APIKeyEnv, src, p.NeedsKey())
		r.keys[p.Name] = k
		if p.Kind == "anthropic" {
			r.clients[p.Name] = provider.NewAnthropic(p.Name, p.BaseURL, k, p.Headers)
		} else {
			r.clients[p.Name] = provider.NewOpenAI(p.Name, p.BaseURL, k, p.Headers)
		}
	}
	return r
}

// SetCatalog connects the list of models the providers really have. With it,
// Resolve skips models that are not installed and fills mode chains from what is.
func (r *Router) SetCatalog(c *catalog.Catalog) { r.cat = c }

// Keys returns the key holder of a provider (nil for an unknown provider).
func (r *Router) Keys(name string) *provider.Keys { return r.keys[name] }

// KeyStats возвращает число ключей провайдера, сколько из них сейчас доступно
// и нужен ли провайдеру ключ вообще.
func (r *Router) KeyStats(name string) (total, available int, needs bool) {
	k := r.keys[name]
	if k == nil {
		return 0, 0, false
	}
	return k.Count(), k.Available(), k.NeedsKey()
}

// Hint помогает автовыбору режима ("auto").
type Hint struct {
	Chars int
	Agent bool
	Code  bool
}

func (r *Router) autoMode(h Hint) string {
	switch {
	case h.Agent || h.Chars > 6000:
		return "hard"
	case h.Code || h.Chars > 800:
		return "medium"
	default:
		return "small"
	}
}

// keyless reports whether the model's provider needs an API key but has none yet.
// Such models are skipped silently, so a user with only one provider configured
// does not get a "falling back" notice for every provider they never set up.
func (r *Router) keyless(m config.Model) bool {
	k := r.keys[m.Provider]
	return k != nil && k.NeedsKey() && k.Count() == 0
}

// resting: every key of the provider is on pause (no credit, rate limit). Asking again
// now would only fail again, so the fallback chain leaves such a provider out.
func (r *Router) resting(m config.Model) bool {
	k := r.keys[m.Provider]
	return k != nil && k.NeedsKey() && k.Exhausted()
}

// Resolve строит цепочку моделей: явно выбранная первой, затем модели режима.
// Если нужны инструменты, модели без поддержки инструментов пропускаются.
// Модели провайдеров, для которых не добавлен ключ, пропускаются тоже (если
// не осталось ничего другого, цепочка строится как есть, чтобы показать ошибку «нет ключа»).
func (r *Router) Resolve(mode, model string, needTools bool, h Hint) (chain []string, usedMode string, err error) {
	if mode == "" || mode == "auto" {
		mode = r.autoMode(h)
	}
	md, ok := r.cfg.Modes[mode]
	if !ok {
		return nil, mode, fmt.Errorf("mode %q is not configured", mode)
	}
	has := func(id string) bool {
		for _, c := range chain {
			if c == id {
				return true
			}
		}
		return false
	}
	if model != "" && model != "auto" {
		if _, ok := r.cfg.ModelByID(model); !ok {
			return nil, mode, fmt.Errorf("unknown model %q", model)
		}
		chain = append(chain, model)
	}
	explicit := len(chain)
	var snap *catalog.Snapshot
	if r.cat != nil {
		snap = r.cat.Peek()
		r.cat.Warm()
	}
	var skipped []string
	for _, id := range md.Models {
		m, ok := r.cfg.ModelByID(id)
		if !ok || has(id) || (needTools && !m.Tools) {
			continue
		}
		if r.keyless(m) {
			skipped = append(skipped, id)
			continue
		}
		if r.resting(m) {
			continue // failed a moment ago with no credit or a limit: do not try again at once
		}
		if snap.Missing(m.Provider, m.Name) {
			// The model from the config is not installed (or does not exist). If it
			// was a model on this computer, use the ones that are installed instead.
			if p, ok := r.cfg.ProviderByName(m.Provider); ok && p.IsLocal() {
				for _, e := range snap.Pick(m.Provider, 2) {
					if !has(e.ID) {
						chain = append(chain, e.ID)
					}
				}
			}
			continue
		}
		chain = append(chain, id)
	}
	if len(chain) == explicit {
		// Nothing from the mode's list is usable: take what the providers have.
		for _, e := range snap.Fallback(3) {
			if !has(e.ID) {
				chain = append(chain, e.ID)
			}
		}
	}
	if len(chain) == explicit {
		// Every model of the mode lacks a key: keep them so the user sees "no key".
		chain = append(chain, skipped...)
	}
	if len(chain) == 0 {
		return nil, mode, fmt.Errorf("no suitable models for mode %q", mode)
	}
	return chain, mode, nil
}

func estimateIn(msgs []provider.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, tc := range m.ToolCalls {
			n += len(tc.Args) + len(tc.Name)
		}
	}
	return n/4 + 1
}

// attempt streams one model. emitted tells whether the model sent text or a tool call
// before it failed: after that no other model may take over (the answer would be a mix).
// On success it also records the usage and sends the usage event.
func (r *Router) attempt(ctx context.Context, id string, m config.Model, req provider.Request, emit func(provider.Event) error) (emitted bool, err error) {
	cl := r.clients[m.Provider]
	if cl == nil {
		return false, &provider.Error{Provider: m.Provider, Retryable: true,
			Msg: fmt.Sprintf("provider %q is not available", m.Provider)}
	}
	if e := emit(provider.Event{Kind: provider.EvModel, Text: id}); e != nil {
		return true, e
	}
	rq := req
	rq.Model = m.Name

	outChars := 0
	var u provider.Usage
	err = cl.Stream(ctx, rq, func(ev provider.Event) error {
		switch ev.Kind {
		case provider.EvText:
			emitted = true
			outChars += len(ev.Text)
		case provider.EvThinking:
			outChars += len(ev.Text)
		case provider.EvToolCall:
			emitted = true
			if ev.Call != nil {
				outChars += len(ev.Call.Args) + len(ev.Call.Name)
			}
		case provider.EvUsage:
			u = ev.Usage
			return nil // the router sends the usage itself, with the cost
		}
		return emit(ev)
	})
	if err != nil {
		return emitted, err
	}
	if u.In == 0 {
		u.In = estimateIn(rq.Messages)
	}
	if u.Out == 0 {
		u.Out = outChars/4 + 1
	}
	cost := (float64(u.In)*m.PriceIn + float64(u.Out)*m.PriceOut) / 1e6
	r.usage.Add(id, u.In, u.Out, cost)
	if !emitted {
		_ = emit(provider.Event{Kind: provider.EvNotice, Tag: "info",
			Text: id + " sent no answer (only its reasoning, or nothing). Try again or pick another model with /models"})
	}
	if e := emit(provider.Event{Kind: provider.EvUsage, Usage: u, Cost: cost, Text: id}); e != nil {
		return true, e
	}
	return emitted, nil
}

// Stream tries the models of the chain in order, silently, whatever their provider (used by
// the gateway and by compare). A fallback is possible only while the model has sent nothing.
// It returns the id of the model that answered. The usage is sent as an EvUsage event.
func (r *Router) Stream(ctx context.Context, chain []string, req provider.Request, emit func(provider.Event) error) (string, error) {
	type failure struct{ id, why string }
	var fails []failure
	var lastErr error = errors.New("no models to try")
	badProv := map[string]bool{} // providers that cannot help in this request (no key, no credit, bad key)
	allLimited := true

	next := func(from int) string {
		for j := from; j < len(chain); j++ {
			if m, ok := r.cfg.ModelByID(chain[j]); ok && !badProv[m.Provider] {
				return chain[j]
			}
		}
		return ""
	}

	for i, id := range chain {
		m, _ := r.cfg.ModelByID(id)
		if badProv[m.Provider] {
			continue
		}
		emitted, err := r.attempt(ctx, id, m, req, emit)
		if err == nil {
			return id, nil
		}
		lastErr = err
		var pe *provider.Error
		if emitted || ctx.Err() != nil || !errors.As(err, &pe) || !pe.Retryable {
			return id, err
		}
		fails = append(fails, failure{id, shorten(pe.Msg, 90)})
		if !pe.RateLimited {
			allLimited = false
		}
		// the same provider will fail the same way for its other models. A limit is
		// different: Gemini counts its quota per model, so another model may still answer.
		prov, _ := r.cfg.ProviderByName(m.Provider)
		if pe.NoKey || pe.Billing || pe.Auth || (pe.RateLimited && prov.DiscoverStyle() != "gemini") {
			badProv[m.Provider] = true
		}
		nx := next(i + 1)
		if nx == "" {
			break
		}
		tag := "fallback"
		switch {
		case pe.RateLimited:
			tag = "rate_limit"
		case pe.NoKey:
			tag = "no_key"
		}
		why := shorten(pe.Msg, 80)
		if pe.Billing {
			why = "no credit or the plan does not include it: " + why
		}
		_ = emit(provider.Event{Kind: provider.EvNotice, Tag: tag,
			Text: fmt.Sprintf("%s unavailable (%s), falling back to %s", id, why, nx)})
	}
	if len(fails) > 1 {
		parts := make([]string, 0, len(fails))
		for _, f := range fails {
			parts = append(parts, f.id+": "+f.why)
		}
		return "", &provider.Error{Msg: "none of the models could answer. " + strings.Join(parts, " | ") +
			". Check /keys, or pick a working model with /models",
			RateLimited: allLimited}
	}
	return "", lastErr
}

// Ask is called before the router moves from a model that failed to the next one of the
// same provider. It returns "allow", "always" (do not ask again) or "deny": then the request
// ends with the error of the model that failed.
type Ask func(failed, next, why string) string

// maxTries bounds the models tried in one request, for providers that list hundreds
// (OpenRouter, Ollama Cloud): after this many failures the provider is reported as not working.
const maxTries = 8

type candidate struct{ id, name string }

func normName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ":latest")
}

// providerWide reports an error that every model of the provider will hit: asking the user
// to try the other models would only repeat it.
func (r *Router) providerWide(prov string, pe *provider.Error) bool {
	if pe.NoKey || (pe.Auth && pe.Status == 401) {
		return true
	}
	k := r.keys[prov]
	return k != nil && k.NeedsKey() && (k.Count() == 0 || k.Available() == 0)
}

// nextOfProvider picks the model to offer after `after` failed: another model of the SAME
// provider that was not tried yet. The models of the configuration come first (in their
// order, starting after the failed one), then the models the provider itself lists.
func (r *Router) nextOfProvider(prov, after string, needTools bool, tried map[string]bool, snap *catalog.Snapshot) string {
	var all []candidate
	seen := map[string]bool{}
	add := func(id, name string) {
		n := normName(name)
		if !seen[n] {
			seen[n] = true
			all = append(all, candidate{id, n})
		}
	}
	for _, m := range r.cfg.Models {
		if m.Provider != prov || (needTools && !m.Tools) || snap.Missing(prov, m.Name) {
			continue
		}
		add(m.ID, m.Name)
	}
	for _, e := range snap.Pick(prov, 1<<20) {
		add(e.ID, e.Name)
	}
	order := all
	for i, c := range all {
		if c.name == normName(after) {
			order = append(append([]candidate{}, all[i+1:]...), all[:i]...)
			break
		}
	}
	for _, c := range order {
		if !tried[c.name] {
			return c.id
		}
	}
	return ""
}

// StreamAsk answers with the first model of the chain. If it fails in a way another model
// might fix (a limit, no credit, a server error), it offers the next model of the SAME
// provider, one after another, until one answers or the provider has none left:
//
//   - ask returns "allow": switch and go on; "always": switch now and from then on without asking;
//   - "deny": the request ends with the error of the model that failed;
//   - ask == nil: switch without asking;
//   - no model of the provider answered: the error says so and points to /keys.
//
// Models of other providers are never used here: the provider the user chose is the one
// whose models are tried.
func (r *Router) StreamAsk(ctx context.Context, chain []string, req provider.Request, emit func(provider.Event) error, ask Ask) (string, error) {
	if len(chain) == 0 {
		return "", errors.New("no models to try")
	}
	cur := chain[0]
	m, ok := r.cfg.ModelByID(cur)
	if !ok {
		return "", fmt.Errorf("unknown model %q", cur)
	}
	prov := m.Provider
	needTools := len(req.Tools) > 0
	var snap *catalog.Snapshot
	if r.cat != nil {
		snap = r.cat.Peek()
	}

	type failure struct{ id, why string }
	var fails []failure
	tried := map[string]bool{}
	allLimited, allNoKey := true, true

	for {
		tried[normName(m.Name)] = true
		emitted, err := r.attempt(ctx, cur, m, req, emit)
		if err == nil {
			return cur, nil
		}
		var pe *provider.Error
		if emitted || ctx.Err() != nil || !errors.As(err, &pe) || !pe.Retryable {
			return cur, err
		}
		fails = append(fails, failure{cur, shorten(pe.Msg, 90)})
		if !pe.RateLimited {
			allLimited = false
		}
		if !pe.NoKey {
			allNoKey = false
		}
		if r.providerWide(prov, pe) || len(tried) >= maxTries {
			break // the other models of this provider would fail the same way
		}
		nx := r.nextOfProvider(prov, m.Name, needTools, tried, snap)
		if nx == "" {
			break
		}
		why := shorten(pe.Msg, 80)
		if pe.Billing {
			why = "no credit or the plan does not include it: " + why
		}
		if ask != nil {
			switch ask(cur, nx, why) {
			case "deny":
				return cur, err // the user does not want another model: show why this one failed
			case "always":
				ask = nil
			}
		}
		tag := "fallback"
		switch {
		case pe.RateLimited:
			tag = "rate_limit"
		case pe.NoKey:
			tag = "no_key"
		}
		_ = emit(provider.Event{Kind: provider.EvNotice, Tag: tag,
			Text: fmt.Sprintf("%s unavailable (%s), trying %s from the same provider", cur, why, nx)})
		cur = nx
		m, _ = r.cfg.ModelByID(cur)
	}

	parts := make([]string, 0, len(fails))
	for _, f := range fails {
		parts = append(parts, f.id+": "+f.why)
	}
	p, _ := r.cfg.ProviderByName(prov)
	return "", &provider.Error{Provider: prov, NoKey: allNoKey, RateLimited: allLimited,
		Msg: fmt.Sprintf("none of the models of %s is working (%s). %s", prov, strings.Join(parts, " | "), advice(p.IsLocal()))}
}

// advice tells what to do when no model of a provider answers.
func advice(local bool) string {
	if local {
		return "Check that the server is running (for example /ollama), or pick another model with /models."
	}
	return "Check your API keys with /keys, or pick another model with /models."
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
