package oauth

import "testing"

func TestLifecycleValidTransitionsAndTerminalStates(t *testing.T) {
	l := NewLifecycle(StateNotAuthenticated)
	for _, state := range []LifecycleState{StateDiscoveryInProgress, StateAwaitingUserConsent, StateAwaitingBrowserAuthorization, StateCallbackReceived, StateTokenExchangeInProgress, StateAuthenticated} {
		if err := l.Transition(state); err != nil {
			t.Fatalf("transition to %s: %v", state, err)
		}
	}
	if err := l.Transition(StateAwaitingBrowserAuthorization); err == nil {
		t.Fatal("authenticated session must not jump back to browser authorization")
	}
	if err := l.Transition(StateRefreshInProgress); err != nil {
		t.Fatal(err)
	}
	if err := l.Transition(StateReauthorizationRequired); err != nil {
		t.Fatal(err)
	}
	if err := l.Transition(StateAwaitingUserConsent); err != nil {
		t.Fatal(err)
	}
	if err := l.Transition(StateUserDeclined); err != nil {
		t.Fatalf("declining reauthorization should be valid: %v", err)
	}
	if err := l.Transition(StateCancelled); err == nil {
		t.Fatal("terminal decline state accepted another transition")
	}
}

func TestLifecycleRejectsCallbackAfterCancellation(t *testing.T) {
	l := NewLifecycle(StateAwaitingBrowserAuthorization)
	if err := l.Transition(StateCancelled); err != nil {
		t.Fatal(err)
	}
	if err := l.Transition(StateCallbackReceived); err == nil {
		t.Fatal("cancelled flow accepted a callback")
	}
}

func TestLifecycleProviderDenialAfterCallback(t *testing.T) {
	l := NewLifecycle(StateAwaitingBrowserAuthorization)
	if err := l.Transition(StateCallbackReceived); err != nil {
		t.Fatal(err)
	}
	if err := l.Transition(StateUserDeclined); err != nil {
		t.Fatalf("provider denial must be terminal: %v", err)
	}
	if err := l.Transition(StateTokenExchangeInProgress); err == nil {
		t.Fatal("denied callback proceeded to token exchange")
	}
}
