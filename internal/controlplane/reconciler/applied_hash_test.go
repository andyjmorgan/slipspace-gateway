package reconciler

import "testing"

func TestAppliedHash(t *testing.T) {
	// A nil holder is a safe no-op (register-only gateways have none).
	var nilH *AppliedHash
	nilH.Set("x")
	if nilH.Get() != "" {
		t.Error("nil AppliedHash.Get() should be empty")
	}

	h := &AppliedHash{}
	if h.Get() != "" {
		t.Error("fresh AppliedHash.Get() should be empty")
	}
	h.Set("abc")
	if got := h.Get(); got != "abc" {
		t.Errorf("Get() = %q, want abc", got)
	}
	h.Set("def")
	if got := h.Get(); got != "def" {
		t.Errorf("Get() after reset = %q, want def", got)
	}
}
