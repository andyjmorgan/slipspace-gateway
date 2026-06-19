package rules_test

import (
	"context"
	"net/http"
	"testing"

	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/slipspace-gateway/protocols/gemini/content"
	openaichat "github.com/andyjmorgan/slipspace-gateway/protocols/openai/chat"
	openairesponses "github.com/andyjmorgan/slipspace-gateway/protocols/openai/responses"
)

// The cascade tests assert the v1.0.5 semantic change: a rule's
// Condition reads the live state every prior rule's action left
// behind, not the frozen GatewayContext from the start of Evaluate.
// Each test isolates one cascade dimension (provider, model,
// protocol, header) so a regression maps unambiguously to the
// affected condition kind.

func modelCondition(operator contractsrules.StringOperator, expected string) contractsrules.Condition {
	return &contractsrules.ModelNameCondition{
		Type:              "modelName",
		Operator:          operator,
		ExpectedModelName: expected,
	}
}

func protocolCondition(expected string) contractsrules.Condition {
	return &contractsrules.ProtocolCondition{
		Type:             "protocol",
		Operator:         contractsrules.EnumEquals,
		ExpectedProtocol: expected,
	}
}

func headerCondition(name, value string) contractsrules.Condition {
	valueOp := contractsrules.StringEquals
	return &contractsrules.HeaderCondition{
		Type:          "header",
		KeyOperator:   contractsrules.StringEquals,
		KeyPattern:    name,
		ValueOperator: &valueOp,
		ValuePattern:  value,
	}
}

func changeProvider(p string) contractsrules.Action {
	return &contractsrules.ChangeProviderAction{Type: "changeProvider", NewProvider: p}
}

func changeModelName(m string) contractsrules.Action {
	return &contractsrules.ChangeModelNameAction{Type: "changeModelName", NewModelName: m}
}

// TestCascade_ProviderMutationVisibleToLaterRule: rule A flips
// state.Provider; rule B's ProviderCondition matches the new value.
func TestCascade_ProviderMutationVisibleToLaterRule(t *testing.T) {
	t.Parallel()
	flip := &contractsrules.RuleContract{
		Name:      "flip-to-anthropic",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{changeProvider("anthropic")},
	}
	tag := &contractsrules.RuleContract{
		Name:      "tag-anthropic",
		Condition: providerCondition("anthropic"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Slipspace-PostFlip", "yes")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {flip, tag}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())

	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if state.Provider != "anthropic" {
		t.Fatalf("state.Provider = %q, want anthropic", state.Provider)
	}
	if got := state.OutgoingHeaders.Get("X-Slipspace-PostFlip"); got != "yes" {
		t.Errorf("X-Slipspace-PostFlip = %q, want yes — tag rule did NOT see the cascaded provider", got)
	}
	records := buf.Drain()
	if len(records) != 2 {
		t.Fatalf("want 2 matches (flip + tag), got %d: %+v", len(records), records)
	}
}

// TestCascade_ModelMutationVisibleToLaterRule: rule A rewrites the
// typed body's Model field; rule B's ModelNameCondition matches the
// new model. This is the catch-all-on-normalised-model pattern.
func TestCascade_ModelMutationVisibleToLaterRule(t *testing.T) {
	t.Parallel()
	normalise := &contractsrules.RuleContract{
		Name:      "normalise-to-cheap",
		Condition: modelCondition(contractsrules.StringStartsWith, "gpt-"),
		Actions:   []contractsrules.Action{changeModelName("tier-cheap")},
	}
	catchall := &contractsrules.RuleContract{
		Name:      "catchall-on-cheap",
		Condition: modelCondition(contractsrules.StringEquals, "tier-cheap"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Cost-Tier", "low")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {normalise, catchall}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	body := &openaichat.ChatCompletionRequest{Model: "gpt-4o-mini"}
	ctx, buf := rules.WithMatchBuffer(context.Background())

	if _, err := e.Evaluate(ctx, newGC(), state, body); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if body.Model != "tier-cheap" {
		t.Fatalf("body.Model = %q, want tier-cheap", body.Model)
	}
	if got := state.OutgoingHeaders.Get("X-Cost-Tier"); got != "low" {
		t.Errorf("X-Cost-Tier = %q, want low — catchall didn't see the cascaded model", got)
	}
	if got := len(buf.Drain()); got != 2 {
		t.Errorf("match count = %d, want 2 (normalise + catchall)", got)
	}
}

// TestCascade_ModelMutationVisibleToCatchAll_MultipleSources: three
// reshaping rules all converge on the same normalised model, the
// single catch-all fires regardless of which reshaping rule got
// there. The canonical policy-normalisation pattern.
func TestCascade_ModelMutationVisibleToCatchAll_MultipleSources(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		initialModel string
	}{
		{"from-gpt", "gpt-4o-mini"},
		{"from-claude", "claude-haiku-4-5"},
		{"from-gemini", "gemini-2.0-flash-001"},
	}

	rule := func(name, prefix string) *contractsrules.RuleContract {
		return &contractsrules.RuleContract{
			Name:      name,
			Condition: modelCondition(contractsrules.StringStartsWith, prefix),
			Actions:   []contractsrules.Action{changeModelName("tier-cheap")},
		}
	}
	catchall := &contractsrules.RuleContract{
		Name:      "catchall-on-cheap",
		Condition: modelCondition(contractsrules.StringEquals, "tier-cheap"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Cost-Tier", "low")},
	}
	configRules := []*contractsrules.RuleContract{
		rule("normalise-gpt", "gpt-"),
		rule("normalise-claude", "claude-"),
		rule("normalise-gemini", "gemini-"),
		catchall,
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": configRules}), 8, nil)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
			body := &openaichat.ChatCompletionRequest{Model: tc.initialModel}
			ctx, _ := rules.WithMatchBuffer(context.Background())

			if _, err := e.Evaluate(ctx, newGC(), state, body); err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if got := state.OutgoingHeaders.Get("X-Cost-Tier"); got != "low" {
				t.Errorf("initial=%q: X-Cost-Tier = %q, want low", tc.initialModel, got)
			}
		})
	}
}

// TestCascade_HeaderMutationVisibleToLaterRule: SetHeader on rule A
// surfaces to rule B's HeaderCondition. This makes the marker-header
// pattern legitimate (rule A tags the request; rule B acts on the
// tag).
func TestCascade_HeaderMutationVisibleToLaterRule(t *testing.T) {
	t.Parallel()
	tagger := &contractsrules.RuleContract{
		Name:      "tag-tenant",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Tenant-Class", "premium")},
	}
	gate := &contractsrules.RuleContract{
		Name:      "premium-only-injection",
		Condition: headerCondition("X-Tenant-Class", "premium"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Premium-Feature", "enabled")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {tagger, gate}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, _ := rules.WithMatchBuffer(context.Background())

	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if got := state.OutgoingHeaders.Get("X-Tenant-Class"); got != "premium" {
		t.Errorf("X-Tenant-Class = %q, want premium", got)
	}
	if got := state.OutgoingHeaders.Get("X-Premium-Feature"); got != "enabled" {
		t.Errorf("X-Premium-Feature = %q, want enabled — gate did NOT see the cascaded header", got)
	}
}

// TestCascade_BehaviorExit_StopsAfterCascadeMutation: rule A
// cascades a provider change AND sets behavior: exit. Rule B sees
// nothing because the loop halts; rule B's matching against the
// cascaded provider doesn't get a chance to run.
func TestCascade_BehaviorExit_StopsAfterCascadeMutation(t *testing.T) {
	t.Parallel()
	flipAndExit := &contractsrules.RuleContract{
		Name:      "flip-and-exit",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{changeProvider("anthropic")},
		Behavior:  contractsrules.BehaviorExit,
	}
	wouldFire := &contractsrules.RuleContract{
		Name:      "would-fire-on-anthropic",
		Condition: providerCondition("anthropic"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Should-Not-Appear", "y")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {flipAndExit, wouldFire}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, _ := rules.WithMatchBuffer(context.Background())

	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if state.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", state.Provider)
	}
	if got := state.OutgoingHeaders.Get("X-Should-Not-Appear"); got != "" {
		t.Errorf("X-Should-Not-Appear = %q, want empty (Behavior=Exit must stop the loop)", got)
	}
}

// TestCascade_TerminatingAction_StopsImmediately: a terminating
// action short-circuits the entire pipeline; later rules don't run
// even if they would have matched against the cascaded state.
func TestCascade_TerminatingAction_StopsImmediately(t *testing.T) {
	t.Parallel()
	terminate := &contractsrules.RuleContract{
		Name:      "block-openai",
		Condition: providerCondition("openai"),
		Actions: []contractsrules.Action{
			&contractsrules.ReturnStatusCodeAction{
				Type: "returnStatusCode", StatusCode: 403, Body: `{"error":"blocked"}`, BodyType: contractsrules.StatusBodyJSON,
			},
		},
	}
	follower := &contractsrules.RuleContract{
		Name:      "would-tag",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Should-Not-Appear", "y")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {terminate, follower}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())

	res, err := e.Evaluate(ctx, newGC(), state, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !res.Outcome.Terminate {
		t.Fatalf("Outcome.Terminate should be true")
	}
	if got := state.OutgoingHeaders.Get("X-Should-Not-Appear"); got != "" {
		t.Errorf("follower rule fired despite earlier terminate; got header %q", got)
	}
	records := buf.Drain()
	if len(records) != 1 || records[0].RuleName != "block-openai" {
		t.Errorf("only the terminating rule should record a match, got %+v", records)
	}
}

// TestCascade_NoOscillation: even if two rules' actions could
// trivially undo each other, the single-pass guarantee bounds the
// loop. Each rule fires at most once per request; no infinite
// recursion.
func TestCascade_NoOscillation(t *testing.T) {
	t.Parallel()
	flipForward := &contractsrules.RuleContract{
		Name:      "flip-to-anthropic",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{changeProvider("anthropic")},
	}
	flipBack := &contractsrules.RuleContract{
		Name:      "flip-back-to-openai",
		Condition: providerCondition("anthropic"),
		Actions:   []contractsrules.Action{changeProvider("openai")},
	}
	wouldRefire := &contractsrules.RuleContract{
		Name:      "would-refire-if-we-looped",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Loop-Marker", "fired")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {flipForward, flipBack, wouldRefire}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())

	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if state.Provider != "openai" {
		t.Errorf("Provider = %q, want openai (flip-forward then flip-back)", state.Provider)
	}
	// The third rule fires because by its turn, the cascade has
	// landed back on openai. That's deliberate — single-pass +
	// live conditions means each rule evaluates against the
	// state-at-its-turn, then moves on. No oscillation, no loop.
	if got := state.OutgoingHeaders.Get("X-Loop-Marker"); got != "fired" {
		t.Errorf("third rule should have fired against the live (re-flipped) state; got %q", got)
	}
	if got := len(buf.Drain()); got != 3 {
		t.Errorf("expected 3 matches (one per rule, single-pass), got %d", got)
	}
}

// TestCascade_ConfigurationNameNotMutated: ConfigurationName is
// set by auth and must never change across rule evaluation, so
// telemetry consumers can trust it as a stable per-request key.
func TestCascade_ConfigurationNameNotMutated(t *testing.T) {
	t.Parallel()
	flip := &contractsrules.RuleContract{
		Name:      "flip-anywhere",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{changeProvider("anthropic")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {flip}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, buf := rules.WithMatchBuffer(context.Background())

	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	records := buf.Drain()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Configuration != "dev" {
		t.Errorf("Configuration = %q, want dev (must not mutate across cascade)", records[0].Configuration)
	}
}

// TestCascade_NoMutation_FrozenBehaviorIntact: if no rule mutates
// state, the cascaded view is identical to the initial gc — and
// downstream rules see exactly what the initial gc carried. This
// is the regression guard for the (much commoner) no-cascade case.
func TestCascade_NoMutation_FrozenBehaviorIntact(t *testing.T) {
	t.Parallel()
	first := &contractsrules.RuleContract{
		Name:      "tag-openai",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{setHeaderAction("X-First", "yes")},
	}
	second := &contractsrules.RuleContract{
		Name:      "tag-openai-again",
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
		t.Errorf("X-Second = %q (no-mutation case should behave identically to pre-cascade)", got)
	}
	if state.Provider != "openai" {
		t.Errorf("Provider mutated unexpectedly: %q", state.Provider)
	}
}

// TestCascade_LiveModelFromGeminiBodyPathParams: Gemini carries
// the model in PathParams, not the body. ChangeModelNameAction
// writes both; subsequent conditions read from PathParams (the
// liveModelName helper checks PathParams first per .NET parity).
func TestCascade_LiveModelFromGeminiBodyPathParams(t *testing.T) {
	t.Parallel()
	normalise := &contractsrules.RuleContract{
		Name:      "normalise-gemini",
		Condition: modelCondition(contractsrules.StringStartsWith, "gemini-"),
		Actions:   []contractsrules.Action{changeModelName("tier-multimodal")},
	}
	catchall := &contractsrules.RuleContract{
		Name:      "catchall-on-tier",
		Condition: modelCondition(contractsrules.StringEquals, "tier-multimodal"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Tier", "multimodal")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {normalise, catchall}}), 8, nil)

	// Seed the initial gc with the Gemini-style model and the
	// state's PathParams with the same — Gemini routes by URL
	// template, not body.
	state := rules.NewMutableState("gemini", "generate_content", "", map[string]string{"model": "gemini-2.0-flash-001"}, http.Header{})
	body := &content.GenerateContentRequest{}
	gc := rules.GatewayContext{
		Provider:          "gemini",
		Protocol:          "generate_content",
		Model:             "gemini-2.0-flash-001",
		ConfigurationName: "dev",
	}
	ctx, _ := rules.WithMatchBuffer(context.Background())

	if _, err := e.Evaluate(ctx, gc, state, body); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if state.PathParams["model"] != "tier-multimodal" {
		t.Errorf("PathParams[model] = %q, want tier-multimodal", state.PathParams["model"])
	}
	if got := state.OutgoingHeaders.Get("X-Tier"); got != "multimodal" {
		t.Errorf("X-Tier = %q, want multimodal — catchall didn't see the cascaded path-param model", got)
	}
}

// TestCascade_LiveModelFromAnthropicBody: messages.MessagesRequest
// is the same shape pattern (Model string field). Cover it
// alongside openaichat to make sure the type switch hits.
func TestCascade_LiveModelFromAnthropicBody(t *testing.T) {
	t.Parallel()
	normalise := &contractsrules.RuleContract{
		Name:      "normalise-claude",
		Condition: modelCondition(contractsrules.StringStartsWith, "claude-"),
		Actions:   []contractsrules.Action{changeModelName("tier-cheap")},
	}
	catchall := &contractsrules.RuleContract{
		Name:      "catchall-on-cheap",
		Condition: modelCondition(contractsrules.StringEquals, "tier-cheap"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Cost-Tier", "low")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {normalise, catchall}}), 8, nil)
	state := rules.NewMutableState("anthropic", "messages", "", nil, http.Header{})
	body := &messages.MessagesRequest{Model: "claude-haiku-4-5"}
	gc := rules.GatewayContext{Provider: "anthropic", Protocol: "messages", Model: "claude-haiku-4-5", ConfigurationName: "dev"}
	ctx, _ := rules.WithMatchBuffer(context.Background())

	if _, err := e.Evaluate(ctx, gc, state, body); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if body.Model != "tier-cheap" {
		t.Errorf("body.Model = %q, want tier-cheap", body.Model)
	}
	if got := state.OutgoingHeaders.Get("X-Cost-Tier"); got != "low" {
		t.Errorf("X-Cost-Tier = %q, want low", got)
	}
}

// TestCascade_LiveModelFromOpenAIResponsesBody: responses API has
// the same Model string field. Tag this case so we don't lose it
// when the responses request type evolves.
func TestCascade_LiveModelFromOpenAIResponsesBody(t *testing.T) {
	t.Parallel()
	normalise := &contractsrules.RuleContract{
		Name:      "normalise-responses",
		Condition: modelCondition(contractsrules.StringStartsWith, "gpt-"),
		Actions:   []contractsrules.Action{changeModelName("tier-cheap")},
	}
	catchall := &contractsrules.RuleContract{
		Name:      "catchall-on-cheap",
		Condition: modelCondition(contractsrules.StringEquals, "tier-cheap"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Cost-Tier", "low")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {normalise, catchall}}), 8, nil)
	state := rules.NewMutableState("openai", "responses", "", nil, http.Header{})
	body := &openairesponses.ResponsesRequest{Model: "gpt-4o-mini"}
	gc := rules.GatewayContext{Provider: "openai", Protocol: "responses", Model: "gpt-4o-mini", ConfigurationName: "dev"}
	ctx, _ := rules.WithMatchBuffer(context.Background())

	if _, err := e.Evaluate(ctx, gc, state, body); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if body.Model != "tier-cheap" {
		t.Errorf("body.Model = %q, want tier-cheap", body.Model)
	}
	if got := state.OutgoingHeaders.Get("X-Cost-Tier"); got != "low" {
		t.Errorf("X-Cost-Tier = %q, want low", got)
	}
}

// TestCascade_ProtocolCondition_FrozenWhenNoActionMutatesProtocol:
// v1.0.2 actions don't mutate state.Protocol (per .NET parity —
// ChangeProvider doesn't remap protocol names). So an
// ProtocolCondition on a later rule sees the SAME protocol name
// the initial gc carried; this confirms the cascade isn't
// accidentally injecting drift where there shouldn't be any.
func TestCascade_ProtocolCondition_FrozenWhenNoActionMutatesProtocol(t *testing.T) {
	t.Parallel()
	flip := &contractsrules.RuleContract{
		Name:      "flip-provider",
		Condition: providerCondition("openai"),
		Actions:   []contractsrules.Action{changeProvider("anthropic")},
	}
	protocolGate := &contractsrules.RuleContract{
		Name:      "tag-chat-protocol",
		Condition: protocolCondition("chat_completions"),
		Actions:   []contractsrules.Action{setHeaderAction("X-Protocol-Tag", "matched")},
	}
	e := rules.NewEvaluator(testStore(map[string][]*contractsrules.RuleContract{"dev": {flip, protocolGate}}), 8, nil)
	state := rules.NewMutableState("openai", "chat_completions", "", nil, http.Header{})
	ctx, _ := rules.WithMatchBuffer(context.Background())

	if _, err := e.Evaluate(ctx, newGC(), state, nil); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if state.Provider != "anthropic" {
		t.Errorf("Provider should have flipped: %q", state.Provider)
	}
	if state.Protocol != "chat_completions" {
		t.Errorf("Protocol should NOT have mutated: %q", state.Protocol)
	}
	if got := state.OutgoingHeaders.Get("X-Protocol-Tag"); got != "matched" {
		t.Errorf("X-Protocol-Tag = %q, want matched — protocol cascade should produce the routed name", got)
	}
}
