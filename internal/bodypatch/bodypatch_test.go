package bodypatch

import (
	"encoding/json"
	"testing"

	"github.com/tidwall/gjson"

	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

func litValue(raw string) contractsrules.RewriteValue {
	return contractsrules.RewriteValue{Kind: contractsrules.RewriteValueLiteral, Literal: json.RawMessage(raw)}
}

func tmplValue(s string) contractsrules.RewriteValue {
	return contractsrules.RewriteValue{Kind: contractsrules.RewriteValueTemplate, Template: s}
}

func structValue(raw string) contractsrules.RewriteValue {
	return contractsrules.RewriteValue{Kind: contractsrules.RewriteValueStructured, Literal: json.RawMessage(raw)}
}

func TestApply_Set(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		op      Op
		want    string
		applied bool
		reason  string
	}{
		{
			name:    "set existing scalar",
			body:    `{"temperature":1}`,
			op:      Op{Kind: OpSet, Path: "temperature", Value: litValue("0"), ActionType: "rewriteField"},
			want:    `{"temperature":0}`,
			applied: true,
		},
		{
			name:    "create nested intermediates",
			body:    `{"model":"x"}`,
			op:      Op{Kind: OpSet, Path: "stream_options.include_usage", Value: litValue("true"), ActionType: "rewriteField"},
			want:    `{"model":"x","stream_options":{"include_usage":true}}`,
			applied: true,
		},
		{
			name:    "set null emits key with null",
			body:    `{"tools":[1]}`,
			op:      Op{Kind: OpSet, Path: "tools", Value: litValue("null"), ActionType: "rewriteField"},
			want:    `{"tools":null}`,
			applied: true,
		},
		{
			name:    "structured literal array",
			body:    `{}`,
			op:      Op{Kind: OpSet, Path: "system", Value: structValue(`[{"type":"text","text":"hi"}]`), ActionType: "rewriteField"},
			want:    `{"system":[{"type":"text","text":"hi"}]}`,
			applied: true,
		},
		{
			name:    "empty literal becomes null",
			body:    `{}`,
			op:      Op{Kind: OpSet, Path: "x", Value: contractsrules.RewriteValue{Kind: contractsrules.RewriteValueLiteral}, ActionType: "rewriteField"},
			want:    `{"x":null}`,
			applied: true,
		},
		{
			name:    "scalar collision drops",
			body:    `{"model":"gpt"}`,
			op:      Op{Kind: OpSet, Path: "model.foo", Value: litValue("1"), ActionType: "rewriteField"},
			want:    `{"model":"gpt"}`,
			applied: false,
			reason:  ReasonPathTraversesPrimitive,
		},
		{
			name:    "empty path is an apply error",
			body:    `{}`,
			op:      Op{Kind: OpSet, Path: "", Value: litValue("1"), ActionType: "rewriteField"},
			want:    `{}`,
			applied: false,
			reason:  ReasonApplyError,
		},
		{
			name:    "single-ref miss drops",
			body:    `{}`,
			op:      Op{Kind: OpSet, Path: "x", Value: tmplValue("{request.body.missing}"), ActionType: "rewriteField"},
			want:    `{}`,
			applied: false,
			reason:  ReasonTemplateRefMiss,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, results := Apply([]byte(tt.body), []Op{tt.op}, Refs{})
			if string(got) != tt.want {
				t.Errorf("body = %s, want %s", got, tt.want)
			}
			if len(results) != 1 {
				t.Fatalf("want 1 result, got %d", len(results))
			}
			if results[0].Applied != tt.applied {
				t.Errorf("applied = %v, want %v", results[0].Applied, tt.applied)
			}
			if results[0].Reason != tt.reason {
				t.Errorf("reason = %q, want %q", results[0].Reason, tt.reason)
			}
		})
	}
}

func TestApply_Remove(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		op      Op
		want    string
		applied bool
		reason  string
	}{
		{
			name:    "remove present key",
			body:    `{"a":1,"user":"x"}`,
			op:      Op{Kind: OpRemove, Path: "user", ActionType: "removeField"},
			want:    `{"a":1}`,
			applied: true,
		},
		{
			name:    "remove missing key is a no-op success",
			body:    `{"a":1}`,
			op:      Op{Kind: OpRemove, Path: "user", ActionType: "removeField"},
			want:    `{"a":1}`,
			applied: true,
		},
		{
			name:    "remove empty path errors",
			body:    `{"a":1}`,
			op:      Op{Kind: OpRemove, Path: "", ActionType: "removeField"},
			want:    `{"a":1}`,
			applied: false,
			reason:  ReasonApplyError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, results := Apply([]byte(tt.body), []Op{tt.op}, Refs{})
			if string(got) != tt.want {
				t.Errorf("body = %s, want %s", got, tt.want)
			}
			if results[0].Applied != tt.applied || results[0].Reason != tt.reason {
				t.Errorf("result = %+v, want applied=%v reason=%q", results[0], tt.applied, tt.reason)
			}
		})
	}
}

func TestApply_Append(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		op      Op
		want    string
		applied bool
		reason  string
	}{
		{
			name:    "append to existing array",
			body:    `{"tools":[{"name":"a"}]}`,
			op:      Op{Kind: OpAppend, Path: "tools", Value: structValue(`{"name":"b"}`), ActionType: "appendField"},
			want:    `{"tools":[{"name":"a"},{"name":"b"}]}`,
			applied: true,
		},
		{
			name:    "append creates absent array",
			body:    `{"model":"x"}`,
			op:      Op{Kind: OpAppend, Path: "tools", Value: structValue(`{"name":"b"}`), ActionType: "appendField"},
			want:    `{"model":"x","tools":[{"name":"b"}]}`,
			applied: true,
		},
		{
			name:    "append to non-array string drops",
			body:    `{"system":"hi"}`,
			op:      Op{Kind: OpAppend, Path: "system", Value: structValue(`{"type":"text"}`), ActionType: "appendField"},
			want:    `{"system":"hi"}`,
			applied: false,
			reason:  ReasonAppendNonArray,
		},
		{
			name:    "append single-ref miss drops",
			body:    `{"tools":[]}`,
			op:      Op{Kind: OpAppend, Path: "tools", Value: tmplValue("{request.body.nope}"), ActionType: "appendField"},
			want:    `{"tools":[]}`,
			applied: false,
			reason:  ReasonTemplateRefMiss,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, results := Apply([]byte(tt.body), []Op{tt.op}, Refs{})
			if string(got) != tt.want {
				t.Errorf("body = %s, want %s", got, tt.want)
			}
			if results[0].Applied != tt.applied || results[0].Reason != tt.reason {
				t.Errorf("result = %+v, want applied=%v reason=%q", results[0], tt.applied, tt.reason)
			}
		})
	}
}

func TestApply_EmptyOps(t *testing.T) {
	body := []byte(`{"a":1}`)
	got, results := Apply(body, nil, Refs{})
	if string(got) != `{"a":1}` || results != nil {
		t.Errorf("expected passthrough, got body=%s results=%v", got, results)
	}
}

func TestApply_SequentialOpsObserveEarlier(t *testing.T) {
	ops := []Op{
		{Kind: OpSet, Path: "a", Value: litValue("5"), ActionType: "rewriteField"},
		{Kind: OpSet, Path: "b", Value: tmplValue("{request.body.a}"), ActionType: "rewriteField"},
	}
	got, _ := Apply([]byte(`{}`), ops, Refs{})
	if gjson.GetBytes(got, "b").Int() != 5 {
		t.Errorf("later op did not observe earlier mutation: %s", got)
	}
}

func TestResolveTemplate(t *testing.T) {
	body := `{"max_tokens":1024,"stream":true,"name":"abc","tools":null}`
	tests := []struct {
		name string
		tmpl string
		refs Refs
		want string
		drop bool
	}{
		{name: "single ref number passthrough", tmpl: "{request.body.max_tokens}", want: "1024"},
		{name: "single ref bool passthrough", tmpl: "{request.body.stream}", want: "true"},
		{name: "single ref null passthrough", tmpl: "{request.body.tools}", want: "null"},
		{name: "single ref string passthrough", tmpl: "{request.body.name}", want: `"abc"`},
		{name: "single ref miss drops", tmpl: "{request.body.gone}", drop: true},
		{name: "plain literal string", tmpl: "hello", want: `"hello"`},
		{name: "mixed content stringifies", tmpl: "max-{request.body.max_tokens}", want: `"max-1024"`},
		{name: "mixed content miss substitutes empty", tmpl: "x-{request.body.gone}-y", want: `"x--y"`},
		{name: "path param ref", tmpl: "{path_params.model}", refs: Refs{PathParams: map[string]string{"model": "gemini"}}, want: `"gemini"`},
		{name: "state provider ref", tmpl: "{state.provider}", refs: Refs{Provider: "openai"}, want: `"openai"`},
		{name: "state endpoint ref", tmpl: "{state.endpoint}", refs: Refs{Endpoint: "chat"}, want: `"chat"`},
		{name: "state provider miss drops", tmpl: "{state.provider}", drop: true},
		{name: "state endpoint miss drops", tmpl: "{state.endpoint}", drop: true},
		{name: "path param miss drops", tmpl: "{path_params.gone}", drop: true},
		{name: "unknown ref namespace drops", tmpl: "{external_url}", drop: true},
		{name: "mixed path params", tmpl: "{path_params.model}-v2", refs: Refs{PathParams: map[string]string{"model": "g"}}, want: `"g-v2"`},
		{name: "mixed state provider", tmpl: "p-{state.provider}", refs: Refs{Provider: "openai"}, want: `"p-openai"`},
		{name: "mixed state endpoint", tmpl: "e-{state.endpoint}", refs: Refs{Endpoint: "chat"}, want: `"e-chat"`},
		{name: "mixed unknown ref empty", tmpl: "u-{external_url}-x", want: `"u--x"`},
		{name: "mixed state provider miss empty", tmpl: "p-{state.provider}", want: `"p-"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, drop := resolveTemplate(body, tt.tmpl, tt.refs)
			if drop != tt.drop {
				t.Fatalf("drop = %v, want %v", drop, tt.drop)
			}
			if !drop && raw != tt.want {
				t.Errorf("raw = %s, want %s", raw, tt.want)
			}
		})
	}
}

func TestResolveValue_UnknownKindDrops(t *testing.T) {
	_, drop := resolveValue(`{}`, contractsrules.RewriteValue{Kind: 99}, Refs{})
	if !drop {
		t.Error("expected drop for unknown kind")
	}
}

func TestResolveTemplate_ResponsePhase(t *testing.T) {
	respBody := `{"id":"msg_batch_123","type":"message_batch"}`
	reqBody := []byte(`{"model":"claude-3-5-sonnet","max_tokens":100}`)
	refs := Refs{Phase: PhaseResponse, ExternalURL: "https://sluice.example.com", RequestBody: reqBody}
	tests := []struct {
		name string
		tmpl string
		want string
		drop bool
	}{
		{name: "response.body single-ref passthrough", tmpl: "{response.body.id}", want: `"msg_batch_123"`},
		{name: "external_url single-ref", tmpl: "{external_url}", want: `"https://sluice.example.com"`},
		{name: "request.body reads request snapshot", tmpl: "{request.body.model}", want: `"claude-3-5-sonnet"`},
		{name: "request.body number from snapshot", tmpl: "{request.body.max_tokens}", want: "100"},
		{
			name: "batches rebase mixed template",
			tmpl: "{external_url}/anthropic/v1/messages/batches/{response.body.id}/results",
			want: `"https://sluice.example.com/anthropic/v1/messages/batches/msg_batch_123/results"`,
		},
		{name: "response.body miss drops", tmpl: "{response.body.absent}", drop: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, drop := resolveTemplate(respBody, tt.tmpl, refs)
			if drop != tt.drop {
				t.Fatalf("drop = %v, want %v", drop, tt.drop)
			}
			if !drop && raw != tt.want {
				t.Errorf("raw = %s, want %s", raw, tt.want)
			}
		})
	}
}

func TestResolveTemplate_RequestPhase_ResponseRefUnavailable(t *testing.T) {
	if _, drop := resolveTemplate(`{"id":"req"}`, "{response.body.id}", Refs{Phase: PhaseRequest}); !drop {
		t.Error("response.body ref should miss in request phase")
	}
	if _, drop := resolveTemplate(`{}`, "{external_url}", Refs{Phase: PhaseRequest}); !drop {
		t.Error("external_url should miss when unset")
	}
	raw, drop := resolveTemplate(`{}`, "x-{external_url}", Refs{Phase: PhaseRequest})
	if drop || raw != `"x-"` {
		t.Errorf("got %q drop=%v, want \"x-\"", raw, drop)
	}
}

func TestApply_ResponsePhase_RebaseField(t *testing.T) {
	resp := []byte(`{"id":"b1","results_url":"https://api.anthropic.com/v1/messages/batches/b1/results"}`)
	op := Op{
		Kind:       OpSet,
		Path:       "results_url",
		Value:      tmplValue("{external_url}/anthropic/v1/messages/batches/{response.body.id}/results"),
		ActionType: "rewriteField",
	}
	got, results := Apply(resp, []Op{op}, Refs{Phase: PhaseResponse, ExternalURL: "https://sluice.example.com"})
	if !results[0].Applied {
		t.Fatalf("not applied: %+v", results[0])
	}
	want := "https://sluice.example.com/anthropic/v1/messages/batches/b1/results"
	if got := gjson.GetBytes(got, "results_url").String(); got != want {
		t.Errorf("results_url = %s, want %s", got, want)
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		in   string
		want []token
	}{
		{"", nil},
		{"abc", []token{{text: "abc"}}},
		{"{a}", []token{{isRef: true, text: "a"}}},
		{"x{a}y", []token{{text: "x"}, {isRef: true, text: "a"}, {text: "y"}}},
		{"{a}{b}", []token{{isRef: true, text: "a"}, {isRef: true, text: "b"}}},
		{"x{unbalanced", []token{{text: "x"}, {text: "{unbalanced"}}},
		{"{a}tail", []token{{isRef: true, text: "a"}, {text: "tail"}}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := tokenize(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d (%v), want %d (%v)", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("token[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestTraversesPrimitive(t *testing.T) {
	tests := []struct {
		name string
		body string
		path string
		want bool
	}{
		{"single segment never traverses", `{"a":"x"}`, "a", false},
		{"scalar intermediate", `{"model":"gpt"}`, "model.foo", true},
		{"object intermediate ok", `{"a":{"b":1}}`, "a.newfield", false},
		{"deep scalar intermediate", `{"a":{"b":1}}`, "a.b.c", true},
		{"array intermediate ok", `{"a":[1]}`, "a.b", false},
		{"absent intermediate ok", `{}`, "a.b.c", false},
		{"null intermediate is primitive", `{"a":null}`, "a.b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := traversesPrimitive(tt.body, tt.path); got != tt.want {
				t.Errorf("traversesPrimitive = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApply_AppendApplyError(t *testing.T) {
	// A path containing an unterminated gjson modifier makes sjson's
	// SetRaw reject it, exercising the append apply-error branch. The
	// non-array guard passes first because gjson cannot resolve the
	// path either (it does not exist), so the append is attempted.
	op := Op{Kind: OpAppend, Path: `a\`, Value: litValue("1"), ActionType: "appendField"}
	_, results := Apply([]byte(`{}`), []Op{op}, Refs{})
	// Whatever sjson decides, the op must not panic and the result is
	// recorded; this primarily exists to drive the append code path.
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
}

func FuzzApply(f *testing.F) {
	f.Add(`{"a":1}`, "a.b.c")
	f.Add(`{"model":"x"}`, "stream_options.include_usage")
	f.Add(`[1,2,3]`, "x")
	f.Add(`{}`, "")
	f.Add(`not json`, "a")
	f.Fuzz(func(t *testing.T, body, path string) {
		ops := []Op{
			{Kind: OpSet, Path: path, Value: litValue("true"), ActionType: "rewriteField"},
			{Kind: OpRemove, Path: path, ActionType: "removeField"},
			{Kind: OpAppend, Path: path, Value: litValue("1"), ActionType: "appendField"},
		}
		// Property: Apply never panics and never returns a fatal error
		// for any input — all failures are per-op drops.
		got, _ := Apply([]byte(body), ops, Refs{})
		_ = got
	})
}
