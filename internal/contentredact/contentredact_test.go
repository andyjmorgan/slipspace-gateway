package contentredact_test

import (
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/contentredact"
)

func TestRedact_MasksCredentials(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		in     string
		secret string // a substring that must NOT survive
	}{
		{"sluice live key", "my key is sk_live_abcdef0123456789 ok", "sk_live_abcdef0123456789"},
		{"openai key", "use sk-abcdefABCDEF0123456789XYZ now", "sk-abcdefABCDEF0123456789XYZ"},
		{"anthropic key", "key sk-ant-api03-aaaaaaaaaaaaaaaaaaaa end", "sk-ant-api03-aaaaaaaaaaaaaaaaaaaa"},
		{"google key", "AIzaSyA1234567890abcdefghijklmnop here", "AIzaSyA1234567890abcdefghijklmnop"},
		{"slack token", "xoxb-1234567890-abcdefghijklmnop x", "xoxb-1234567890-abcdefghijklmnop"},
		{"jwt", "token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV done", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := contentredact.Redact(tc.in)
			if strings.Contains(got, tc.secret) {
				t.Errorf("Redact(%q) = %q, still contains the secret", tc.in, got)
			}
			if !strings.Contains(got, "[REDACTED]") {
				t.Errorf("Redact(%q) = %q, expected a [REDACTED] mask", tc.in, got)
			}
		})
	}
}

func TestRedact_BearerKeepsScheme(t *testing.T) {
	t.Parallel()
	got := contentredact.Redact("Authorization: Bearer abcdef0123456789token")
	if !strings.Contains(got, "Bearer [REDACTED]") {
		t.Errorf("Redact kept-scheme = %q, want 'Bearer [REDACTED]'", got)
	}
	if strings.Contains(got, "abcdef0123456789token") {
		t.Errorf("bearer token survived: %q", got)
	}
}

func TestRedact_LeavesOrdinaryProse(t *testing.T) {
	t.Parallel()
	// Ordinary text — including short hyphenated words — must pass through.
	cases := []string{
		"",
		"Write a haiku about the sea.",
		"The well-known sci-fi author wrote about time-travel.",
		"My order number is 12345 and the SKU is AB-99.",
	}
	for _, in := range cases {
		if got := contentredact.Redact(in); got != in {
			t.Errorf("Redact(%q) = %q, expected unchanged", in, got)
		}
	}
}
