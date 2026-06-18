package rules_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/contracts/events"
	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/proxy"
)

func returnStatusRule(name, body string, status int, bt contractsrules.StatusCodeBodyType) *contractsrules.RuleContract {
	return &contractsrules.RuleContract{
		Name:      name,
		Condition: providerCondition("openai"),
		Actions: []contractsrules.Action{
			&contractsrules.ReturnStatusCodeAction{
				Type: "returnStatusCode", StatusCode: status, Body: body, BodyType: bt,
			},
		},
	}
}

func TestEvaluator_ReturnStatusCode_TerminatesWithResponse(t *testing.T) {
	t.Parallel()
	r := returnStatusRule("block-pii", `{"error":"blocked"}`, 403, contractsrules.StatusBodyJSON)
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {r}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())

	result, err := e.Evaluate(ctx, newGC(), state, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.Outcome.Terminate {
		t.Fatal("expected Terminate=true")
	}
	if result.Outcome.Response == nil {
		t.Fatal("expected Response to be populated")
	}
	if result.Outcome.Response.StatusCode != 403 {
		t.Errorf("StatusCode = %d, want 403", result.Outcome.Response.StatusCode)
	}
	if string(result.Outcome.Response.Body) != `{"error":"blocked"}` {
		t.Errorf("Body = %q", result.Outcome.Response.Body)
	}
	if result.SourceRule == nil || result.SourceRule.Name != "block-pii" {
		t.Errorf("SourceRule = %+v", result.SourceRule)
	}
	records := buf.Drain()
	if len(records) != 1 || !records[0].Terminated {
		t.Errorf("Terminated should be true on rule.matched, got %+v", records)
	}
}

func TestEvaluator_ReturnStatusCode_StopsActionLoop(t *testing.T) {
	t.Parallel()
	// Pair returnStatusCode with a setHeader after it. The setHeader
	// must NOT fire because the terminating action short-circuited.
	r := &contractsrules.RuleContract{
		Name:      "terminating-first",
		Condition: providerCondition("openai"),
		Actions: []contractsrules.Action{
			&contractsrules.ReturnStatusCodeAction{Type: "returnStatusCode", StatusCode: 429, BodyType: contractsrules.StatusBodyText},
			setHeaderAction("X-Should-Not-Set", "v"),
		},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {r}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, _ := rules.WithMatchBuffer(context.Background())
	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := state.OutgoingHeaders.Get("X-Should-Not-Set"); got != "" {
		t.Errorf("subsequent action ran after terminating action; X-Should-Not-Set = %q", got)
	}
}

func TestEvaluator_ReturnStatusCode_StopsRuleLoop(t *testing.T) {
	t.Parallel()
	first := &contractsrules.RuleContract{
		Name:      "synthetic",
		Condition: providerCondition("openai"),
		Actions: []contractsrules.Action{
			&contractsrules.ReturnStatusCodeAction{Type: "returnStatusCode", StatusCode: 429, BodyType: contractsrules.StatusBodyText},
		},
	}
	second := &contractsrules.RuleContract{
		Name:      "tag",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Should-Not-Set", "v")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {first, second}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, _ := rules.WithMatchBuffer(context.Background())
	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := state.OutgoingHeaders.Get("X-Should-Not-Set"); got != "" {
		t.Errorf("second rule should not have run; X-Should-Not-Set = %q", got)
	}
}

func TestEvaluator_ReturnStatusCode_ClampsBadStatus(t *testing.T) {
	t.Parallel()
	r := returnStatusRule("oops", "x", 99, contractsrules.StatusBodyText)
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {r}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, _ := rules.WithMatchBuffer(context.Background())
	result, _ := e.Evaluate(ctx, newGC(), state, nil)
	if result.Outcome.Response.StatusCode != 500 {
		t.Errorf("out-of-band status should clamp to 500, got %d", result.Outcome.Response.StatusCode)
	}
}

// lifecycleObserver records which Observer lifecycle methods fired
// so the synthetic-path test can assert OnRequestStart →
// OnResponseHeaders → OnComplete ran in order.
type lifecycleObserver struct {
	gotStart, gotHeaders, gotComplete bool
	completeStatus                    int
}

func (o *lifecycleObserver) OnRequestStart(context.Context, proxy.Destination) { o.gotStart = true }

func (o *lifecycleObserver) OnResponseHeaders(_ context.Context, status int, _ http.Header, _ bool) {
	o.gotHeaders = true
	o.completeStatus = status
}

func (o *lifecycleObserver) OnResponseChunk(context.Context, time.Time) {}

func (o *lifecycleObserver) OnUpstreamError(context.Context, error) {}

func (o *lifecycleObserver) OnComplete(_ context.Context, status int, _ int64) {
	o.gotComplete = true
	o.completeStatus = status
}

func (o *lifecycleObserver) OnRuleMatched(context.Context, events.RuleMatched) {}

func TestHTTPHandler_SyntheticPath_WritesResponseAndDrivesLifecycle(t *testing.T) {
	t.Parallel()

	r := returnStatusRule("block-pii", "blocked", 429, contractsrules.StatusBodyText)
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"": {r}}), 8, nil)

	obs := &lifecycleObserver{}
	factory := func(context.Context, proxy.Destination) proxy.Observer { return obs }

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next should not be invoked on synthetic path")
	})
	h := rules.HTTPHandler(e, stubMatch("openai", "chat_completions"), factory, next)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 429 {
		t.Errorf("status = %d, want 429", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	if string(body) != "blocked" {
		t.Errorf("body = %q, want blocked", body)
	}
	if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := w.Header().Get("X-Sluice-Synthetic"); got != "rule:block-pii" {
		t.Errorf("X-Sluice-Synthetic = %q, want rule:block-pii", got)
	}
	if !obs.gotStart || !obs.gotHeaders || !obs.gotComplete {
		t.Errorf("lifecycle incomplete: start=%v headers=%v complete=%v", obs.gotStart, obs.gotHeaders, obs.gotComplete)
	}
	if obs.completeStatus != 429 {
		t.Errorf("observer saw status %d, want 429", obs.completeStatus)
	}
}

func TestHTTPHandler_SyntheticPath_BodyTypeMaps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		bt   contractsrules.StatusCodeBodyType
		want string
	}{
		{contractsrules.StatusBodyJSON, "application/json"},
		{contractsrules.StatusBodyHTML, "text/html; charset=utf-8"},
		{contractsrules.StatusBodyText, "text/plain; charset=utf-8"},
		{contractsrules.StatusCodeBodyType("other"), "text/plain; charset=utf-8"},
	}
	for _, tc := range cases {
		t.Run(string(tc.bt), func(t *testing.T) {
			t.Parallel()
			r := returnStatusRule("t", "x", 418, tc.bt)
			e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"": {r}}), 8, nil)
			h := rules.HTTPHandler(e, stubMatch("openai", "chat_completions"), nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if got := w.Header().Get("Content-Type"); got != tc.want {
				t.Errorf("Content-Type = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHTTPHandler_SyntheticPath_NilFactoryWorks(t *testing.T) {
	t.Parallel()
	// A nil ObserverFactory must not crash — the synthetic path
	// degrades to "write the response, skip lifecycle". Useful for
	// tests and for non-reporter wiring.
	r := returnStatusRule("t", "ok", 418, contractsrules.StatusBodyText)
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"": {r}}), 8, nil)
	h := rules.HTTPHandler(e, stubMatch("openai", "chat_completions"), nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 418 {
		t.Errorf("status = %d, want 418", w.Code)
	}
}

func TestSyntheticOutcomeFromContext_Nil(t *testing.T) {
	t.Parallel()
	if _, ok := rules.SyntheticOutcomeFromContext(nil); ok { //nolint:staticcheck // exercising the nil-ctx defensive branch
		t.Error("nil ctx should yield ok=false")
	}
	if _, ok := rules.SyntheticOutcomeFromContext(context.Background()); ok {
		t.Error("unsalted ctx should yield ok=false")
	}
}

func TestApplyLlmImpersonation_TerminatesWithPlainText(t *testing.T) {
	t.Parallel()
	r := &contractsrules.RuleContract{
		Name:      "imp",
		Condition: providerCondition("openai"),
		Actions: []contractsrules.Action{
			&contractsrules.LlmImpersonationAction{
				Type:    "llmImpersonation",
				Message: "Blocked: PII filter triggered",
			},
		},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {r}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())

	result, err := e.Evaluate(ctx, newGC(), state, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.Outcome.Terminate {
		t.Fatal("expected Terminate=true")
	}
	if result.Outcome.Response == nil {
		t.Fatal("expected Response populated")
	}
	if result.Outcome.Response.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", result.Outcome.Response.StatusCode)
	}
	if string(result.Outcome.Response.Body) != "Blocked: PII filter triggered" {
		t.Errorf("Body = %q", result.Outcome.Response.Body)
	}
	if result.Outcome.Response.BodyType != contractsrules.StatusBodyText {
		t.Errorf("BodyType = %q, want text", result.Outcome.Response.BodyType)
	}
	if records := buf.Drain(); len(records) != 1 || !records[0].Terminated {
		t.Errorf("rule.matched record terminated=true expected, got %+v", records)
	}
}

func TestApplyLlmImpersonation_EmptyMessageErrors(t *testing.T) {
	t.Parallel()
	r := &contractsrules.RuleContract{
		Name:      "imp",
		Condition: providerCondition("openai"),
		Actions: []contractsrules.Action{
			&contractsrules.LlmImpersonationAction{Type: "llmImpersonation", Message: "   "},
		},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {r}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())

	result, _ := e.Evaluate(ctx, newGC(), state, nil)
	if result.Outcome.Terminate {
		t.Error("empty message should not terminate (action errored)")
	}
	records := buf.Drain()
	if len(records) != 1 || records[0].ErrorMessage == "" {
		t.Errorf("rule.matched should carry ErrorMessage; got %+v", records)
	}
}

func TestSyntheticOutcomeFromContext_RoundTrip(t *testing.T) {
	t.Parallel()
	// Cannot use unexported withSyntheticOutcome from outside the
	// package; instead exercise the full path through the middleware
	// to install it.
	r := returnStatusRule("t", "x", 418, contractsrules.StatusBodyText)
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"": {r}}), 8, nil)
	var resultSeen rules.Result
	wrap := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next should not run on synthetic")
	})
	captureFactory := func(ctx context.Context, _ proxy.Destination) proxy.Observer {
		if out, ok := rules.SyntheticOutcomeFromContext(ctx); ok {
			resultSeen = out
		}
		return &lifecycleObserver{}
	}
	h := rules.HTTPHandler(e, stubMatch("openai", "chat_completions"), captureFactory, wrap)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil))
	if !resultSeen.Outcome.Terminate {
		t.Fatal("synthetic outcome not stashed on ctx for factory")
	}
	if resultSeen.SourceRule == nil || !strings.HasPrefix(resultSeen.SourceRule.Name, "t") {
		t.Errorf("SourceRule = %+v", resultSeen.SourceRule)
	}
}
