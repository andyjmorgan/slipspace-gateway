package tokens

import (
	"bufio"
	"bytes"
	"strings"
)

// sseEvent is one parsed SSE frame; mirrors the accumulator package's
// internal sseEvent so the two packages stay independent. Kept private
// because the only consumer is the per-provider extractor in this
// package.
type sseEvent struct {
	Name string
	Data string
}

// parseSSE walks raw, splitting on blank lines, returning one sseEvent
// per frame. Comments (`:`-prefixed lines) and empty events are
// skipped. Order is preserved.
//
// A parallel implementation lives in
// internal/observability/livefeed/accumulator/sse.go — the two are kept
// independent on purpose. The accumulator package consumes only
// `chat_completions` / `messages` / `generate_content` chunks and is
// indexed by endpoint name; this package needs identical parsing but
// across a slightly different endpoint set. Sharing the helper would
// pull accumulator's exported surface (or a new shared package) along
// with it; ~30 lines is below the threshold where dedupe pays for
// itself.
func parseSSE(raw []byte) []sseEvent {
	var events []sseEvent
	var current sseEvent
	var dataLines []string

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	// Provider SSE payloads carry single events up to a few hundred KB
	// (large tool-call argument deltas). Matches the accumulator's
	// buffer for symmetry — neither component should clip mid-event.
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
	}
	flush()
	return events
}

// looksLikeJSON reports whether raw's first non-whitespace byte is `{`
// or `[`. Used to dispatch JSON-body parsing vs SSE parsing without
// taking a streaming flag on Extract — the caller (reporter) doesn't
// always have one (a body buffer captured during shutdown may not
// carry the streaming bit).
func looksLikeJSON(raw []byte) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}
