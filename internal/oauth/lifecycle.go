package oauth

import (
	"fmt"
	"sync"
)

// LifecycleState makes every authorization transition explicit and testable.
type LifecycleState string

const (
	StateNotAuthenticated             LifecycleState = "not_authenticated"
	StateDiscoveryInProgress          LifecycleState = "discovery_in_progress"
	StateAwaitingUserConsent          LifecycleState = "awaiting_user_consent"
	StateAwaitingBrowserAuthorization LifecycleState = "awaiting_browser_authorization"
	StateCallbackReceived             LifecycleState = "callback_received"
	StateTokenExchangeInProgress      LifecycleState = "token_exchange_in_progress"
	StateAuthenticated                LifecycleState = "authenticated"
	StateRefreshInProgress            LifecycleState = "refresh_in_progress"
	StateReauthorizationRequired      LifecycleState = "reauthorization_required"
	StateUserDeclined                 LifecycleState = "user_declined"
	StateCancelled                    LifecycleState = "cancelled"
	StateExpired                      LifecycleState = "expired"
	StateFailed                       LifecycleState = "failed"
)

var lifecycleTransitions = map[LifecycleState]map[LifecycleState]bool{
	StateNotAuthenticated:             {StateDiscoveryInProgress: true, StateAwaitingUserConsent: true, StateAwaitingBrowserAuthorization: true, StateCancelled: true},
	StateDiscoveryInProgress:          {StateAwaitingUserConsent: true, StateFailed: true, StateCancelled: true},
	StateAwaitingUserConsent:          {StateAwaitingBrowserAuthorization: true, StateUserDeclined: true, StateCancelled: true, StateFailed: true},
	StateAwaitingBrowserAuthorization: {StateCallbackReceived: true, StateUserDeclined: true, StateCancelled: true, StateExpired: true, StateFailed: true},
	StateCallbackReceived:             {StateTokenExchangeInProgress: true, StateUserDeclined: true, StateCancelled: true, StateExpired: true, StateFailed: true},
	StateTokenExchangeInProgress:      {StateAuthenticated: true, StateReauthorizationRequired: true, StateCancelled: true, StateExpired: true, StateFailed: true},
	StateAuthenticated:                {StateRefreshInProgress: true, StateCancelled: true},
	StateRefreshInProgress:            {StateAuthenticated: true, StateReauthorizationRequired: true, StateFailed: true, StateCancelled: true},
	StateReauthorizationRequired:      {StateDiscoveryInProgress: true, StateAwaitingUserConsent: true, StateAwaitingBrowserAuthorization: true, StateCancelled: true},
}

// Lifecycle validates transitions under a mutex. Terminal states have no outgoing edges.
type Lifecycle struct {
	mu    sync.Mutex
	state LifecycleState
}

func NewLifecycle(initial LifecycleState) *Lifecycle { return &Lifecycle{state: initial} }
func (l *Lifecycle) State() LifecycleState           { l.mu.Lock(); defer l.mu.Unlock(); return l.state }
func (l *Lifecycle) Transition(next LifecycleState) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !lifecycleTransitions[l.state][next] {
		return fmt.Errorf("invalid OAuth lifecycle transition %s -> %s", l.state, next)
	}
	l.state = next
	return nil
}
