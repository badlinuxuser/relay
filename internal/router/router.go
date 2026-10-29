// Package router выбирает модель по режиму, делает фолбэк при ошибках и лимитах
// и ведёт учёт расхода.
package router

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"relay/internal/config"
	"relay/internal/provider"
	"relay/internal/usage"
)

type Router struct {
	cfg     *config.Config
	clients map[string]provider.Client
	keys    map[string]*provider.Keys
	usage   *usage.Tracker
}

func New(cfg *config.Config, tr *usage.Tracker) *Router {
	r := &Router{
		cfg:     cfg,
		clients: map[string]provider.Client{},
		keys:    map[string]*provider.Keys{},
		usage:   tr,
	}
	for _, p := range cfg.Providers {
		k := provider.NewKeys(p.APIKeyEnv)
		r.keys[p.Name] = k
		if p.Kind == "anthropic" {
			r.clients[p.Name] = provider.NewAnthropic(p.Name, p.BaseURL, k, p.Headers)
		} else {
			r.clients[p.Name] = provider.NewOpenAI(p.Name, p.BaseURL, k, p.Headers)
		}
	}
	return r
}

// KeyStats возвращает число ключей провайдера и сколько из них сейчас доступно.
func (r *Router) KeyStats(name string) (total, available int) {
	k := r.keys[name]
	if k == nil {
		return 0, 0
	}
	return k.Count(), k.Available()
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

// Resolve строит цепочку моделей: явно выбранная первой, затем модели режима.
// Если нужны инструменты, модели без поддержки инструментов пропускаются.
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
	for _, id := range md.Models {
		m, ok := r.cfg.ModelByID(id)
		if !ok || has(id) || (needTools && !m.Tools) {
			continue
		}
		chain = append(chain, id)
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
		if pe.RateLimited {
			tag = "rate_limit"
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
