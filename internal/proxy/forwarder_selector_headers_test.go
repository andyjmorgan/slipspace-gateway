package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/auth"
)

// TestForward_SelectorHeadersNeverReachUpstream pins issue #558: every
// spelling the auth resolver accepts as a configuration selector — current
// and legacy — is stripped by the forwarder's unconditional backstop even
// when the Destination carries no DropHeaders at all. The legacy identity
// header carries a live api-key secret, so this must not depend on the auth
// middleware having seeded the drop list.
func TestForward_SelectorHeadersNeverReachUpstream(t *testing.T) {
	selectors := []string{
		auth.HeaderIdentity,
		auth.HeaderConfiguration,
		auth.LegacyHeaderIdentity,
		auth.LegacyHeaderConfiguration,
	}

	var captured http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	f := New(Options{})
	req := httptest.NewRequest(http.MethodPost, "http://gateway.local/v1/messages", strings.NewReader("{}"))
	for _, name := range selectors {
		req.Header.Set(name, "sk_live_secret_for_"+name)
	}
	req.Header.Set("X-Keep", "yes")

	// Deliberately no DropHeaders and no OutgoingHeaders: the backstop alone
	// must hold.
	dest := newDestination(t, upstream.URL+"/v1/messages")
	if len(dest.DropHeaders) != 0 {
		t.Fatalf("test precondition: DropHeaders must be empty, got %v", dest.DropHeaders)
	}

	if _, err := f.Forward(context.Background(), httptest.NewRecorder(), req, dest); err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if captured == nil {
		t.Fatal("upstream saw no request")
	}
	for _, name := range selectors {
		if got := captured.Get(name); got != "" {
			t.Errorf("%s reached upstream (%q); alwaysDropHeaders must strip it", name, got)
		}
	}
	if got := captured.Get("X-Keep"); got != "yes" {
		t.Errorf("X-Keep should be forwarded, got %q", got)
	}
}

// TestAlwaysDropHeaders_CoversEverySelectorSpelling keeps the forwarder's
// backstop and the auth resolver's accepted selector set in lockstep: if a
// spelling is added to (or removed from) auth without touching this list,
// this test fails rather than a secret quietly forwarding.
func TestAlwaysDropHeaders_CoversEverySelectorSpelling(t *testing.T) {
	want := []string{
		auth.HeaderIdentity,
		auth.HeaderConfiguration,
		auth.LegacyHeaderIdentity,
		auth.LegacyHeaderConfiguration,
		auth.HeaderAuthorization,
	}
	have := make(map[string]bool, len(alwaysDropHeaders))
	for _, h := range alwaysDropHeaders {
		have[http.CanonicalHeaderKey(h)] = true
	}
	for _, name := range want {
		if !have[http.CanonicalHeaderKey(name)] {
			t.Errorf("alwaysDropHeaders is missing %s", name)
		}
	}
}
