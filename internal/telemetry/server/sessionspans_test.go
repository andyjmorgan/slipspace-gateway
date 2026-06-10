package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	adminc "github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

// spanBlob marshals a span_event blob from a key->value map, failing the test
// on a marshal error so cases stay one-liners.
func spanBlob(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal blob: %v", err)
	}
	return b
}

func i64(v int64) *int64 { return &v }
func iptr(v int) *int    { return &v }
func sptr(v string) *string {
	return &v
}

func TestSessionSpanFromEvent(t *testing.T) {
	at := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		event store.RequestEvent
		want  adminc.SessionSpan
	}{
		{
			name: "full span with client tool call and usage",
			event: store.RequestEvent{
				CorrelationID:        "cid-1",
				ObservedAt:           at,
				SessionID:            "sess-1",
				ConversationID:       "agent-7",
				ParentConversationID: "sess-1",
				Model:                "claude-opus-4-8",
				StatusCode:           200,
				SpanEvent: spanBlob(t, map[string]any{
					"sluice.latency_ms":                    1234,
					"sluice.upstream_status":               200,
					"gen_ai.response.time_to_first_chunk":  0.5,
					"gen_ai.response.finish_reasons":       []string{"tool_use", "end_turn"},
					"gen_ai.usage.input_tokens":            100,
					"gen_ai.usage.output_tokens":           20,
					"gen_ai.usage.cache_read.input_tokens": 64,
					"gen_ai_content": map[string]any{
						"input_messages": []any{map[string]any{
							"role": "user",
							"parts": []any{
								map[string]any{"type": "text", "content": "run the tests"},
							},
						}},
						"output_messages": []any{map[string]any{
							"role": "assistant",
							"parts": []any{
								map[string]any{"type": "text", "content": "on it"},
								map[string]any{"type": "tool_call", "id": "toolu_01", "name": "Bash", "arguments": map[string]any{"command": "make e2e"}},
							},
						}},
					},
				}),
			},
			want: adminc.SessionSpan{
				CID:                  "cid-1",
				At:                   at,
				LatencyMs:            i64(1234),
				TTFCMs:               i64(500),
				Status:               iptr(200),
				Model:                sptr("claude-opus-4-8"),
				FinishReason:         sptr("tool_use"),
				SessionID:            "sess-1",
				ConversationID:       "agent-7",
				ParentConversationID: sptr("sess-1"),
				Usage: adminc.SessionSpanUsage{
					Input: i64(100), Output: i64(20), CacheRead: i64(64),
				},
				OutputParts: []adminc.SessionSpanOutputPart{
					{Type: "text", Chars: iptr(5)},
					{Type: "tool_call", ID: "toolu_01", Name: "Bash", Args: `{"command":"make e2e"}`, ArgsChars: iptr(22)},
				},
				InputParts: []adminc.SessionSpanInputPart{
					{Type: "text", Chars: iptr(13), Text: "run the tests"},
				},
				InputText:       sptr("run the tests"),
				InputTextChars:  iptr(13),
				OutputText:      sptr("on it"),
				OutputTextChars: iptr(5),
			},
		},
		{
			name: "tool continuation joins a prior span's call id",
			event: store.RequestEvent{
				CorrelationID: "cid-2",
				ObservedAt:    at,
				SessionID:     "sess-1",
				StatusCode:    200,
				SpanEvent: spanBlob(t, map[string]any{
					"sluice.latency_ms": 50,
					"gen_ai_content": map[string]any{
						"input_messages": []any{map[string]any{
							"role": "user",
							"parts": []any{
								map[string]any{"type": "tool_call_response", "id": "toolu_01", "result": "ok: 12 passed"},
							},
						}},
					},
				}),
			},
			want: adminc.SessionSpan{
				CID:       "cid-2",
				At:        at,
				LatencyMs: i64(50),
				Status:    iptr(200),
				SessionID: "sess-1",
				// conversation falls back to the session for the main loop.
				ConversationID: "sess-1",
				OutputParts:    []adminc.SessionSpanOutputPart{},
				InputParts: []adminc.SessionSpanInputPart{
					{Type: "tool_call_response", ID: "toolu_01", Chars: iptr(13), Text: "ok: 12 passed"},
				},
			},
		},
		{
			name: "server-executed tool: call and response on the same span",
			event: store.RequestEvent{
				CorrelationID:  "cid-3",
				ObservedAt:     at,
				SessionID:      "sess-1",
				ConversationID: "sess-1",
				StatusCode:     200,
				SpanEvent: spanBlob(t, map[string]any{
					"gen_ai.usage.server_tool_use.web_search_requests": 2,
					"gen_ai_content": map[string]any{
						"output_messages": []any{map[string]any{
							"role": "assistant",
							"parts": []any{
								map[string]any{"type": "tool_call", "id": "srvtoolu_01", "name": "web_search", "arguments": map[string]any{"query": "go 1.26"}},
								map[string]any{"type": "tool_call_response", "id": "srvtoolu_01", "result": "10 results"},
								map[string]any{"type": "text", "content": "found it"},
							},
						}},
					},
				}),
			},
			want: adminc.SessionSpan{
				CID:            "cid-3",
				At:             at,
				Status:         iptr(200),
				SessionID:      "sess-1",
				ConversationID: "sess-1",
				Usage: adminc.SessionSpanUsage{
					ServerToolUse: map[string]int64{"web_search_requests": 2},
				},
				OutputParts: []adminc.SessionSpanOutputPart{
					{Type: "tool_call", ID: "srvtoolu_01", Name: "web_search", Args: `{"query":"go 1.26"}`, ArgsChars: iptr(19)},
					{Type: "tool_call_response", ID: "srvtoolu_01", Chars: iptr(10), Text: "10 results"},
					{Type: "text", Chars: iptr(8)},
				},
				InputParts:      []adminc.SessionSpanInputPart{},
				OutputText:      sptr("found it"),
				OutputTextChars: iptr(8),
			},
		},
		{
			name: "reasoning parts counted, excluded from output_text; unknown passthrough",
			event: store.RequestEvent{
				CorrelationID:  "cid-4",
				ObservedAt:     at,
				SessionID:      "sess-1",
				ConversationID: "sess-1",
				SpanEvent: spanBlob(t, map[string]any{
					"gen_ai_content": map[string]any{
						"output_messages": []any{map[string]any{
							"role": "assistant",
							"parts": []any{
								map[string]any{"type": "reasoning", "content": "think hard"},
								map[string]any{"type": "image"},
								map[string]any{"type": "text", "content": "héllo"},
							},
						}},
						// Media-only input parts are outside the input enum -> dropped.
						"input_messages": []any{map[string]any{
							"role":  "user",
							"parts": []any{map[string]any{"type": "image"}},
						}},
					},
				}),
			},
			want: adminc.SessionSpan{
				CID:            "cid-4",
				At:             at,
				SessionID:      "sess-1",
				ConversationID: "sess-1",
				OutputParts: []adminc.SessionSpanOutputPart{
					{Type: "reasoning", Chars: iptr(10)},
					{Type: "unknown"},
					// chars counts code points, not bytes (é is 2 bytes, 1 char).
					{Type: "text", Chars: iptr(5)},
				},
				InputParts:      []adminc.SessionSpanInputPart{},
				OutputText:      sptr("héllo"),
				OutputTextChars: iptr(5),
			},
		},
		{
			name: "missing blob degrades to nulls and empty envelopes",
			event: store.RequestEvent{
				CorrelationID: "cid-5",
				ObservedAt:    at,
				SessionID:     "sess-1",
			},
			want: adminc.SessionSpan{
				CID:            "cid-5",
				At:             at,
				SessionID:      "sess-1",
				ConversationID: "sess-1",
				OutputParts:    []adminc.SessionSpanOutputPart{},
				InputParts:     []adminc.SessionSpanInputPart{},
			},
		},
		{
			name: "lenient numerics: string-encoded counts, single-string finish reason",
			event: store.RequestEvent{
				CorrelationID:  "cid-6",
				ObservedAt:     at,
				SessionID:      "sess-1",
				ConversationID: "sess-1",
				SpanEvent: spanBlob(t, map[string]any{
					"sluice.latency_ms":              "750",
					"gen_ai.usage.input_tokens":      "42",
					"gen_ai.response.finish_reasons": "end_turn",
					"sluice.upstream_status":         429.0,
				}),
			},
			want: adminc.SessionSpan{
				CID:            "cid-6",
				At:             at,
				LatencyMs:      i64(750),
				Status:         iptr(429),
				FinishReason:   sptr("end_turn"),
				SessionID:      "sess-1",
				ConversationID: "sess-1",
				Usage:          adminc.SessionSpanUsage{Input: i64(42)},
				OutputParts:    []adminc.SessionSpanOutputPart{},
				InputParts:     []adminc.SessionSpanInputPart{},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionSpanFromEvent(tc.event)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("span mismatch:\n got %s\nwant %s", gotJSON, wantJSON)
			}
		})
	}
}

// TestSessionSpans_FullFidelity pins the no-truncation decision: a large tool
// argument payload and result text are served whole and every *_chars equals
// the served length.
func TestSessionSpans_FullFidelity(t *testing.T) {
	big := make([]byte, 0, 200_000)
	for len(big) < 200_000 {
		big = append(big, 'x')
	}
	args := `{"data":"` + string(big) + `"}`
	event := store.RequestEvent{
		CorrelationID: "cid-big", SessionID: "s", ConversationID: "s",
		SpanEvent: spanBlob(t, map[string]any{
			"gen_ai_content": map[string]any{
				"output_messages": []any{map[string]any{
					"role": "assistant",
					"parts": []any{
						map[string]any{"type": "tool_call", "id": "t1", "name": "Write", "arguments": json.RawMessage(args)},
					},
				}},
				"input_messages": []any{map[string]any{
					"role": "user",
					"parts": []any{
						map[string]any{"type": "tool_call_response", "id": "t0", "result": string(big)},
					},
				}},
			},
		}),
	}
	got := sessionSpanFromEvent(event)
	if len(got.OutputParts) != 1 || got.OutputParts[0].Args != args {
		t.Fatalf("tool args were not served whole (len %d, want %d)", len(got.OutputParts[0].Args), len(args))
	}
	if *got.OutputParts[0].ArgsChars != len(args) {
		t.Errorf("args_chars = %d, want %d", *got.OutputParts[0].ArgsChars, len(args))
	}
	if len(got.InputParts) != 1 || got.InputParts[0].Text != string(big) {
		t.Fatalf("tool result was not served whole")
	}
	if *got.InputParts[0].Chars != len(big) {
		t.Errorf("chars = %d, want %d", *got.InputParts[0].Chars, len(big))
	}
}

func TestSessionSpansHandler(t *testing.T) {
	at := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	q := &fakeQueries{session: []store.RequestEvent{
		{CorrelationID: "c1", ObservedAt: at, SessionID: "sess-1", ConversationID: "sess-1"},
		{CorrelationID: "c2", ObservedAt: at.Add(time.Second), SessionID: "sess-1", ConversationID: "sess-1"},
	}}
	h := newQueryServer(t, q)

	resp := get(t, h, "/api/v1/sessions/sess-1/spans", true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	var spans []adminc.SessionSpan
	if err := json.Unmarshal(resp.Body.Bytes(), &spans); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(spans) != 2 || spans[0].CID != "c1" || spans[1].CID != "c2" {
		t.Fatalf("spans = %+v", spans)
	}
	if !spans[1].At.After(spans[0].At) {
		t.Errorf("spans not ordered by at: %v then %v", spans[0].At, spans[1].At)
	}
}

func TestSessionSpansHandler_Errors(t *testing.T) {
	t.Run("auth required", func(t *testing.T) {
		h := newQueryServer(t, &fakeQueries{})
		if resp := get(t, h, "/api/v1/sessions/x/spans", false); resp.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.Code)
		}
	})
	t.Run("unknown session is 404", func(t *testing.T) {
		h := newQueryServer(t, &fakeQueries{})
		if resp := get(t, h, "/api/v1/sessions/nope/spans", true); resp.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.Code)
		}
	})
	t.Run("query failure is 500", func(t *testing.T) {
		h := newQueryServer(t, &fakeQueries{sessionErr: errors.New("db")})
		if resp := get(t, h, "/api/v1/sessions/x/spans", true); resp.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.Code)
		}
	})
}
