package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
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
			// fieldCap 0 = cap disabled, so the table pins the uncapped shapes.
			got := sessionSpanFromEvent(tc.event, 0)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("span mismatch:\n got %s\nwant %s", gotJSON, wantJSON)
			}
		})
	}
}

// bigSpanEvent builds an event whose blob carries a large tool argument
// payload, a large tool result, and a large text part (all built from `fill`).
func bigSpanEvent(t *testing.T, fill string) store.RequestEvent {
	t.Helper()
	args := `{"data":"` + fill + `"}`
	return store.RequestEvent{
		CorrelationID: "cid-big", SessionID: "s", ConversationID: "s",
		SpanEvent: spanBlob(t, map[string]any{
			"gen_ai_content": map[string]any{
				"output_messages": []any{map[string]any{
					"role": "assistant",
					"parts": []any{
						map[string]any{"type": "text", "content": fill},
						map[string]any{"type": "tool_call", "id": "t1", "name": "Write", "arguments": json.RawMessage(args)},
					},
				}},
				"input_messages": []any{map[string]any{
					"role": "user",
					"parts": []any{
						map[string]any{"type": "text", "content": fill},
						map[string]any{"type": "tool_call_response", "id": "t0", "result": fill},
					},
				}},
			},
		}),
	}
}

// TestSessionSpans_FieldCap pins the truncation policy that replaced v1's
// "full fidelity" (the OOM fix): every served content field is capped at
// fieldCap bytes while every *_chars field carries the TRUE uncapped size, so
// the renderer's "showing first N of M" notice fires exactly on truncation.
func TestSessionSpans_FieldCap(t *testing.T) {
	const capBytes = 1024
	big := strings.Repeat("x", 200_000)
	args := `{"data":"` + big + `"}`

	got := sessionSpanFromEvent(bigSpanEvent(t, big), capBytes)

	// tool_call args: capped, args_chars = true size.
	call := got.OutputParts[1]
	if len(call.Args) != capBytes {
		t.Errorf("args len = %d, want capped %d", len(call.Args), capBytes)
	}
	if *call.ArgsChars != len(args) {
		t.Errorf("args_chars = %d, want true %d", *call.ArgsChars, len(args))
	}
	// input text part + tool result: capped, chars = true size.
	for _, p := range got.InputParts {
		if len(p.Text) != capBytes {
			t.Errorf("input %s text len = %d, want capped %d", p.Type, len(p.Text), capBytes)
		}
		if *p.Chars != len(big) {
			t.Errorf("input %s chars = %d, want true %d", p.Type, *p.Chars, len(big))
		}
	}
	// concatenated input_text/output_text: capped, *_chars = true size.
	if len(*got.InputText) != capBytes || *got.InputTextChars != len(big) {
		t.Errorf("input_text len/chars = %d/%d, want %d/%d", len(*got.InputText), *got.InputTextChars, capBytes, len(big))
	}
	if len(*got.OutputText) != capBytes || *got.OutputTextChars != len(big) {
		t.Errorf("output_text len/chars = %d/%d, want %d/%d", len(*got.OutputText), *got.OutputTextChars, capBytes, len(big))
	}
	// output text part chars (size-only field) also carries the true size.
	if *got.OutputParts[0].Chars != len(big) {
		t.Errorf("text part chars = %d, want true %d", *got.OutputParts[0].Chars, len(big))
	}

	// fieldCap 0 disables the cap: everything served whole.
	whole := sessionSpanFromEvent(bigSpanEvent(t, big), 0)
	if whole.OutputParts[1].Args != args || *whole.InputText != big {
		t.Error("fieldCap 0 must serve fields whole")
	}
}

// TestCapText pins the rune-boundary cut: a cap landing mid-rune backs up so
// the capped string stays valid UTF-8.
func TestCapText(t *testing.T) {
	s := "aé" // 'a' (1 byte) + 'é' (2 bytes)
	if got := capText(s, 2); got != "a" {
		t.Errorf("capText(%q, 2) = %q, want %q (no mid-rune cut)", s, got, "a")
	}
	if got := capText(s, 3); got != s {
		t.Errorf("capText(%q, 3) = %q, want whole", s, got)
	}
	if got := capText(s, 0); got != s {
		t.Errorf("capText cap 0 = %q, want whole (disabled)", got)
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
	var page adminc.SessionSpansPage
	if err := json.Unmarshal(resp.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Spans) != 2 || page.Spans[0].CID != "c1" || page.Spans[1].CID != "c2" {
		t.Fatalf("spans = %+v", page.Spans)
	}
	if page.NextCursor != "" {
		t.Errorf("next_cursor = %q, want empty on the last page", page.NextCursor)
	}
	if !page.Spans[1].At.After(page.Spans[0].At) {
		t.Errorf("spans not ordered by at: %v then %v", page.Spans[0].At, page.Spans[1].At)
	}
}

// TestSessionSpansHandler_Paging walks the cursor across pages and checks the
// union equals the single-shot list, in the same stable order — the
// pre-paging wire content, reassembled page by page.
func TestSessionSpansHandler_Paging(t *testing.T) {
	at := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	var events []store.RequestEvent
	for i := 0; i < 5; i++ {
		events = append(events, store.RequestEvent{
			CorrelationID: "c" + strconv.Itoa(i),
			ObservedAt:    at.Add(time.Duration(i) * time.Second),
			SessionID:     "sess-1", ConversationID: "sess-1",
		})
	}
	q := &fakeQueries{session: events}
	h := newQueryServer(t, q)

	var walked []adminc.SessionSpan
	cursor := ""
	pages := 0
	for {
		path := "/api/v1/sessions/sess-1/spans?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		resp := get(t, h, path, true)
		if resp.Code != http.StatusOK {
			t.Fatalf("page %d status = %d", pages, resp.Code)
		}
		var page adminc.SessionSpansPage
		if err := json.Unmarshal(resp.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode page %d: %v", pages, err)
		}
		if q.lastPageParams.Limit != 2 || q.lastPageParams.Cursor != cursor {
			t.Fatalf("page params not plumbed: %+v", q.lastPageParams)
		}
		walked = append(walked, page.Spans...)
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if pages != 3 {
		t.Errorf("pages = %d, want 3 (2+2+1)", pages)
	}
	if len(walked) != len(events) {
		t.Fatalf("walked %d spans, want %d", len(walked), len(events))
	}
	for i, s := range walked {
		if s.CID != events[i].CorrelationID {
			t.Errorf("walked[%d] = %s, want %s (stable order)", i, s.CID, events[i].CorrelationID)
		}
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
	t.Run("empty later page is 200, not 404", func(t *testing.T) {
		// A cursor implies the first page existed; an empty continuation is a
		// normal end-of-list.
		h := newQueryServer(t, &fakeQueries{})
		resp := get(t, h, "/api/v1/sessions/x/spans?cursor=0", true)
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.Code)
		}
		var page adminc.SessionSpansPage
		if err := json.Unmarshal(resp.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(page.Spans) != 0 || page.NextCursor != "" {
			t.Errorf("page = %+v, want empty terminal page", page)
		}
	})
	t.Run("invalid cursor is 400", func(t *testing.T) {
		h := newQueryServer(t, &fakeQueries{session: []store.RequestEvent{{CorrelationID: "c"}}})
		if resp := get(t, h, "/api/v1/sessions/x/spans?cursor=!!!", true); resp.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.Code)
		}
	})
	t.Run("query failure is 500", func(t *testing.T) {
		h := newQueryServer(t, &fakeQueries{sessionErr: errors.New("db")})
		if resp := get(t, h, "/api/v1/sessions/x/spans", true); resp.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", resp.Code)
		}
	})
}
