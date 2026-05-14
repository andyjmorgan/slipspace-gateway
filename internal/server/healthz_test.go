package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthzHandler_GET_OK(t *testing.T) {
	h := NewHealthzHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	h.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status: got %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "text/plain; charset=utf-8"; got != want {
		t.Fatalf("content-type: got %q, want %q", got, want)
	}
	if got, want := rec.Body.String(), "ok\n"; got != want {
		t.Fatalf("body: got %q, want %q", got, want)
	}
}

func TestHealthzHandler_GET_Draining(t *testing.T) {
	h := NewHealthzHandler()
	h.MarkDraining()

	if !h.IsDraining() {
		t.Fatal("IsDraining should report true after MarkDraining")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	h.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusServiceUnavailable; got != want {
		t.Fatalf("status: got %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "text/plain; charset=utf-8"; got != want {
		t.Fatalf("content-type: got %q, want %q", got, want)
	}
	if got, want := rec.Body.String(), "draining\n"; got != want {
		t.Fatalf("body: got %q, want %q", got, want)
	}
}

func TestHealthzHandler_MarkDrainingIdempotent(t *testing.T) {
	h := NewHealthzHandler()
	h.MarkDraining()
	h.MarkDraining()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHealthzHandler_MethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			h := NewHealthzHandler()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/healthz", strings.NewReader(""))

			h.ServeHTTP(rec, req)

			if got, want := rec.Code, http.StatusMethodNotAllowed; got != want {
				t.Fatalf("status: got %d, want %d", got, want)
			}
			if got, want := rec.Header().Get("Allow"), http.MethodGet; got != want {
				t.Fatalf("allow header: got %q, want %q", got, want)
			}
		})
	}
}

func TestHealthzHandler_CancelledRequestContext(t *testing.T) {
	h := NewHealthzHandler()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(ctx)

	h.ServeHTTP(rec, req)

	// We bailed before writing — body should be empty.
	body, _ := io.ReadAll(rec.Body)
	if len(body) != 0 {
		t.Fatalf("expected empty body on cancelled context, got %q", string(body))
	}
}
