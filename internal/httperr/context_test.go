package httperr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFromContext_PassiveDefault(t *testing.T) {
	wr := FromContext(context.Background())
	if wr == nil {
		t.Fatal("FromContext returned nil; want passive writer")
	}
	rec := httptest.NewRecorder()
	wr.Write(context.Background(), rec, http.StatusInternalServerError, "rules", "internal", "boom")
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var body Body
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	if body.Error != "internal" || body.Message != "boom" {
		t.Fatalf("body = %+v", body)
	}
}

func TestWithWriter_RoundTrip(t *testing.T) {
	wr := New(nil, nil)
	ctx := WithWriter(context.Background(), wr)
	if got := FromContext(ctx); got != wr {
		t.Fatalf("FromContext = %p, want the installed writer %p", got, wr)
	}
	if got := WithWriter(context.Background(), nil); got != context.Background() {
		t.Fatal("WithWriter(nil) must return ctx unchanged")
	}
}

func TestHandler_InstallsWriter(t *testing.T) {
	wr := New(nil, nil)
	var seen *Writer
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = FromContext(r.Context())
	})
	Handler(wr, next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if seen != wr {
		t.Fatalf("handler saw %p, want %p", seen, wr)
	}

	// nil writer: passthrough, next is returned as-is.
	if got := Handler(nil, next); got == nil {
		t.Fatal("Handler(nil, next) returned nil")
	}
}
