package tokens

import "testing"

func TestLooksLikeJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  []byte
		want bool
	}{
		{"object", []byte(`{"x":1}`), true},
		{"array", []byte(`[1,2,3]`), true},
		{"leading whitespace then object", []byte("  \n\t{\"x\":1}"), true},
		{"leading whitespace then array", []byte("\n  [1]"), true},
		{"sse data line", []byte("data: {\"x\":1}"), false},
		{"event line", []byte("event: foo"), false},
		{"empty", []byte(""), false},
		{"whitespace only", []byte("   \n\t  "), false},
		{"plain text", []byte("hello"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := looksLikeJSON(tc.raw); got != tc.want {
				t.Errorf("looksLikeJSON(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseSSE_HandlesCommentsAndUnknownFields(t *testing.T) {
	t.Parallel()
	raw := []byte(`: heartbeat comment
event: ping
data: {"a":1}
id: ignored-id-line
retry: 1000

event: message
data: {"b":2}

`)
	got := parseSSE(raw)
	if len(got) != 2 {
		t.Fatalf("len(events) = %d, want 2; events=%+v", len(got), got)
	}
	if got[0].Name != "ping" || got[0].Data != `{"a":1}` {
		t.Errorf("events[0] = %+v, want {ping, {\"a\":1}}", got[0])
	}
	if got[1].Name != "message" || got[1].Data != `{"b":2}` {
		t.Errorf("events[1] = %+v, want {message, {\"b\":2}}", got[1])
	}
}

func TestParseSSE_EmptyEventSkipped(t *testing.T) {
	t.Parallel()
	// An "event:" line with no following data line should not produce
	// an output event — the flush logic requires at least one data
	// line to emit.
	raw := []byte("event: empty-frame\n\nevent: real\ndata: {\"x\":1}\n\n")
	got := parseSSE(raw)
	if len(got) != 1 {
		t.Fatalf("len(events) = %d, want 1; events=%+v", len(got), got)
	}
}
