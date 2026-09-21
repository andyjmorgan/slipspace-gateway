package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// helloMiddleware sits in front of auth, so the two things that matter are
// (a) the probe is answered without credentials and (b) nothing else is —
// a regression on (b) would open an unauthenticated hole in the data plane.

func TestHelloMiddleware(t *testing.T) {
	t.Parallel()

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusUnauthorized)
	})

	cases := []struct {
		name     string
		method   string
		path     string
		wantNext bool
		wantCode int
		wantBody string
	}{
		{name: "GET answered locally", method: http.MethodGet, path: "/api/hello", wantCode: http.StatusOK, wantBody: helloBody},
		{name: "HEAD answered locally with empty body", method: http.MethodHead, path: "/api/hello", wantCode: http.StatusOK, wantBody: ""},
		{name: "POST falls through", method: http.MethodPost, path: "/api/hello", wantNext: true, wantCode: http.StatusUnauthorized},
		{name: "other path falls through", method: http.MethodGet, path: "/v1/messages", wantNext: true, wantCode: http.StatusUnauthorized},
		{name: "subpath falls through", method: http.MethodGet, path: "/api/hello/x", wantNext: true, wantCode: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nextCalled = false
			rec := httptest.NewRecorder()
			helloMiddleware(next).ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))

			if nextCalled != tc.wantNext {
				t.Fatalf("next called = %v, want %v", nextCalled, tc.wantNext)
			}
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantNext {
				return
			}
			body, _ := io.ReadAll(rec.Body)
			if string(body) != tc.wantBody {
				t.Fatalf("body = %q, want %q", body, tc.wantBody)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("content-type = %q, want application/json", ct)
			}
		})
	}
}
