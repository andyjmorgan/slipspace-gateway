package headers

import (
	"net/http"
	"testing"
)

func TestIsSensitiveHeaderName(t *testing.T) {
	t.Parallel()
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
		if got := IsSensitiveHeaderName(name); got != want {
			t.Errorf("IsSensitiveHeaderName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestRedactSensitive_NilAndEmpty(t *testing.T) {
	t.Parallel()
	if got := RedactSensitive(nil); got != nil {
		t.Errorf("RedactSensitive(nil) = %v, want nil", got)
	}
	if got := RedactSensitive(http.Header{}); got != nil {
		t.Errorf("RedactSensitive(empty) = %v, want nil", got)
	}
}

func TestRedactSensitive_MasksCredentials(t *testing.T) {
	t.Parallel()
	in := http.Header{
		"Authorization":     []string{"Bearer sk-abc123"},
		"X-Api-Key":         []string{"sk-secret"},
		"X-Sluice-Identity": []string{"sk_live_supersecret_must_not_leak"},
		"Cookie":            []string{"session=xyz; other=qux"},
		"Content-Type":      []string{"application/json"},
		"User-Agent":        []string{"sluice/1.0"},
	}
	got := RedactSensitive(in)
	if v := got["Authorization"]; len(v) != 1 || v[0] != "[REDACTED]" {
		t.Errorf("Authorization = %v, want [REDACTED]", v)
	}
	if v := got["X-Api-Key"]; len(v) != 1 || v[0] != "[REDACTED]" {
		t.Errorf("X-Api-Key = %v, want [REDACTED]", v)
	}
	if v := got["X-Sluice-Identity"]; len(v) != 1 || v[0] != "[REDACTED]" {
		t.Errorf("X-Sluice-Identity leaked or unmasked: %v", v)
	}
	if v := got["Cookie"]; len(v) != 1 || v[0] != "[REDACTED]" {
		t.Errorf("Cookie = %v, want [REDACTED]", v)
	}
	if v := got["Content-Type"]; len(v) != 1 || v[0] != "application/json" {
		t.Errorf("Content-Type leaked or mangled: %v", v)
	}
	if v := got["User-Agent"]; len(v) != 1 || v[0] != "sluice/1.0" {
		t.Errorf("User-Agent leaked or mangled: %v", v)
	}
}

func TestRedactSensitive_MultiValuedSensitive(t *testing.T) {
	t.Parallel()
	in := http.Header{
		"Set-Cookie": []string{"a=1", "b=2", "c=3"},
	}
	got := RedactSensitive(in)
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

func TestRedactSensitive_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	in := http.Header{
		"Authorization": []string{"Bearer original"},
	}
	_ = RedactSensitive(in)
	if got := in.Get("Authorization"); got != "Bearer original" {
		t.Errorf("input mutated: Authorization = %q", got)
	}
}
