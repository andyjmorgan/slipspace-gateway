//go:build e2e

package streaming_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/andyjmorgan/sluice-gateway/test/e2e/harness"
)

func TestMain(m *testing.M) {
	// Tolerate goroutines parked in long-lived stdlib I/O loops owned by the
	// testcontainers SDK and the exec.Cmd wrappers used to spawn the gateway
	// and mockllm processes. Anything other than these is a real leak.
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"),
		goleak.IgnoreTopFunction("os/exec.(*Cmd).watchCtx"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreTopFunction("net/http.(*persistConn).writeLoop"),
	)
}

func TestStreaming_SSEFraming_PreservesChunkBoundaries(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"choices":[{"delta":{"role":"assistant"}}]}`},
			{Data: `{"choices":[{"delta":{"content":"a"}}]}`},
			{Data: `{"choices":[{"delta":{"content":"b"}}]}`},
			{Data: `{"choices":[{"delta":{"content":"c"}}]}`},
			{Data: "[DONE]"},
		},
	})

	stream := h.PostStream("/v1/chat/completions",
		map[string]any{"model": "gpt-4o", "stream": true, "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	chunks := stream.CollectAll(5 * time.Second)
	if len(chunks) != 5 {
		t.Fatalf("got %d chunks, want 5: %+v", len(chunks), chunks)
	}
	if !chunks[4].IsDone() {
		t.Errorf("final chunk is not [DONE]: %s", chunks[4].Data)
	}
	for i, want := range []string{"role", `"a"`, `"b"`, `"c"`} {
		if !strings.Contains(chunks[i].Data, want) {
			t.Errorf("chunk %d=%q, want contains %q", i, chunks[i].Data, want)
		}
	}
}

func TestStreaming_SlowUpstream_ChunksArriveAsTheyAreEmitted(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	const delay = 200 * time.Millisecond
	h.StageMockResponse(harness.CannedResponse{
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		Streaming: true,
		Headers:   map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: []harness.CannedStreamChunk{
			{Data: `{"choices":[{"delta":{"role":"assistant"}}]}`},
			{Data: `{"choices":[{"delta":{"content":"slow"}}]}`, DelayMs: int(delay / time.Millisecond)},
			{Data: "[DONE]"},
		},
	})

	start := time.Now()
	stream := h.PostStream("/v1/chat/completions",
		map[string]any{"model": "gpt-4o", "stream": true, "messages": []map[string]string{{"role": "user", "content": "."}}},
		nil)
	defer func() { _ = stream.Close() }()

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	firstAt := time.Since(start)
	if firstAt > delay/2 {
		t.Errorf("first chunk too slow: %s (delay was %s); gateway is buffering instead of flushing", firstAt, delay)
	}
	if !strings.Contains(first.Data, "role") {
		t.Errorf("first chunk unexpected: %s", first.Data)
	}

	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second chunk: %v", err)
	}
	secondAt := time.Since(start)
	if secondAt < delay {
		t.Errorf("second chunk too fast: %s, want >= %s (mockllm delay)", secondAt, delay)
	}
	if !strings.Contains(second.Data, "slow") {
		t.Errorf("second chunk unexpected: %s", second.Data)
	}
}

func TestStreaming_ClientDisconnect_GatewayCleansUp(t *testing.T) {
	t.Parallel()
	h := harness.New(t)

	chunks := make([]harness.CannedStreamChunk, 0, 20)
	for i := 0; i < 20; i++ {
		chunks = append(chunks, harness.CannedStreamChunk{
			Data:    `{"choices":[{"delta":{"content":"x"}}]}`,
			DelayMs: 200,
		})
	}
	h.StageMockResponse(harness.CannedResponse{
		Method:       http.MethodPost,
		Path:         "/v1/chat/completions",
		Streaming:    true,
		Headers:      map[string]string{"Content-Type": "text/event-stream"},
		StreamChunks: chunks,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	body := strings.NewReader(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"."}]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.GatewayURL+"/v1/chat/completions", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("status=%d", resp.StatusCode)
	}

	buf := make([]byte, 256)
	if _, err := resp.Body.Read(buf); err != nil && err != io.EOF {
		t.Fatalf("read first: %v", err)
	}

	cancel()
	_ = resp.Body.Close()

	healthCtx, healthCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer healthCancel()
	healthReq, _ := http.NewRequestWithContext(healthCtx, http.MethodGet, h.GatewayURL+"/healthz", http.NoBody)
	hr, err := h.HTTP.Do(healthReq)
	if err != nil {
		t.Fatalf("post-disconnect healthz: %v", err)
	}
	defer func() { _ = hr.Body.Close() }()
	if hr.StatusCode != http.StatusOK {
		t.Errorf("post-disconnect healthz status=%d", hr.StatusCode)
	}

	// The mid-stream disconnect drives the gateway's ReverseProxy into
	// http.ErrAbortHandler. That sentinel is benign (the client left), so the
	// recover middleware must NOT count it against gateway.request.panics.total
	// — otherwise every cancelled SSE stream inflates the panic counter and
	// error logs. With the bug the counter series appears within a chunk or two;
	// poll briefly and require it to stay absent.
	deadline := time.Now().Add(3 * time.Second)
	for {
		body := scrapeGatewayMetrics(t, h.PromURL())
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "gateway_request_panics_total") {
				t.Fatalf("client-disconnect abort was counted as a request panic: %s", line)
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// scrapeGatewayMetrics fetches the gateway's Prometheus exposition text.
func scrapeGatewayMetrics(t *testing.T, promURL string) string {
	t.Helper()
	resp, err := http.Get(promURL + "/metrics") //nolint:gosec,noctx // test scrape against the harness's bound prom port
	if err != nil {
		t.Fatalf("scrape metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	return string(b)
}
