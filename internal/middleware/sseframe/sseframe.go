// Package sseframe collates a provider response body into the JSON
// documents that carry it, once. A non-streaming body is a single document;
// an SSE stream is one document per data frame.
//
// It exists so the response is split exactly once per request: the reporter
// collates the captured response and hands the same frames to both the
// token-usage extractor and the GenAI-attribute extractor, rather than each
// re-scanning the raw bytes. The content accumulator keeps its own parser —
// it needs SSE event names for stateful reassembly, which telemetry
// extraction does not.
package sseframe

import (
	"bufio"
	"bytes"
	"strings"
)

// maxFrameBytes bounds a single SSE frame. Provider payloads carry single
// events up to a few hundred KB (large tool-call argument deltas); 4 MiB
// matches the accumulator and token scanners so no component clips an event.
const maxFrameBytes = 4 * 1024 * 1024

// Collate returns the JSON documents in raw: the whole body as one frame
// when it is a JSON object/array, or each SSE data frame's payload when it
// is a stream. Empty frames and the `[DONE]` sentinel are dropped. Returns
// nil for empty input.
//
// Frames are returned as sub-slices/copies suitable for json.Unmarshal; the
// caller owns them.
func Collate(raw []byte) [][]byte {
	if len(raw) == 0 {
		return nil
	}
	if looksLikeJSON(raw) {
		return [][]byte{raw}
	}

	var frames [][]byte
	var dataLines []string

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), maxFrameBytes)

	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		if data == "" || strings.TrimSpace(data) == "[DONE]" {
			return
		}
		frames = append(frames, []byte(data))
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, ":"), strings.HasPrefix(line, "event:"):
			continue
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	return frames
}

// looksLikeJSON reports whether raw's first non-whitespace byte is `{` or
// `[` — i.e. a JSON body rather than an SSE stream.
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
