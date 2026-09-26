package main

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/proxy"
)

// TestApplyStateOverlays_DropHeaders pins issue #564: a setHeader Remove
// recorded on MutableState.DropHeaders must reach proxy.Destination.
// DropHeaders (de-duplicated against what auth already seeded), so the
// forwarder strips the inbound client header rather than only cancelling a
// rule-written value.
func TestApplyStateOverlays_DropHeaders(t *testing.T) {
	t.Parallel()
	u, _ := url.Parse("http://upstream.local/v1/chat/completions")
	dest := proxy.Destination{
		UpstreamURL:     u,
		OutgoingHeaders: http.Header{},
		DropHeaders:     []string{"X-Api-Key", "X-Internal-Token"},
	}
	state := &rules.MutableState{
		DropHeaders:     []string{"X-Internal-Token", "X-Debug"},
		OutgoingHeaders: http.Header{"X-Tier": []string{"gold"}},
	}

	applyStateOverlays(&dest, state)

	want := []string{"X-Api-Key", "X-Internal-Token", "X-Debug"}
	if len(dest.DropHeaders) != len(want) {
		t.Fatalf("DropHeaders = %v, want %v", dest.DropHeaders, want)
	}
	for i := range want {
		if dest.DropHeaders[i] != want[i] {
			t.Fatalf("DropHeaders = %v, want %v", dest.DropHeaders, want)
		}
	}
	if got := dest.OutgoingHeaders.Get("X-Tier"); got != "gold" {
		t.Fatalf("OutgoingHeaders X-Tier = %q, want gold (overlay must still apply)", got)
	}
}

func TestApplyStateOverlays_NoDropHeaders_LeavesDestUntouched(t *testing.T) {
	t.Parallel()
	dest := proxy.Destination{OutgoingHeaders: http.Header{}, DropHeaders: []string{"Authorization"}}
	applyStateOverlays(&dest, &rules.MutableState{OutgoingHeaders: http.Header{}})
	if len(dest.DropHeaders) != 1 || dest.DropHeaders[0] != "Authorization" {
		t.Fatalf("DropHeaders = %v, want [Authorization]", dest.DropHeaders)
	}
}
