package provider

import "testing"

type fixedKeys []string

func (f fixedKeys) Values(string) []string { return f }

func noEmit(Event) error { return nil }

func TestRateLimitPausesTheKeyOnlyForThatModel(t *testing.T) {
	k := NewKeys("p", nil, fixedKeys{"k1"}, true)
	err := DoWithKeysFor(k, "p", "m1", noEmit, func(string) error {
		return &Error{RateLimited: true, Retryable: true}
	})
	if err == nil {
		t.Fatal("the limit must be reported")
	}
	if _, ok := k.GetFor("m1"); ok {
		t.Fatal("the key must rest for m1")
	}
	if _, ok := k.GetFor("m2"); !ok {
		t.Fatal("another model of the provider must still be able to use the key")
	}
	if k.Available() != 1 {
		t.Fatal("a limit on one model does not take the key away from the provider")
	}
}

func TestNoCreditPausesTheWholeKey(t *testing.T) {
	k := NewKeys("p", nil, fixedKeys{"k1"}, true)
	_ = DoWithKeysFor(k, "p", "m1", noEmit, func(string) error {
		return &Error{Billing: true, Retryable: true}
	})
	if _, ok := k.GetFor("m2"); ok {
		t.Fatal("no credit: no model can use the key")
	}
	if !k.Exhausted() {
		t.Fatal("the provider must count as exhausted")
	}
}
