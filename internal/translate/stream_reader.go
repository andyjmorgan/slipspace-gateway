package translate

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// streamingReader adapts a StreamTranslator into an io.ReadCloser that reads an
// upstream SSE body, translates each event, and yields the translated SSE
// bytes. It is pull-based (no goroutine): each Read pulls just enough upstream
// events to satisfy the request, so streaming latency is preserved end-to-end
// and there is no background goroutine to leak or cancel.
type streamingReader struct {
	src     *bufio.Reader
	closer  io.Closer
	tr      StreamTranslator
	pending bytes.Buffer
	done    bool // translator Close() already called
	eof     bool // upstream drained and translator flushed
}

// NewStreamingReader returns an io.ReadCloser that translates the upstream SSE
// body through st. Closing the returned reader closes the upstream body.
func NewStreamingReader(upstream io.ReadCloser, st StreamTranslator) io.ReadCloser {
	return &streamingReader{
		src:    bufio.NewReader(upstream),
		closer: upstream,
		tr:     st,
	}
}

// Read fills p from translated output, pulling and translating upstream SSE
// events on demand.
func (r *streamingReader) Read(p []byte) (int, error) {
	for r.pending.Len() == 0 && !r.eof {
		if err := r.fill(); err != nil {
			return 0, err
		}
	}
	if r.pending.Len() == 0 && r.eof {
		return 0, io.EOF
	}
	return r.pending.Read(p)
}

// fill reads the next upstream SSE event (or detects end of stream) and appends
// its translation to pending.
func (r *streamingReader) fill() error {
	data, ok, rerr := r.nextEvent()
	if rerr != nil && rerr != io.EOF {
		return rerr
	}
	if !ok || isDone(data) {
		return r.flush()
	}
	out, err := r.tr.Translate(data)
	if err != nil {
		return err
	}
	r.pending.Write(out)
	if rerr == io.EOF {
		return r.flush()
	}
	return nil
}

// flush calls the translator's Close exactly once and marks EOF.
func (r *streamingReader) flush() error {
	if !r.done {
		r.done = true
		out, err := r.tr.Close()
		if err != nil {
			return err
		}
		r.pending.Write(out)
	}
	r.eof = true
	return nil
}

// nextEvent reads lines until a blank-line event boundary or EOF, returning the
// payload after the last `data:` field. ok=false signals upstream EOF with no
// pending event. A trailing event with no final blank line is still returned
// (with rerr == io.EOF).
func (r *streamingReader) nextEvent() (data []byte, ok bool, rerr error) {
	var payload []byte
	sawData := false
	for {
		line, err := r.src.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(trimmed, "data:"):
			payload = []byte(strings.TrimSpace(trimmed[len("data:"):]))
			sawData = true
		case trimmed == "" && sawData && err == nil:
			return payload, true, nil
		}
		if err != nil {
			if sawData {
				return payload, true, err
			}
			return nil, false, err
		}
	}
}

// isDone reports whether an SSE data payload is the OpenAI [DONE] sentinel.
func isDone(data []byte) bool {
	return string(bytes.TrimSpace(data)) == "[DONE]"
}

// Close closes the underlying upstream body.
func (r *streamingReader) Close() error {
	return r.closer.Close()
}
