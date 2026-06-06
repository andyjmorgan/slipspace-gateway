package rules

import (
	"net/http"
	"net/url"
	"testing"
)

func TestNewMutableState_SeedsFromRouting(t *testing.T) {
	t.Parallel()

	s := NewMutableState("openai", "chat_completions", "", map[string]string{"model": "gpt-4o-mini"},
		http.Header{"X-Test": []string{"v1"}}, // intentionally ignored
	)

	if s.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", s.Provider)
	}
	if s.Protocol != "chat_completions" {
		t.Errorf("Protocol = %q, want chat_completions", s.Protocol)
	}
	if got := s.PathParams["model"]; got != "gpt-4o-mini" {
		t.Errorf("PathParams[model] = %q, want gpt-4o-mini", got)
	}
	if got := s.OutgoingHeaders.Get("X-Test"); got != "" {
		t.Errorf("OutgoingHeaders should start empty; got X-Test = %q", got)
	}
	if s.UpstreamURL != nil {
		t.Errorf("UpstreamURL should be nil pre-mutation, got %v", s.UpstreamURL)
	}
	if s.UpstreamCredentialOverride != nil {
		t.Errorf("UpstreamCredentialOverride should be nil pre-mutation")
	}
	if s.BodyMutated {
		t.Errorf("BodyMutated should be false pre-mutation")
	}
}

func TestNewMutableState_IsolatesFromCaller(t *testing.T) {
	t.Parallel()

	// Caller mutates path params after construction; the state must not
	// see the change. Confirms the constructor clones rather than
	// retains. (Outgoing headers do not seed from the inbound set, so
	// no leak check needed there.)
	srcParams := map[string]string{"model": "gpt-4o"}
	s := NewMutableState("openai", "chat_completions", "", srcParams, nil)
	srcParams["model"] = "claude-3"

	if got := s.PathParams["model"]; got != "gpt-4o" {
		t.Errorf("PathParams clone leaked: got %q, want gpt-4o", got)
	}
}

func TestNewMutableState_NilInputs(t *testing.T) {
	t.Parallel()

	s := NewMutableState("openai", "chat_completions", "", nil, nil)
	if s.PathParams == nil {
		t.Errorf("PathParams should be initialised to empty map, got nil")
	}
	if s.OutgoingHeaders == nil {
		t.Errorf("OutgoingHeaders should be initialised to empty Header, got nil")
	}
	if len(s.PathParams) != 0 {
		t.Errorf("PathParams should be empty, got %v", s.PathParams)
	}
	if len(s.OutgoingHeaders) != 0 {
		t.Errorf("OutgoingHeaders should be empty, got %v", s.OutgoingHeaders)
	}
}

func TestMutableState_OverrideAssignments(t *testing.T) {
	t.Parallel()

	s := NewMutableState("openai", "chat_completions", "", nil, nil)

	u, _ := url.Parse("https://example.com/v1/chat/completions")
	s.UpstreamURL = u
	if s.UpstreamURL.Host != "example.com" {
		t.Errorf("UpstreamURL.Host = %q", s.UpstreamURL.Host)
	}

	cred := "sk-replacement"
	s.UpstreamCredentialOverride = &cred
	if got := *s.UpstreamCredentialOverride; got != "sk-replacement" {
		t.Errorf("UpstreamCredentialOverride = %q", got)
	}

	s.BodyMutated = true
	if !s.BodyMutated {
		t.Errorf("BodyMutated assignment did not stick")
	}
}
