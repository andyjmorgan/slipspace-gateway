package headers

import (
	"net/http"
	"testing"
)

func TestRedactor_IsSensitive_Builtins(t *testing.T) {
	t.Parallel()
	r := NewRedactor(nil)
	cases := map[string]bool{
		"Authorization":           true,
		"authorization":           true,
		"Proxy-Authorization":     true,
		"X-Api-Key":               true,
		"x-api-key":               true,
		"Anthropic-Api-Key":       true,
		"X-Goog-Api-Key":          true,
		"X-Apikey":                true,
		"X-Sluice-Identity":       true,
		"x-sluice-identity":       true,
		"Cookie":                  true,
		"Set-Cookie":              true,
		"X-Auth-Token":            true,
		"Bearer-Token":            true,
		"X-CSRF-Token":            true,
		"X-API-Secret":            true,
		"Content-Type":            false,
		"User-Agent":              false,
		"X-Sluice-Correlation-Id": false,
		"X-Custom-Header":         false,
		"Accept":                  false,
	}
	for name, want := range cases {
		if got := r.IsSensitive(name); got != want {
			t.Errorf("Redactor.IsSensitive(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestRedactor_IsSensitive_NilReceiverFallsBackToBuiltins(t *testing.T) {
	t.Parallel()
	var r *Redactor
	if !r.IsSensitive("Authorization") {
		t.Error("nil receiver should still mask built-in matches")
	}
	if r.IsSensitive("Content-Type") {
		t.Error("nil receiver should not mask non-sensitive headers")
	}
}

func TestRedactor_IsSensitive_OperatorExtras(t *testing.T) {
	t.Parallel()
	r := NewRedactor([]string{
		"acme-trace-id",
		"X-TENANT-KEY", // mixed case → normalised
		"   ",          // empty after trim → dropped
		"auth",         // dup of builtin → dropped
	})
	cases := map[string]bool{
		"X-Acme-Trace-Id":     true, // operator extra match
		"x-acme-trace-id-v2":  true, // substring match by design
		"X-Tenant-Key":        true,
		"X-Authorization":     true, // built-in still works
		"X-Innocent-Customer": false,
	}
	for name, want := range cases {
		if got := r.IsSensitive(name); got != want {
			t.Errorf("Redactor.IsSensitive(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestRedactor_Extras_OnlyReturnsOperatorAdditions(t *testing.T) {
	t.Parallel()
	r := NewRedactor([]string{"acme-trace-id", "X-Tenant-Key", "auth"})
	extras := r.Extras()
	// "auth" is a builtin → must NOT appear in Extras()
	for _, e := range extras {
		if e == "auth" {
			t.Errorf("Extras() must not include builtins; got %v", extras)
		}
	}
	have := map[string]bool{}
	for _, e := range extras {
		have[e] = true
	}
	if !have["acme-trace-id"] {
		t.Errorf("Extras() missing acme-trace-id: %v", extras)
	}
	if !have["x-tenant-key"] {
		t.Errorf("Extras() missing x-tenant-key (normalised): %v", extras)
	}
}

func TestRedactor_Redact_NilAndEmpty(t *testing.T) {
	t.Parallel()
	r := NewRedactor(nil)
	if got := r.Redact(nil); got != nil {
		t.Errorf("Redact(nil) = %v, want nil", got)
	}
	if got := r.Redact(http.Header{}); got != nil {
		t.Errorf("Redact(empty) = %v, want nil", got)
	}
}

func TestRedactor_Redact_MasksCredentials(t *testing.T) {
	t.Parallel()
	r := NewRedactor([]string{"acme-trace-id"})
	in := http.Header{
		"Authorization":     []string{"Bearer sk-abc123"},
		"X-Api-Key":         []string{"sk-secret"},
		"X-Sluice-Identity": []string{"sk_live_supersecret_must_not_leak"},
		"X-Acme-Trace-Id":   []string{"tenant-tenant-tenant"}, // operator extra
		"Cookie":            []string{"session=xyz; other=qux"},
		"Content-Type":      []string{"application/json"},
		"User-Agent":        []string{"sluice/1.0"},
	}
	got := r.Redact(in)
	for _, name := range []string{
		"Authorization",
		"X-Api-Key",
		"X-Sluice-Identity",
		"X-Acme-Trace-Id",
		"Cookie",
	} {
		v := got[name]
		if len(v) != 1 || v[0] != "[REDACTED]" {
			t.Errorf("%s = %v, want [REDACTED]", name, v)
		}
	}
	if v := got["Content-Type"]; len(v) != 1 || v[0] != "application/json" {
		t.Errorf("Content-Type leaked or mangled: %v", v)
	}
	if v := got["User-Agent"]; len(v) != 1 || v[0] != "sluice/1.0" {
		t.Errorf("User-Agent leaked or mangled: %v", v)
	}
}

func TestRedactor_Redact_MultiValuedSensitive(t *testing.T) {
	t.Parallel()
	r := NewRedactor(nil)
	in := http.Header{
		"Set-Cookie": []string{"a=1", "b=2", "c=3"},
	}
	got := r.Redact(in)
	v := got["Set-Cookie"]
	if len(v) != 3 {
		t.Fatalf("Set-Cookie len = %d, want 3", len(v))
	}
	for _, val := range v {
		if val != "[REDACTED]" {
			t.Errorf("Set-Cookie value = %q, want [REDACTED]", val)
		}
	}
}

func TestRedactor_Redact_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	r := NewRedactor(nil)
	in := http.Header{
		"Authorization": []string{"Bearer original"},
	}
	_ = r.Redact(in)
	if got := in.Get("Authorization"); got != "Bearer original" {
		t.Errorf("input mutated: Authorization = %q", got)
	}
}
