package bodycapture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/slipspace-gateway/protocols/gemini/content"
	openaichat "github.com/andyjmorgan/slipspace-gateway/protocols/openai/chat"
	openairesponses "github.com/andyjmorgan/slipspace-gateway/protocols/openai/responses"
)

const (
	chatBody = `{
		"model": "gpt-4o",
		"messages": [{"role":"user","content":"hi"}],
		"temperature": 0.2,
		"future_field": {"shape": "tbd"}
	}`

	responsesBody = `{
		"model": "gpt-4o",
		"input": "hello",
		"future_field": "round-trips"
	}`

	messagesBody = `{
		"model": "claude-3-7-sonnet-20250219",
		"max_tokens": 1024,
		"messages": [{"role":"user","content":"hi"}],
		"future_field": [1,2,3]
	}`

	geminiBody = `{
		"contents": [{"role":"user","parts":[{"text":"hi"}]}],
		"generationConfig": {"temperature": 0.1},
		"future_field": true
	}`

	opaqueRaw = "raw-bytes-not-json"
)

func kindFromValue(k RequestKind, ok bool) KindFromContextFunc {
	return func(context.Context) (RequestKind, bool) { return k, ok }
}

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func TestCapture_HappyPaths(t *testing.T) {
	cases := []struct {
		name string

		kind RequestKind

		body string

		assertBody func(t *testing.T, c Captured)
	}{
		{
			name: "chat",
			kind: KindChat,
			body: chatBody,
			assertBody: func(t *testing.T, c Captured) {
				req, ok := c.Body.(*openaichat.ChatCompletionRequest)
				if !ok {
					t.Fatalf("body type = %T want *openaichat.ChatCompletionRequest", c.Body)
				}
				if req.Model != "gpt-4o" {
					t.Fatalf("model = %q", req.Model)
				}
				if _, present := req.Extra["future_field"]; !present {
					t.Fatalf("unknown future_field did not survive: %v", req.Extra)
				}
			},
		},
		{
			name: "responses",
			kind: KindResponses,
			body: responsesBody,
			assertBody: func(t *testing.T, c Captured) {
				req, ok := c.Body.(*openairesponses.ResponsesRequest)
				if !ok {
					t.Fatalf("body type = %T", c.Body)
				}
				if req.Model != "gpt-4o" {
					t.Fatalf("model = %q", req.Model)
				}
				if _, present := req.Extra["future_field"]; !present {
					t.Fatalf("unknown future_field did not survive")
				}
			},
		},
		{
			name: "messages",
			kind: KindMessages,
			body: messagesBody,
			assertBody: func(t *testing.T, c Captured) {
				req, ok := c.Body.(*messages.MessagesRequest)
				if !ok {
					t.Fatalf("body type = %T", c.Body)
				}
				if req.MaxTokens != 1024 {
					t.Fatalf("max_tokens = %d", req.MaxTokens)
				}
				if _, present := req.Extra["future_field"]; !present {
					t.Fatalf("unknown future_field did not survive")
				}
			},
		},
		{
			name: "generate_content",
			kind: KindGenerateContent,
			body: geminiBody,
			assertBody: func(t *testing.T, c Captured) {
				req, ok := c.Body.(*content.GenerateContentRequest)
				if !ok {
					t.Fatalf("body type = %T", c.Body)
				}
				if len(req.Contents) != 1 {
					t.Fatalf("contents = %d", len(req.Contents))
				}
				if _, present := req.Extra["future_field"]; !present {
					t.Fatalf("unknown future_field did not survive")
				}
			},
		},
		{
			name: "passthrough_skips_parse",
			kind: KindPassthrough,
			body: opaqueRaw,
			assertBody: func(t *testing.T, c Captured) {
				if c.Body != nil {
					t.Fatalf("passthrough Body = %v want nil", c.Body)
				}
				if string(c.Raw) != opaqueRaw {
					t.Fatalf("passthrough raw = %q", string(c.Raw))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			got, err := Capture(req, tc.kind, nil)
			if err != nil {
				t.Fatalf("Capture: %v", err)
			}
			if got.Kind != tc.kind {
				t.Fatalf("kind = %q want %q", got.Kind, tc.kind)
			}
			if string(got.Raw) != tc.body {
				t.Fatalf("raw bytes mutated: got %q want %q", string(got.Raw), tc.body)
			}
			tc.assertBody(t, got)
		})
	}
}

func TestCapture_NilBodyTreatedAsEmpty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Body = nil
	_, err := Capture(req, KindPassthrough, nil)
	if err != nil {
		t.Fatalf("nil-body passthrough should not error: %v", err)
	}
}

func TestCapture_BodyTooLarge(t *testing.T) {
	big := bytes.Repeat([]byte("a"), int(MaxBodyBytes)+1)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(big))
	_, err := Capture(req, KindPassthrough, nil)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v want ErrBodyTooLarge", err)
	}
}

func TestCapture_MalformedJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"model": "gpt-4",`))
	_, err := Capture(req, KindChat, nil)
	if !errors.Is(err, ErrParse) {
		t.Fatalf("err = %v want ErrParse", err)
	}
}

func TestCapture_UnknownKind(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	_, err := Capture(req, RequestKind("totally-new"), nil)
	if !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("err = %v want ErrUnknownKind", err)
	}
}

func TestCapture_ReadError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", &errReader{})
	_, err := Capture(req, KindChat, nil)
	if err == nil {
		t.Fatalf("expected read error")
	}
	if errors.Is(err, ErrBodyTooLarge) || errors.Is(err, ErrParse) || errors.Is(err, ErrUnknownKind) {
		t.Fatalf("read error should not match typed sentinels: %v", err)
	}
}

func TestCapture_DynamicPropertiesRoundTrip(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(chatBody))
	got, err := Capture(req, KindChat, nil)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	out, err := json.Marshal(got.Body)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !bytes.Contains(out, []byte(`"future_field"`)) {
		t.Fatalf("future_field dropped on re-marshal: %s", out)
	}
}

func TestHTTPHandler_HappyPath(t *testing.T) {
	var seen Captured
	var bodyAfter []byte
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := FromContext(r.Context())
		if !ok {
			t.Fatalf("Captured missing from context")
		}
		seen = c

		read, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("forwarder read: %v", err)
		}
		bodyAfter = read
		w.WriteHeader(http.StatusOK)
	})

	h := HTTPHandler(kindFromValue(KindChat, true), nil, next)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(chatBody))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	if seen.Kind != KindChat {
		t.Fatalf("captured kind = %q", seen.Kind)
	}
	if string(bodyAfter) != chatBody {
		t.Fatalf("downstream body mismatch: got %q want %q", bodyAfter, chatBody)
	}
}

func TestHTTPHandler_PassthroughReplacesBody(t *testing.T) {
	var bodyAfter []byte
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read, _ := io.ReadAll(r.Body)
		bodyAfter = read
		w.WriteHeader(http.StatusOK)
	})

	h := HTTPHandler(kindFromValue(KindPassthrough, true), nil, next)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(opaqueRaw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if string(bodyAfter) != opaqueRaw {
		t.Fatalf("passthrough body mismatch: %q", bodyAfter)
	}
}

func TestHTTPHandler_TooLarge(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true })

	big := bytes.Repeat([]byte("a"), int(MaxBodyBytes)+1)
	h := HTTPHandler(kindFromValue(KindChat, true), nil, next)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(big))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d want 413", rec.Code)
	}
	if nextCalled {
		t.Fatalf("next must not be invoked on too-large body")
	}
	assertErrorBody(t, rec.Result().Body, "body too large")
}

func TestHTTPHandler_MalformedBody(t *testing.T) {
	logger, logs := captureLogger()

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatalf("next must not be invoked on malformed body")
	})

	h := HTTPHandler(kindFromValue(KindChat, true), nil, next)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{not json`))
	req = req.WithContext(observability.WithLogger(req.Context(), logger))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", rec.Code)
	}
	assertErrorBody(t, rec.Result().Body, "malformed request body")
	if !strings.Contains(logs.String(), `"result":"malformed_body"`) {
		t.Fatalf("malformed_body result not logged: %s", logs.String())
	}
}

func TestHTTPHandler_MissingKindOnContext(t *testing.T) {
	logger, logs := captureLogger()

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatalf("next must not be invoked without a kind")
	})

	h := HTTPHandler(kindFromValue("", false), nil, next)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(chatBody))
	req = req.WithContext(observability.WithLogger(req.Context(), logger))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d want 500", rec.Code)
	}
	assertErrorBody(t, rec.Result().Body, "no request kind on context")
	if !strings.Contains(logs.String(), `"result":"no_kind"`) {
		t.Fatalf("no_kind result not logged: %s", logs.String())
	}
}

func TestHTTPHandler_UnknownKind(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatalf("next must not be invoked on unknown kind")
	})

	h := HTTPHandler(kindFromValue("ghost-shape", true), nil, next)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d want 500", rec.Code)
	}
	assertErrorBody(t, rec.Result().Body, "unknown request kind")
}

func TestHTTPHandler_ReadError(t *testing.T) {
	logger, logs := captureLogger()

	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatalf("next must not be invoked on read failure")
	})

	h := HTTPHandler(kindFromValue(KindChat, true), nil, next)
	req := httptest.NewRequest(http.MethodPost, "/", &errReader{})
	req = req.WithContext(observability.WithLogger(req.Context(), logger))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d want 500", rec.Code)
	}
	if !strings.Contains(logs.String(), `"result":"internal"`) {
		t.Fatalf("internal result not logged: %s", logs.String())
	}
}

func TestHTTPHandler_PanicsOnNilDeps(t *testing.T) {
	cases := []struct {
		name string

		kindFrom KindFromContextFunc

		next http.Handler
	}{
		{"nil kindFrom", nil, http.NotFoundHandler()},
		{"nil next", kindFromValue(KindChat, true), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic for %s", tc.name)
				}
			}()
			HTTPHandler(tc.kindFrom, nil, tc.next)
		})
	}
}

func TestFromContext_Empty(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Fatalf("FromContext should report false when nothing is stashed")
	}
	if _, ok := FromContext(nil); ok { //nolint:staticcheck
		t.Fatalf("FromContext(nil) should report false")
	}
}

func TestAllocate_PassthroughIsRejected(t *testing.T) {
	if _, err := allocate(KindPassthrough); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("allocate(passthrough) = %v want ErrUnknownKind", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }
func (errReader) Close() error             { return nil }

func assertErrorBody(t *testing.T, body io.ReadCloser, want string) {
	t.Helper()
	defer func() { _ = body.Close() }()
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if payload.Error != want {
		t.Fatalf("error body = %q want %q", payload.Error, want)
	}
}
