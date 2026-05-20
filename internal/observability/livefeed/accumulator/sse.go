package accumulator

import (
	"bufio"
	"bytes"
	"strings"
)

// sseEvent is one parsed SSE frame. Name carries the `event:` field
// (empty when the frame omitted it — most providers use the default
// "message" event implicitly). Data is the concatenated `data:` lines
// joined by newlines, per the SSE spec.
type sseEvent struct {
	Name string
	Data string
}

// parseSSE walks the raw byte buffer, splitting on blank lines, and
// returns one sseEvent per frame. Lines starting with ":" are
// comments (heartbeats) and skipped. Empty events (no data lines) are
// also skipped. Order is preserved.
//
// The parser is forgiving: any line that doesn't start with `event:`,
// `data:`, or a comment is silently ignored. Providers don't use
// `id:` or `retry:` on these endpoints, but a future change could be
// handled by extending here without touching callers.
func parseSSE(raw []byte) []sseEvent {
	var events []sseEvent
	var current sseEvent
	var dataLines []string

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	// Provider SSE payloads carry single events up to a few hundred KB
	// (large tool-call arguments). Default scanner buffer is 64 KB —
	// bump to 4 MB to match the per-body cap envelope.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	flush := func() {
		if len(dataLines) > 0 {
			current.Data = strings.Join(dataLines, "\n")
			events = append(events, current)
		}
		current = sseEvent{}
		dataLines = nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			current.Name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			continue
		}
		// Unknown field — silently skip (id:, retry:, etc.).
	}
	flush()
	return events
}
