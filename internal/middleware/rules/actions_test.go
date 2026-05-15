package rules

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/providers/anthropic/messages"
	"github.com/andyjmorgan/sluice-gateway/providers/gemini/content"
	openaichat "github.com/andyjmorgan/sluice-gateway/providers/openai/chat"
	openairesponses "github.com/andyjmorgan/sluice-gateway/providers/openai/responses"
)

func freshState(t *testing.T) *MutableState {
	t.Helper()
	return NewMutableState("openai", "chat_completions", nil, http.Header{})
}

func TestApplyChangeProvider(t *testing.T) {
	t.Parallel()

	s := freshState(t)
	out, err := applyAction(&contractsrules.ChangeProviderAction{NewProvider: "anthropic"}, s, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out.Terminate {
		t.Errorf("non-terminating action should not Terminate")
	}
	if s.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", s.Provider)
	}
}

func TestApplyChangeProvider_EmptyName(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	if _, err := applyAction(&contractsrules.ChangeProviderAction{NewProvider: "   "}, s, nil); !errors.Is(err, errEmptyValue) {
		t.Fatalf("err = %v, want errEmptyValue", err)
	}
}

func TestApplyChangeModelName(t *testing.T) {
	t.Parallel()

	t.Run("openai chat", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		body := &openaichat.ChatCompletionRequest{Model: "gpt-4o-mini"}
		_, err := applyAction(&contractsrules.ChangeModelNameAction{NewModelName: "gpt-4o"}, s, body)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if body.Model != "gpt-4o" {
			t.Errorf("body.Model = %q, want gpt-4o", body.Model)
		}
		if !s.BodyMutated {
			t.Errorf("BodyMutated should be true")
		}
		if got := s.PathParams["model"]; got != "gpt-4o" {
			t.Errorf("PathParams[model] = %q", got)
		}
	})

	t.Run("openai responses", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		body := &openairesponses.ResponsesRequest{Model: "gpt-4o-mini"}
		_, err := applyAction(&contractsrules.ChangeModelNameAction{NewModelName: "gpt-4o"}, s, body)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if body.Model != "gpt-4o" {
			t.Errorf("body.Model = %q", body.Model)
		}
	})

	t.Run("anthropic messages", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		body := &messages.MessagesRequest{Model: "claude-haiku-4-5"}
		_, err := applyAction(&contractsrules.ChangeModelNameAction{NewModelName: "claude-sonnet"}, s, body)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if body.Model != "claude-sonnet" {
			t.Errorf("body.Model = %q", body.Model)
		}
	})

	t.Run("gemini path-based, body untouched but path updates", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		body := &content.GenerateContentRequest{}
		_, err := applyAction(&contractsrules.ChangeModelNameAction{NewModelName: "gemini-2.0-flash"}, s, body)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := s.PathParams["model"]; got != "gemini-2.0-flash" {
			t.Errorf("PathParams[model] = %q", got)
		}
	})

	t.Run("nil body (passthrough) updates path only", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		_, err := applyAction(&contractsrules.ChangeModelNameAction{NewModelName: "x"}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := s.PathParams["model"]; got != "x" {
			t.Errorf("PathParams[model] = %q", got)
		}
	})

	t.Run("unknown body type errors", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		_, err := applyAction(&contractsrules.ChangeModelNameAction{NewModelName: "x"}, s, &struct{ Model string }{})
		if !errors.Is(err, ErrUnknownModelField) {
			t.Fatalf("err = %v, want ErrUnknownModelField", err)
		}
	})

	t.Run("empty name errors", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		_, err := applyAction(&contractsrules.ChangeModelNameAction{NewModelName: " "}, s, nil)
		if !errors.Is(err, errEmptyValue) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestApplyChangeUrl(t *testing.T) {
	t.Parallel()

	s := freshState(t)
	_, err := applyAction(&contractsrules.ChangeUrlAction{NewURL: "https://api.example.com/v1/chat"}, s, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if s.UpstreamURL == nil || s.UpstreamURL.Host != "api.example.com" {
		t.Errorf("UpstreamURL = %v", s.UpstreamURL)
	}
}

func TestApplyChangeUrl_Invalid(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	if _, err := applyAction(&contractsrules.ChangeUrlAction{NewURL: "://broken"}, s, nil); err == nil {
		t.Fatal("expected parse error for ://broken")
	}
	if _, err := applyAction(&contractsrules.ChangeUrlAction{NewURL: ""}, s, nil); !errors.Is(err, errEmptyValue) {
		t.Fatalf("err = %v, want errEmptyValue", err)
	}
}

func TestApplyChangeApiKey(t *testing.T) {
	t.Parallel()

	t.Run("explicit override", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		_, err := applyAction(&contractsrules.ChangeApiKeyAction{APIKey: "sk-replacement"}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if s.UpstreamCredentialOverride == nil || *s.UpstreamCredentialOverride != "sk-replacement" {
			t.Errorf("UpstreamCredentialOverride = %v", s.UpstreamCredentialOverride)
		}
	})

	t.Run("use sluice key sentinels with empty override", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		_, err := applyAction(&contractsrules.ChangeApiKeyAction{UseSluiceKey: true}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if s.UpstreamCredentialOverride == nil || *s.UpstreamCredentialOverride != "" {
			t.Errorf("UpstreamCredentialOverride should be empty-string sentinel, got %v", s.UpstreamCredentialOverride)
		}
	})

	t.Run("empty key errors when not useSluiceKey", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		_, err := applyAction(&contractsrules.ChangeApiKeyAction{}, s, nil)
		if !errors.Is(err, errEmptyValue) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestApplySetHeader(t *testing.T) {
	t.Parallel()

	t.Run("Set replaces existing", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		s.OutgoingHeaders.Set("X-Test", "old")
		_, err := applyAction(&contractsrules.SetHeaderAction{HeaderName: "X-Test", HeaderAction: contractsrules.HeaderSet, HeaderValue: "new"}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := s.OutgoingHeaders.Get("X-Test"); got != "new" {
			t.Errorf("X-Test = %q", got)
		}
	})

	t.Run("Append joins with comma + space on existing", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		s.OutgoingHeaders.Set("X-Test", "a")
		_, err := applyAction(&contractsrules.SetHeaderAction{HeaderName: "X-Test", HeaderAction: contractsrules.HeaderAppend, HeaderValue: "b"}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := s.OutgoingHeaders.Get("X-Test"); got != "a, b" {
			t.Errorf("X-Test = %q, want \"a, b\"", got)
		}
	})

	t.Run("Append creates when missing", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		_, err := applyAction(&contractsrules.SetHeaderAction{HeaderName: "X-New", HeaderAction: contractsrules.HeaderAppend, HeaderValue: "v"}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := s.OutgoingHeaders.Get("X-New"); got != "v" {
			t.Errorf("X-New = %q", got)
		}
	})

	t.Run("Prepend joins with comma + space on existing", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		s.OutgoingHeaders.Set("X-Test", "b")
		_, err := applyAction(&contractsrules.SetHeaderAction{HeaderName: "X-Test", HeaderAction: contractsrules.HeaderPrepend, HeaderValue: "a"}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := s.OutgoingHeaders.Get("X-Test"); got != "a, b" {
			t.Errorf("X-Test = %q", got)
		}
	})

	t.Run("Prepend creates when missing", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		_, err := applyAction(&contractsrules.SetHeaderAction{HeaderName: "X-New", HeaderAction: contractsrules.HeaderPrepend, HeaderValue: "v"}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := s.OutgoingHeaders.Get("X-New"); got != "v" {
			t.Errorf("X-New = %q", got)
		}
	})

	t.Run("Remove deletes", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		s.OutgoingHeaders.Set("X-Test", "v")
		_, err := applyAction(&contractsrules.SetHeaderAction{HeaderName: "X-Test", HeaderAction: contractsrules.HeaderRemove}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := s.OutgoingHeaders.Get("X-Test"); got != "" {
			t.Errorf("X-Test = %q, want empty", got)
		}
	})

	t.Run("unknown header action errors", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		_, err := applyAction(&contractsrules.SetHeaderAction{HeaderName: "X-Test", HeaderAction: "Toggle"}, s, nil)
		if err == nil {
			t.Fatal("expected error for unknown header action")
		}
	})

	t.Run("empty header name errors", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		_, err := applyAction(&contractsrules.SetHeaderAction{HeaderAction: contractsrules.HeaderSet, HeaderValue: "v"}, s, nil)
		if !errors.Is(err, errEmptyValue) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("nil OutgoingHeaders is materialised on first write", func(t *testing.T) {
		t.Parallel()
		s := &MutableState{}
		_, err := applyAction(&contractsrules.SetHeaderAction{HeaderName: "X-New", HeaderAction: contractsrules.HeaderSet, HeaderValue: "v"}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := s.OutgoingHeaders.Get("X-New"); got != "v" {
			t.Errorf("X-New = %q", got)
		}
	})
}

func TestApplyAppendQueryString(t *testing.T) {
	t.Parallel()

	t.Run("appends to existing query", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		u, _ := url.Parse("https://api.example.com/v1?alpha=1")
		s.UpstreamURL = u
		_, err := applyAction(&contractsrules.AppendQueryStringAction{Key: "beta", Value: "2"}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		q := s.UpstreamURL.Query()
		if q.Get("alpha") != "1" || q.Get("beta") != "2" {
			t.Errorf("query = %v", s.UpstreamURL.RawQuery)
		}
	})

	t.Run("duplicates allowed", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		u, _ := url.Parse("https://api.example.com/v1?tag=a")
		s.UpstreamURL = u
		_, err := applyAction(&contractsrules.AppendQueryStringAction{Key: "tag", Value: "b"}, s, nil)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		q := s.UpstreamURL.Query()
		if got := q["tag"]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("tag values = %v, want [a b]", got)
		}
	})

	t.Run("empty key errors", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		u, _ := url.Parse("https://api.example.com/v1")
		s.UpstreamURL = u
		_, err := applyAction(&contractsrules.AppendQueryStringAction{Value: "x"}, s, nil)
		if !errors.Is(err, errEmptyValue) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("missing URL errors", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		_, err := applyAction(&contractsrules.AppendQueryStringAction{Key: "k", Value: "v"}, s, nil)
		if err == nil {
			t.Fatal("expected error when UpstreamURL is nil")
		}
	})
}

func TestApplyAction_NilAndUnknownActions(t *testing.T) {
	t.Parallel()

	if _, err := applyAction(nil, freshState(t), nil); err != nil {
		t.Errorf("nil action should be a no-op, got err %v", err)
	}

	if _, err := applyAction(&contractsrules.UnknownAction{Type: "future"}, freshState(t), nil); err != nil {
		t.Errorf("UnknownAction should be a no-op, got err %v", err)
	}
}
