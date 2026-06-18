package rules_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
)

func stubMatch(provider, endpoint string) rules.MatchFromContextFunc {
	return func(context.Context) (string, string, string, map[string]string, bool) {
		return provider, endpoint, "", nil, true
	}
}

func TestHTTPHandler_NilArgsPanic(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		fn   func()
	}{
		{"nil evaluator", func() {
			rules.HTTPHandler(nil, stubMatch("o", "e"), nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		}},
		{"nil next", func() { rules.HTTPHandler(rules.NewEvaluator(nil, 8, nil), stubMatch("o", "e"), nil, nil) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected panic")
				}
			}()
			tc.fn()
		})
	}
}

func TestHTTPHandler_RuleAppliesStashesState(t *testing.T) {
	t.Parallel()
	r := &contractsrules.RuleContract{
		Name:      "tag",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Tagged", "yes")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"": {r}}), 8, nil)

	var stateSeen *rules.MutableState
	next := http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		stateSeen = rules.MutableStateFromContext(req.Context())
	})
	h := rules.HTTPHandler(e, stubMatch("openai", "chat_completions"), nil, next)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if stateSeen == nil {
		t.Fatal("MutableState not stashed on context")
	}
	if got := stateSeen.OutgoingHeaders.Get("X-Tagged"); got != "yes" {
		t.Errorf("rule did not fire; X-Tagged = %q", got)
	}
}

func TestHTTPHandler_NoMatchOnContext_500(t *testing.T) {
	t.Parallel()
	e := rules.NewEvaluator(nil, 8, nil)
	noMatch := func(context.Context) (string, string, string, map[string]string, bool) {
		return "", "", "", nil, false
	}
	h := rules.HTTPHandler(e, noMatch, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next should not be called when matchFrom fails")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestHTTPHandler_EmptyRules_PassesThrough(t *testing.T) {
	t.Parallel()
	e := rules.NewEvaluator(nil, 8, nil)
	called := false
	h := rules.HTTPHandler(e, stubMatch("openai", "chat_completions"), nil, http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		called = true
		if rules.MutableStateFromContext(req.Context()) == nil {
			t.Error("MutableState should be set even with empty rules")
		}
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Error("next should have been called")
	}
}

// TestHTTPHandler_ExtractInboundModel_PathParamsBranch covers the
// PathParams-first branch of extractInboundModel (Gemini's URL-
// templated model). Without this case, the only branch hit by other
// tests is the body-extraction delegate to bodycapture.Model — the
// early return on a non-empty path-param model goes unexercised.
func TestHTTPHandler_ExtractInboundModel_PathParamsBranch(t *testing.T) {
	t.Parallel()
	tagOnGemini := &contractsrules.RuleContract{
		Name:      "tag-gemini",
		Condition: modelCondition(contractsrules.StringEquals, "gemini-2.0-flash-001"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Match", "yes")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"": {tagOnGemini}}), 8, nil)
	matchWithParams := func(context.Context) (string, string, string, map[string]string, bool) {
		return "gemini", "generate_content", "/v1beta/models/{model}:generateContent", map[string]string{"model": "gemini-2.0-flash-001"}, true
	}

	var stateSeen *rules.MutableState
	next := http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		stateSeen = rules.MutableStateFromContext(req.Context())
	})
	h := rules.HTTPHandler(e, matchWithParams, nil, next)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash-001:generateContent", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if stateSeen == nil {
		t.Fatal("MutableState not stashed on context")
	}
	if got := stateSeen.OutgoingHeaders.Get("X-Match"); got != "yes" {
		t.Errorf("modelName condition did NOT match against PathParams[model]; got header %q", got)
	}
}

func TestMutableStateFromContext_Nil(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // exercising the nil-ctx defensive branch
	if rules.MutableStateFromContext(nil) != nil {
		t.Error("nil ctx should yield nil state")
	}
	if rules.MutableStateFromContext(context.Background()) != nil {
		t.Error("unsalted ctx should yield nil state")
	}
}
