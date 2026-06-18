package rules_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
)

func TestMutableState_Clone_NilReceiver(t *testing.T) {
	t.Parallel()
	var s *rules.MutableState
	if got := s.Clone(); got != nil {
		t.Errorf("nil.Clone() = %+v; want nil", got)
	}
}

func TestMutableState_Clone_EmptyState(t *testing.T) {
	t.Parallel()
	s := &rules.MutableState{}
	clone := s.Clone()
	if clone == nil {
		t.Fatal("Clone returned nil for empty state")
	}
	if clone.OutgoingHeaders == nil {
		t.Error("Clone left OutgoingHeaders nil; expected an empty map")
	}
}

func TestMutableState_Clone_DeepCopiesAllFields(t *testing.T) {
	t.Parallel()
	u, _ := url.Parse("https://example.com/v1/chat")
	cred := "sk-test"
	s := &rules.MutableState{
		Provider:                   "openai",
		Protocol:                   "chat_completions",
		SourceProtocol:             "messages",
		UpstreamURL:                u,
		OutgoingHeaders:            http.Header{"X-A": []string{"1"}, "X-B": []string{"2"}},
		UpstreamCredentialOverride: &cred,
		PathParams:                 map[string]string{"model": "gpt-4o", "version": "v1"},
		BodyMutated:                true,
		QueryAdditions:             []rules.QueryAddition{{Key: "k", Value: "v"}},
		Tags:                       []string{"a", "b"},
		PolicyRef:                  "ha",
	}

	clone := s.Clone()

	// SourceProtocol must survive the clone — it is load-bearing for the
	// translate path's per-attempt response leg (regression: it was missing
	// from Clone when first added).
	if clone.SourceProtocol != "messages" {
		t.Errorf("clone.SourceProtocol = %q; want messages", clone.SourceProtocol)
	}

	// Mutate the clone and prove the original is untouched.
	clone.Provider = "anthropic"
	clone.UpstreamURL.Path = "/different"
	clone.OutgoingHeaders.Set("X-A", "mutated")
	*clone.UpstreamCredentialOverride = "rotated"
	clone.PathParams["model"] = "claude"
	clone.QueryAdditions = append(clone.QueryAdditions, rules.QueryAddition{Key: "x", Value: "y"})
	clone.Tags = append(clone.Tags, "c")
	clone.PolicyRef = "lb"

	if s.Provider != "openai" {
		t.Errorf("Provider mutated: %q", s.Provider)
	}
	if s.UpstreamURL.Path != "/v1/chat" {
		t.Errorf("UpstreamURL.Path mutated: %q", s.UpstreamURL.Path)
	}
	if s.OutgoingHeaders.Get("X-A") != "1" {
		t.Errorf("OutgoingHeaders mutated: %v", s.OutgoingHeaders)
	}
	if *s.UpstreamCredentialOverride != "sk-test" {
		t.Errorf("UpstreamCredentialOverride mutated: %q", *s.UpstreamCredentialOverride)
	}
	if s.PathParams["model"] != "gpt-4o" {
		t.Errorf("PathParams mutated: %v", s.PathParams)
	}
	if len(s.QueryAdditions) != 1 {
		t.Errorf("QueryAdditions mutated: %v", s.QueryAdditions)
	}
	if len(s.Tags) != 2 {
		t.Errorf("Tags mutated: %v", s.Tags)
	}
	if s.PolicyRef != "ha" {
		t.Errorf("PolicyRef mutated: %q", s.PolicyRef)
	}
}

func TestMutableState_Clone_NilFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	s := &rules.MutableState{
		Provider: "openai",
		// All pointer/slice/map fields left nil except the ones the
		// constructor would normally fill.
	}
	clone := s.Clone()

	if clone.UpstreamURL != nil {
		t.Error("Clone allocated UpstreamURL for nil source")
	}
	if clone.UpstreamCredentialOverride != nil {
		t.Error("Clone allocated UpstreamCredentialOverride for nil source")
	}
	if clone.PathParams != nil {
		t.Error("Clone allocated PathParams for nil source")
	}
	if clone.QueryAdditions != nil {
		t.Error("Clone allocated QueryAdditions for nil source")
	}
	if clone.Tags != nil {
		t.Error("Clone allocated Tags for nil source")
	}
}
