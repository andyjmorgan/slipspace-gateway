package server

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAcceptsGzip pins the negotiation: gzip members opt in unless their
// q-value is zero; other codings never do.
func TestAcceptsGzip(t *testing.T) {
	tests := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"identity", false},
		{"br, deflate", false},
		{"gzip", true},
		{"GZIP", true},
		{"gzip, deflate, br", true},
		{"br;q=1.0, gzip;q=0.8, *;q=0.1", true},
		{"gzip;q=0", false},
		{"gzip;q=0.0", false},
		{"gzip; q=0.5", true},
		{"deflate, gzip ;q=1", true},
	}
	for _, tc := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.header != "" {
			r.Header.Set("Accept-Encoding", tc.header)
		}
		if got := acceptsGzip(r); got != tc.want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

// TestWithGzip proves the middleware round-trip: a negotiating client gets a
// gzip stream (with the headers a cache needs) that gunzips back to the
// handler's exact bytes; a non-negotiating client gets identity bytes with no
// encoding header.
func TestWithGzip(t *testing.T) {
	body := strings.Repeat(`{"spans":[{"cid":"x"}]}`, 200)
	h := withGzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, body)
	}))

	t.Run("negotiated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding = %q, want gzip", got)
		}
		if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
			t.Errorf("Vary = %q, want Accept-Encoding", got)
		}
		if rec.Body.Len() >= len(body) {
			t.Errorf("compressed %d bytes >= plain %d — not actually compressing", rec.Body.Len(), len(body))
		}
		zr, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		plain, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("gunzip: %v", err)
		}
		var got string
		if err := json.Unmarshal(plain, &got); err != nil || got != body {
			t.Errorf("gunzipped body does not round-trip the handler's JSON (err %v)", err)
		}
	})

	t.Run("identity fallback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("Content-Encoding = %q, want none", got)
		}
		var got string
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got != body {
			t.Errorf("identity body does not carry the handler's JSON (err %v)", err)
		}
	})

	t.Run("status passes through", func(t *testing.T) {
		notFound := withGzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "nope")
		}))
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		notFound.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}
