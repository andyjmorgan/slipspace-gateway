package rules

import (
	"net/http"
	"net/url"
	"testing"
)

func TestNewMutableState_SeedsFromRouting(t *testing.T) {
	t.Parallel()

	headers := http.Header{}
	headers.Set("X-Test", "v1")
	headers.Add("Accept", "application/json")

	s := NewMutableState("openai", "chat_completions",
		map[string]string{"model": "gpt-4o-mini"},
		headers,
	)

	if s.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", s.Provider)
	}
	if s.Endpoint != "chat_completions" {
		t.Errorf("Endpoint = %q, want chat_completions", s.Endpoint)
	}
	if got := s.PathParams["model"]; got != "gpt-4o-mini" {
		t.Errorf("PathParams[model] = %q, want gpt-4o-mini", got)
	}
	if got := s.OutgoingHeaders.Get("X-Test"); got != "v1" {
		t.Errorf("OutgoingHeaders X-Test = %q, want v1", got)
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

	// Caller mutates inputs after construction; the state must not see
	// the change. Confirms the constructor clones rather than retains.
	srcParams := map[string]string{"model": "gpt-4o"}
	srcHeaders := http.Header{}
	srcHeaders.Set("X-Original", "yes")

	s := NewMutableState("openai", "chat_completions", srcParams, srcHeaders)

	srcParams["model"] = "claude-3"
	srcHeaders.Set("X-Original", "no")
	srcHeaders.Set("X-Added", "later")

	if got := s.PathParams["model"]; got != "gpt-4o" {
		t.Errorf("PathParams clone leaked: got %q, want gpt-4o", got)
	}
	if got := s.OutgoingHeaders.Get("X-Original"); got != "yes" {
		t.Errorf("Headers clone leaked: X-Original = %q, want yes", got)
	}
	if got := s.OutgoingHeaders.Get("X-Added"); got != "" {
		t.Errorf("Headers clone leaked: X-Added should be empty, got %q", got)
	}
}

func TestNewMutableState_NilInputs(t *testing.T) {
	t.Parallel()

	s := NewMutableState("openai", "chat_completions", nil, nil)
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

	s := NewMutableState("openai", "chat_completions", nil, nil)

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
