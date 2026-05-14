//go:build e2e

package harness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PostJSON issues a JSON POST to the gateway. It applies a default
// Authorization header (the dev API key) and Content-Type: application/json
// unless the caller overrides them. The response body is fully read into a
// buffer so callers can re-read it via [Response.Body]. On any transport
// error the test fails via t.Fatalf.
func (h *Harness) PostJSON(path string, body any, headers http.Header) *Response {
	h.T.Helper()
	return h.do(http.MethodPost, path, body, headers, false)
}

// PostStream issues a streaming POST. It defaults Accept: text/event-stream
// and returns an [SSEReader] backed by the live HTTP body. The caller must
// invoke [SSEReader.Close] when done (CollectAll closes on completion).
func (h *Harness) PostStream(path string, body any, headers http.Header) *SSEReader {
	h.T.Helper()
	// Caller closes the streaming body via SSEReader.Close (or CollectAll).
	resp := h.doRaw(http.MethodPost, path, body, headers, true) //nolint:bodyclose
	return &SSEReader{
		resp:   resp,
		reader: bufio.NewReader(resp.Body),
	}
}

// Response is a buffered HTTP response. Body is fully drained and closed
// before this struct is returned.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// JSONUnmarshal decodes Body into v.
func (r *Response) JSONUnmarshal(v any) error {
	return json.Unmarshal(r.Body, v)
}

func (h *Harness) do(method, path string, body any, headers http.Header, stream bool) *Response {
	h.T.Helper()
	resp := h.doRaw(method, path, body, headers, stream)
	defer func() { _ = resp.Body.Close() }()

	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		h.T.Fatalf("harness: read response body: %v", err)
	}
	return &Response{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       buf,
	}
}

func (h *Harness) doRaw(method, path string, body any, headers http.Header, stream bool) *http.Response {
	h.T.Helper()

	var reader io.Reader
	if body != nil {
		switch b := body.(type) {
		case []byte:
			reader = bytes.NewReader(b)
		case string:
			reader = strings.NewReader(b)
		case io.Reader:
			reader = b
		default:
			raw, err := json.Marshal(body)
			if err != nil {
				h.T.Fatalf("harness: marshal request body: %v", err)
			}
			reader = bytes.NewReader(raw)
		}
	}

	url := h.GatewayURL + path
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		h.T.Fatalf("harness: build request %s %s: %v", method, url, err)
	}

	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+h.APIKey)
	}
	if stream && req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "text/event-stream")
	}

	for k, vs := range headers {
		req.Header.Del(k)
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	client := h.HTTP
	if stream {
		// Streaming responses must not be cut short by the buffered client's
		// overall Timeout — caller controls duration via context/deadline.
		client = &http.Client{Transport: h.HTTP.Transport}
	}

	resp, err := client.Do(req)
	if err != nil {
		h.T.Fatalf("harness: %s %s: %v", method, url, err)
	}
	return resp
}

// SSEReader parses a `text/event-stream` body emitting [SSEChunk] records.
type SSEReader struct {
	resp   *http.Response
	reader *bufio.Reader
}

// SSEChunk is one SSE event. The `data:` body is unescaped per RFC 9457 §8 —
// successive `data:` lines join with a literal newline.
type SSEChunk struct {
	Event string
	Data  string
	ID    string
}

// IsDone reports whether the chunk is the OpenAI "[DONE]" terminator.
func (c SSEChunk) IsDone() bool {
	return strings.TrimSpace(c.Data) == "[DONE]"
}

// Status returns the HTTP status of the streaming response.
func (s *SSEReader) Status() int {
	return s.resp.StatusCode
}

// Header returns the HTTP response headers.
func (s *SSEReader) Header() http.Header {
	return s.resp.Header
}

// Close releases the underlying HTTP body.
func (s *SSEReader) Close() error {
	return s.resp.Body.Close()
}

// Next reads the next SSE event. Returns io.EOF when the stream ends.
func (s *SSEReader) Next() (SSEChunk, error) {
	var (
		chunk     SSEChunk
		dataParts []string
		started   bool
	)

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && started {
				chunk.Data = strings.Join(dataParts, "\n")
				return chunk, nil
			}
			return SSEChunk{}, err
		}
		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			if !started {
				continue
			}
			chunk.Data = strings.Join(dataParts, "\n")
			return chunk, nil
		}

		started = true

		switch {
		case strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "data:"):
			dataParts = append(dataParts, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case strings.HasPrefix(line, "event:"):
			chunk.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "id:"):
			chunk.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		}
	}
}

// CollectAll drains the stream and closes it. Fails the test on any error
// other than io.EOF.
func (s *SSEReader) CollectAll(deadline time.Duration) []SSEChunk {
	defer func() { _ = s.Close() }()

	stopAt := time.Now().Add(deadline)
	var out []SSEChunk
	for {
		if deadline > 0 && time.Now().After(stopAt) {
			return out
		}
		chunk, err := s.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			return out
		}
		out = append(out, chunk)
		if chunk.IsDone() {
			return out
		}
	}
}

// Discard reads and drops the rest of the body. Useful for tests that only
// care that the stream terminates correctly.
func (s *SSEReader) Discard() {
	defer func() { _ = s.Close() }()
	_, _ = io.Copy(io.Discard, s.resp.Body)
}

// String is a debug aid; do not depend on the format.
func (c SSEChunk) String() string {
	return fmt.Sprintf("SSEChunk{event=%q id=%q data=%q}", c.Event, c.ID, c.Data)
}
