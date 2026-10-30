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

// Stream пробует модели цепочки по очереди. Фолбэк возможен только пока модель
// ничего не выдала (иначе ответ пришлось бы склеивать из двух моделей).
// Возвращает id модели, давшей ответ. Расход отправляется событием EvUsage.
func (r *Router) Stream(ctx context.Context, chain []string, req provider.Request, emit func(provider.Event) error) (string, error) {
	var lastErr error = errors.New("no models to try")
	for i, id := range chain {
		m, _ := r.cfg.ModelByID(id)
		cl := r.clients[m.Provider]
		if cl == nil {
			lastErr = fmt.Errorf("provider %q is not available", m.Provider)
			continue
		}
		if err := emit(provider.Event{Kind: provider.EvModel, Text: id}); err != nil {
			return id, err
		}
		rq := req
		rq.Model = m.Name

		emitted := false
		outChars := 0
		var u provider.Usage
		err := cl.Stream(ctx, rq, func(ev provider.Event) error {
			switch ev.Kind {
			case provider.EvText:
				emitted = true
				outChars += len(ev.Text)
			case provider.EvToolCall:
				emitted = true
				if ev.Call != nil {
					outChars += len(ev.Call.Args) + len(ev.Call.Name)
				}
			case provider.EvUsage:
				u = ev.Usage
				return nil // расход отправим сами, уже со стоимостью
			}
			return emit(ev)
		})
		if err == nil {
			if u.In == 0 {
				u.In = estimateIn(rq.Messages)
			}
			if u.Out == 0 {
				u.Out = outChars/4 + 1
			}
			cost := (float64(u.In)*m.PriceIn + float64(u.Out)*m.PriceOut) / 1e6
			r.usage.Add(id, u.In, u.Out, cost)
			return id, emit(provider.Event{Kind: provider.EvUsage, Usage: u, Cost: cost, Text: id})
		}
		lastErr = err
		var pe *provider.Error
		if emitted || ctx.Err() != nil || !errors.As(err, &pe) || !pe.Retryable || i == len(chain)-1 {
			return id, err
		}
		tag := "fallback"
		switch {
		case pe.RateLimited:
			tag = "rate_limit"
		case pe.NoKey:
			tag = "no_key"
		}
		_ = emit(provider.Event{Kind: provider.EvNotice, Tag: tag,
			Text: fmt.Sprintf("%s unavailable (%s), falling back to %s", id, shorten(pe.Msg, 80), chain[i+1])})
	}
	return "", lastErr
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
