package rules

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
	"github.com/andyjmorgan/sluice-gateway/internal/bodypatch"
	"github.com/andyjmorgan/sluice-gateway/internal/observability"
)

func testMeters(t *testing.T) *observability.Meters {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	m, err := observability.NewMeters(mp.Meter(observability.MeterName))
	if err != nil {
		t.Fatalf("NewMeters: %v", err)
	}
	return m
}

func lit(raw string) contractsrules.RewriteValue {
	return contractsrules.RewriteValue{Kind: contractsrules.RewriteValueLiteral, Literal: []byte(raw)}
}

func TestApplyRewriteActions_RecordOps(t *testing.T) {
	tests := []struct {
		name     string
		action   contractsrules.Action
		wantKind bodypatch.OpKind
		wantPath string
		wantErr  error
	}{
		{
			name:     "rewriteField records set",
			action:   &contractsrules.RewriteFieldAction{Type: "rewriteField", Target: "request.body.temperature", Value: lit("0")},
			wantKind: bodypatch.OpSet,
			wantPath: "temperature",
		},
		{
			name:     "removeField records remove",
			action:   &contractsrules.RemoveFieldAction{Type: "removeField", Target: "request.body.user"},
			wantKind: bodypatch.OpRemove,
			wantPath: "user",
		},
		{
			name:     "appendField records append",
			action:   &contractsrules.AppendFieldAction{Type: "appendField", Target: "request.body.messages", Value: lit(`{"role":"system"}`)},
			wantKind: bodypatch.OpAppend,
			wantPath: "messages",
		},
		{
			name:    "rewriteField response scope errors",
			action:  &contractsrules.RewriteFieldAction{Type: "rewriteField", Target: "response.body.x", Value: lit("1")},
			wantErr: contractsrules.ErrResponseScopeUnsupported,
		},
		{
			name:    "removeField bad target errors",
			action:  &contractsrules.RemoveFieldAction{Type: "removeField", Target: "nope"},
			wantErr: contractsrules.ErrInvalidTarget,
		},
		{
			name:    "appendField bad target errors",
			action:  &contractsrules.AppendFieldAction{Type: "appendField", Target: "request.body.a[0]", Value: lit("1")},
			wantErr: contractsrules.ErrInvalidTarget,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &MutableState{}
			_, err := applyAction(tt.action, state, nil)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if len(state.BodyRewrites) != 0 {
					t.Errorf("error case still recorded an op: %+v", state.BodyRewrites)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(state.BodyRewrites) != 1 {
				t.Fatalf("want 1 op, got %d", len(state.BodyRewrites))
			}
			op := state.BodyRewrites[0]
			if op.Kind != tt.wantKind || op.Path != tt.wantPath {
				t.Errorf("op = {%d %q}, want {%d %q}", op.Kind, op.Path, tt.wantKind, tt.wantPath)
			}
		})
	}
}

func TestMatchBodyField(t *testing.T) {
	body := []byte(`{"stream":true,"max_tokens":1024,"model":"gpt","system":[{"t":1}],"meta":{"k":1},"nilv":null,"tools":[{"name":"bash"}]}`)
	tests := []struct {
		name string
		cond contractsrules.BodyFieldCondition
		want bool
	}{
		{name: "equals bool true", cond: bf("request.body.stream", contractsrules.BodyFieldEquals, "true"), want: true},
		{name: "equals bool false", cond: bf("request.body.stream", contractsrules.BodyFieldEquals, "false"), want: false},
		{name: "equals number", cond: bf("request.body.max_tokens", contractsrules.BodyFieldEquals, "1024"), want: true},
		{name: "equals string", cond: bf("request.body.model", contractsrules.BodyFieldEquals, "gpt"), want: true},
		{name: "equals missing field", cond: bf("request.body.absent", contractsrules.BodyFieldEquals, "x"), want: false},
		{name: "contains", cond: bf("request.body.model", contractsrules.BodyFieldContains, "p"), want: true},
		{name: "contains miss", cond: bf("request.body.model", contractsrules.BodyFieldContains, "zzz"), want: false},
		{name: "matches regex", cond: bf("request.body.model", contractsrules.BodyFieldMatches, "^g.t$"), want: true},
		{name: "matches missing field", cond: bf("request.body.absent", contractsrules.BodyFieldMatches, ".*"), want: false},
		{name: "exists present", cond: bf("request.body.tools", contractsrules.BodyFieldExists, ""), want: true},
		{name: "exists absent", cond: bf("request.body.nope", contractsrules.BodyFieldExists, ""), want: false},
		{name: "is array", cond: bf("request.body.system", contractsrules.BodyFieldIsType, "array"), want: true},
		{name: "is object", cond: bf("request.body.meta", contractsrules.BodyFieldIsType, "object"), want: true},
		{name: "is number", cond: bf("request.body.max_tokens", contractsrules.BodyFieldIsType, "number"), want: true},
		{name: "is bool", cond: bf("request.body.stream", contractsrules.BodyFieldIsType, "bool"), want: true},
		{name: "is string", cond: bf("request.body.model", contractsrules.BodyFieldIsType, "string"), want: true},
		{name: "is null", cond: bf("request.body.nilv", contractsrules.BodyFieldIsType, "null"), want: true},
		{name: "is wrong type", cond: bf("request.body.model", contractsrules.BodyFieldIsType, "array"), want: false},
		{name: "is unknown type name", cond: bf("request.body.model", contractsrules.BodyFieldIsType, "weird"), want: false},
		{name: "predicate exists", cond: bf(`request.body.tools.#(name=="bash")`, contractsrules.BodyFieldExists, ""), want: true},
		{name: "bad target", cond: bf("stream", contractsrules.BodyFieldExists, ""), want: false},
		{name: "response scope target", cond: bf("response.body.x", contractsrules.BodyFieldExists, ""), want: false},
		{name: "unknown operator", cond: bf("request.body.model", "Nope", ""), want: false},
		{name: "bad regex", cond: bf("request.body.model", contractsrules.BodyFieldMatches, "("), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gc := GatewayContext{BodyRaw: body}
			if got := matchBodyField(tt.cond, gc); got != tt.want {
				t.Errorf("matchBodyField = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchBodyField_EmptyBody(t *testing.T) {
	if matchBodyField(bf("request.body.x", contractsrules.BodyFieldExists, ""), GatewayContext{}) {
		t.Error("expected false for empty body")
	}
}

func TestMatchCondition_BodyField_NotInversion(t *testing.T) {
	gc := GatewayContext{BodyRaw: []byte(`{"stream":true}`)}
	cond := &contractsrules.BodyFieldCondition{Type: "bodyField", Target: "request.body.stream", Operator: contractsrules.BodyFieldExists, Not: true}
	if matchCondition(cond, gc, 0, 8, nil) {
		t.Error("Not should invert a present field to false")
	}
}

func bf(target string, op contractsrules.BodyFieldOperator, value string) contractsrules.BodyFieldCondition {
	return contractsrules.BodyFieldCondition{Type: "bodyField", Target: target, Operator: op, Value: value}
}

func TestBodyRewriteHandler(t *testing.T) {
	tests := []struct {
		name      string
		state     *MutableState
		body      string
		wantBody  string
		wantNext  bool
		setNoBody bool
	}{
		{
			name:     "no state passthrough",
			state:    nil,
			body:     `{"a":1}`,
			wantBody: `{"a":1}`,
			wantNext: true,
		},
		{
			name:     "no rewrites passthrough",
			state:    &MutableState{},
			body:     `{"a":1}`,
			wantBody: `{"a":1}`,
			wantNext: true,
		},
		{
			name: "applies set",
			state: &MutableState{BodyRewrites: []bodypatch.Op{
				{Kind: bodypatch.OpSet, Path: "stream_options.include_usage", Value: lit("true"), ActionType: "rewriteField"},
			}},
			body:     `{"model":"x"}`,
			wantBody: `{"model":"x","stream_options":{"include_usage":true}}`,
			wantNext: true,
		},
		{
			name: "drop recorded but body otherwise unchanged",
			state: &MutableState{BodyRewrites: []bodypatch.Op{
				{Kind: bodypatch.OpSet, Path: "model.foo", Value: lit("1"), ActionType: "rewriteField"},
			}},
			body:     `{"model":"gpt"}`,
			wantBody: `{"model":"gpt"}`,
			wantNext: true,
		},
		{
			name: "nil body with rewrites",
			state: &MutableState{BodyRewrites: []bodypatch.Op{
				{Kind: bodypatch.OpSet, Path: "a", Value: lit("1"), ActionType: "rewriteField"},
			}},
			setNoBody: true,
			wantBody:  `{"a":1}`,
			wantNext:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody string
			var nextCalled bool
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				if r.Body != nil {
					b, _ := io.ReadAll(r.Body)
					gotBody = string(b)
				}
			})

			h := BodyRewriteHandler(testMeters(t), next)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if !tt.setNoBody {
				req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.NopCloser(stringReader(tt.body)))
			} else {
				req.Body = nil
			}
			if tt.state != nil {
				req = req.WithContext(WithMutableState(req.Context(), tt.state))
			}

			h.ServeHTTP(httptest.NewRecorder(), req)

			if nextCalled != tt.wantNext {
				t.Fatalf("nextCalled = %v, want %v", nextCalled, tt.wantNext)
			}
			if gotBody != tt.wantBody {
				t.Errorf("body = %q, want %q", gotBody, tt.wantBody)
			}
		})
	}
}

func TestBodyRewriteHandler_NilMeters(t *testing.T) {
	state := &MutableState{BodyRewrites: []bodypatch.Op{
		{Kind: bodypatch.OpSet, Path: "a", Value: lit("1"), ActionType: "rewriteField"},
	}}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	h := BodyRewriteHandler(nil, next)
	req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(stringReader(`{}`)))
	req = req.WithContext(WithMutableState(req.Context(), state))
	h.ServeHTTP(httptest.NewRecorder(), req) // must not panic
}

func TestBodyRewriteHandler_NilNextPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on nil next")
		}
	}()
	BodyRewriteHandler(nil, nil)
}

func TestBodyRewriteHandler_ReadError(t *testing.T) {
	state := &MutableState{BodyRewrites: []bodypatch.Op{
		{Kind: bodypatch.OpSet, Path: "a", Value: lit("1"), ActionType: "rewriteField"},
	}}
	nextCalled := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true })
	h := BodyRewriteHandler(testMeters(t), next)

	req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(errReader{}))
	req = req.WithContext(WithMutableState(req.Context(), state))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if nextCalled {
		t.Error("next should not run when the body read fails")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestReadRequestBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(stringReader(`{"a":1}`)))
	b, err := readRequestBody(req)
	if err != nil || string(b) != `{"a":1}` {
		t.Errorf("got %q err=%v", b, err)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	req2.Body = nil
	b2, err := readRequestBody(req2)
	if err != nil || len(b2) != 0 {
		t.Errorf("nil body: got %q err=%v", b2, err)
	}
}

func TestRecordRewriteResults(t *testing.T) {
	results := []bodypatch.Result{
		{ActionType: "rewriteField", Applied: true},
		{ActionType: "rewriteField", Applied: false, Reason: bodypatch.ReasonPathTraversesPrimitive},
	}
	// nil meters must be a no-op.
	recordRewriteResults(context.Background(), nil, results)
	// real meters must not panic.
	recordRewriteResults(context.Background(), testMeters(t), results)
}

func TestState_Clone_BodyRewrites(t *testing.T) {
	s := &MutableState{BodyRewrites: []bodypatch.Op{
		{Kind: bodypatch.OpSet, Path: "a", ActionType: "rewriteField"},
	}}
	c := s.Clone()
	if len(c.BodyRewrites) != 1 {
		t.Fatalf("clone lost rewrites: %+v", c.BodyRewrites)
	}
	c.BodyRewrites[0].Path = "b"
	if s.BodyRewrites[0].Path != "a" {
		t.Error("clone shares backing array with original")
	}
}

type stringReaderT struct {
	s string
	i int
}

func stringReader(s string) *stringReaderT { return &stringReaderT{s: s} }

func (r *stringReaderT) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}
