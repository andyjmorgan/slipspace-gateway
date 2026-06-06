package rules_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/middleware/rules"
	openaichat "github.com/andyjmorgan/sluice-gateway/protocols/openai/chat"
)

// newGC builds the GatewayContext most evaluator tests need. The
// "unknown configuration" case constructs its own GatewayContext
// inline so it can supply a different ConfigurationName.
func newGC() rules.GatewayContext {
	return rules.GatewayContext{
		Provider:          "openai",
		Protocol:          "chat_completions",
		Model:             "gpt-4o-mini",
		ConfigurationName: "dev",
	}
}

func setHeaderAction(name, value string) contractsrules.Action {
	return &contractsrules.SetHeaderAction{
		Type:         "setHeader",
		HeaderName:   name,
		HeaderAction: contractsrules.HeaderSet,
		HeaderValue:  value,
	}
}

func providerCondition(p string) contractsrules.Condition {
	return &contractsrules.ProviderCondition{
		Type:             "provider",
		Operator:         contractsrules.EnumEquals,
		ExpectedProvider: p,
	}
}

func TestEvaluator_EmptyRules_NoOp(t *testing.T) {
	t.Parallel()
	e := rules.NewEvaluator(nil, 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	out, err := e.Evaluate(context.Background(), newGC(), state, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if out.Outcome.Terminate {
		t.Errorf("empty rule set should not terminate")
	}
}

func TestEvaluator_SingleRule_AppliesAction(t *testing.T) {
	t.Parallel()
	r1 := &contractsrules.RuleContract{
		Name:      "tag-openai",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Sluice-Tagged", "yes")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{
		"dev": {r1},
	}), 8, nil)

	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())

	out, err := e.Evaluate(ctx, newGC(), state, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if out.Outcome.Terminate {
		t.Errorf("unexpected Terminate")
	}
	if got := state.OutgoingHeaders.Get("X-Sluice-Tagged"); got != "yes" {
		t.Errorf("header = %q, want yes", got)
	}
	records := buf.Drain()
	if len(records) != 1 {
		t.Fatalf("got %d matches, want 1", len(records))
	}
	rec := records[0]
	if rec.RuleName != "tag-openai" || rec.Configuration != "dev" {
		t.Errorf("record = %+v", rec)
	}
	if len(rec.ActionsApplied) != 1 || rec.ActionsApplied[0] != "setHeader" {
		t.Errorf("ActionsApplied = %v", rec.ActionsApplied)
	}
	if rec.Terminated {
		t.Errorf("Terminated should be false for non-terminating action")
	}
	if rec.MatchedAt.IsZero() {
		t.Errorf("MatchedAt should be set")
	}
}

func TestEvaluator_RuleIDPropagates(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	r := &contractsrules.RuleContract{
		ID:        &id,
		Name:      "with-id",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Test", "v")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {r}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())
	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	records := buf.Drain()
	if len(records) != 1 || records[0].RuleID != id.String() {
		t.Errorf("RuleID = %q, want %s", records[0].RuleID, id.String())
	}
}

func TestEvaluator_NoMatch_NoRecord(t *testing.T) {
	t.Parallel()
	r := &contractsrules.RuleContract{
		Name:      "anthropic-only",
		Condition: providerCondition("anthropic"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Test", "v")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {r}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())
	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := state.OutgoingHeaders.Get("X-Test"); got != "" {
		t.Errorf("header should be untouched, got %q", got)
	}
	if records := buf.Drain(); len(records) != 0 {
		t.Errorf("no rules matched, expected 0 records, got %d", len(records))
	}
}

func TestEvaluator_BehaviorExit_HaltsIteration(t *testing.T) {
	t.Parallel()
	first := &contractsrules.RuleContract{
		Name:      "first",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-First", "yes")},
		Behavior:  contractsrules.BehaviorExit,
	}
	second := &contractsrules.RuleContract{
		Name:      "second",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Second", "yes")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {first, second}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())
	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := state.OutgoingHeaders.Get("X-First"); got != "yes" {
		t.Errorf("X-First = %q", got)
	}
	if got := state.OutgoingHeaders.Get("X-Second"); got != "" {
		t.Errorf("X-Second should be empty (Exit halted iteration), got %q", got)
	}
	records := buf.Drain()
	if len(records) != 1 || records[0].RuleName != "first" {
		t.Errorf("Behavior=Exit should record only first rule, got %+v", records)
	}
}

func TestEvaluator_BehaviorContinue_IsDefault(t *testing.T) {
	t.Parallel()
	first := &contractsrules.RuleContract{
		Name:      "first",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-First", "yes")},
	}
	second := &contractsrules.RuleContract{
		Name:      "second",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Second", "yes")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {first, second}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, _ := rules.WithMatchBuffer(context.Background())
	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := state.OutgoingHeaders.Get("X-First"); got != "yes" {
		t.Errorf("X-First = %q", got)
	}
	if got := state.OutgoingHeaders.Get("X-Second"); got != "yes" {
		t.Errorf("X-Second = %q (default Continue should reach the second rule)", got)
	}
}

func TestEvaluator_TypedBody_ChangeModelName(t *testing.T) {
	t.Parallel()
	r := &contractsrules.RuleContract{
		Name:      "rewrite-model",
		Condition: providerCondition("openai"),
		Actions: []contractsrules.Action{
			&contractsrules.ChangeModelNameAction{Type: "changeModelName", NewModelName: "gpt-4o"},
		},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {r}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	body := &openaichat.ChatCompletionRequest{Model: "gpt-3.5"}
	ctx, _ := rules.WithMatchBuffer(context.Background())

	if _, err := e.Evaluate(ctx, newGC(), state, body); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if body.Model != "gpt-4o" {
		t.Errorf("body.Model = %q, want gpt-4o", body.Model)
	}
	if !state.BodyMutated {
		t.Errorf("BodyMutated should be true after changeModelName")
	}
}

func TestEvaluator_ActionError_RecordedOnEvent(t *testing.T) {
	t.Parallel()
	r := &contractsrules.RuleContract{
		Name:      "bad-url",
		Condition: providerCondition("openai"),
		Actions: []contractsrules.Action{
			&contractsrules.ChangeUrlAction{Type: "changeUrl", NewURL: "://busted"},
		},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {r}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())
	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	records := buf.Drain()
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].ErrorMessage == "" {
		t.Errorf("ErrorMessage should carry the action error")
	}
	if len(records[0].ActionsApplied) != 1 || records[0].ActionsApplied[0] != "changeUrl" {
		t.Errorf("ActionsApplied = %v (action type should be recorded even on error)", records[0].ActionsApplied)
	}
}

func TestEvaluator_UnknownConfigName_NoOp(t *testing.T) {
	t.Parallel()
	r := &contractsrules.RuleContract{
		Name:      "ghost",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X", "v")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"prod": {r}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())
	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if records := buf.Drain(); len(records) != 0 {
		t.Errorf("unknown config should have no records, got %d", len(records))
	}
}

func TestEvaluator_NilReceiver(t *testing.T) {
	t.Parallel()
	var e *rules.Evaluator
	if _, err := e.Evaluate(context.Background(), newGC(), nil, nil); err != nil {
		t.Errorf("nil evaluator should be a no-op, got %v", err)
	}
}

func TestEvaluator_DefaultMaxGroupDepth(t *testing.T) {
	t.Parallel()
	// Passing 0 (or negative) should fall back to the package default.
	e := rules.NewEvaluator(nil, 0, nil)
	if e == nil {
		t.Fatal("nil evaluator returned")
	}
}

// TestEvaluator_PicksUpStoreReplace proves that swapping the underlying
// config snapshot causes the next Evaluate call to read the new rule
// library. This is the load-bearing test for the read-through path that
// Phase 2's admin write endpoints will rely on.
func TestEvaluator_PicksUpStoreReplace(t *testing.T) {
	t.Parallel()

	tag := func(name, value string) *contractsrules.RuleContract {
		return &contractsrules.RuleContract{
			Name:      name,
			Condition: providerCondition("openai"),
			Actions:   []contractsrules.Action{setHeaderAction("X-Sluice-Tagged", value)},
		}
	}
	initialRule := tag("initial", "first")
	replacedRule := tag("replaced", "second")

	store := config.NewStore(&config.ResolvedConfig{
		PerConfigurationRules: map[string][]*contractsrules.RuleContract{
			"dev": {initialRule},
		},
	})
	e := rules.NewEvaluator(store, 8, nil)

	mustEval := func(label string, wantRule string) {
		t.Helper()
		state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
		ctx, buf := rules.WithMatchBuffer(context.Background())
		if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
			t.Fatalf("%s: Evaluate: %v", label, err)
		}
		matches := buf.Drain()
		if len(matches) != 1 {
			t.Fatalf("%s: matches=%d, want 1", label, len(matches))
		}
		if matches[0].RuleName != wantRule {
			t.Fatalf("%s: rule=%q, want %q", label, matches[0].RuleName, wantRule)
		}
	}

	mustEval("pre-swap", "initial")

	store.Replace(&config.ResolvedConfig{
		PerConfigurationRules: map[string][]*contractsrules.RuleContract{
			"dev": {replacedRule},
		},
	})

	mustEval("post-swap", "replaced")
}
