package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveSessionID(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		aliases []string
		want    string
	}{
		{
			name:    "canonical present",
			headers: map[string]string{"X-Sluice-Session-Id": "canon"},
			aliases: []string{"X-Agentling-Task-Id"},
			want:    "canon",
		},
		{
			name:    "canonical wins over alias",
			headers: map[string]string{"X-Sluice-Session-Id": "canon", "X-Agentling-Task-Id": "alias"},
			aliases: []string{"X-Agentling-Task-Id"},
			want:    "canon",
		},
		{
			name:    "alias fallback when canonical absent",
			headers: map[string]string{"X-Agentling-Task-Id": "alias"},
			aliases: []string{"X-Agentling-Task-Id"},
			want:    "alias",
		},
		{
			name:    "first configured alias wins",
			headers: map[string]string{"X-Trace-Id": "trace", "X-Agentling-Task-Id": "alias"},
			aliases: []string{"X-Agentling-Task-Id", "X-Trace-Id"},
			want:    "alias",
		},
		{
			name:    "later alias used when earlier absent",
			headers: map[string]string{"X-Trace-Id": "trace"},
			aliases: []string{"X-Agentling-Task-Id", "X-Trace-Id"},
			want:    "trace",
		},
		{
			name:    "none present",
			headers: nil,
			aliases: []string{"X-Agentling-Task-Id"},
			want:    "",
		},
		{
			name:    "no aliases configured, no canonical",
			headers: map[string]string{"X-Agentling-Task-Id": "alias"},
			aliases: nil,
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := resolveSessionID(r, tc.aliases); got != tc.want {
				t.Errorf("resolveSessionID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCorrelationMiddleware_SessionEcho(t *testing.T) {
	cases := []struct {
		name        string
		headers     map[string]string
		aliases     []string
		wantSession string
		wantLogged  bool
	}{
		{
			name:        "canonical echoed and logged",
			headers:     map[string]string{"X-Sluice-Session-Id": "canon"},
			wantSession: "canon",
			wantLogged:  true,
		},
		{
			name:        "alias echoed and logged",
			headers:     map[string]string{"X-Agentling-Task-Id": "task-42"},
			aliases:     []string{"X-Agentling-Task-Id"},
			wantSession: "task-42",
			wantLogged:  true,
		},
		{
			name:        "absent leaves header unset and unlogged",
			headers:     nil,
			aliases:     []string{"X-Agentling-Task-Id"},
			wantSession: "",
			wantLogged:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))

			var nextCalled bool
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			h := correlationMiddleware(logger, tc.aliases, next)

			r := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if !nextCalled {
				t.Fatal("next handler not invoked")
			}
			if got := rec.Header().Get(headerSessionID); got != tc.wantSession {
				t.Errorf("%s = %q, want %q", headerSessionID, got, tc.wantSession)
			}
			if rec.Header().Get(headerCorrelationID) == "" {
				t.Errorf("%s unset, want a generated value", headerCorrelationID)
			}
			loggedSession := strings.Contains(buf.String(), `"session_id"`)
			if loggedSession != tc.wantLogged {
				t.Errorf("session_id logged = %v, want %v (log: %s)", loggedSession, tc.wantLogged, buf.String())
			}
		})
	}
}

func TestCorrelationMiddleware_HonoursInboundCorrelationID(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := correlationMiddleware(logger, nil, next)

	const id = "client-supplied-corr"
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(headerCorrelationID, id)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if got := rec.Header().Get(headerCorrelationID); got != id {
		t.Errorf("%s = %q, want %q", headerCorrelationID, got, id)
	}
}
