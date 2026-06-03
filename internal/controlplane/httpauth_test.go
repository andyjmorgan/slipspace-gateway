package controlplane

import (
	"net/http"
	"net/http/httptest"
	"testing"

	contractsadmin "github.com/andyjmorgan/sluice-gateway/contracts/admin"
)

func TestBasicAuth(t *testing.T) {
	const pw = "s3cret"
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	verify := func(user, pass string) bool {
		return user == contractsadmin.Username && pass == pw
	}
	h := BasicAuth(verify, next)

	call := func(setCreds func(*http.Request)) *httptest.ResponseRecorder {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/api/v1/config/entities", nil)
		if setCreds != nil {
			setCreds(req)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("no header is 401 with no WWW-Authenticate", func(t *testing.T) {
		rec := call(nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("= %d, want 401", rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != "" {
			t.Errorf("WWW-Authenticate = %q, want empty (browser dialog suppression)", got)
		}
		if reached {
			t.Error("handler reached without auth")
		}
	})

	t.Run("wrong username is 401", func(t *testing.T) {
		if rec := call(func(r *http.Request) { r.SetBasicAuth("operator", pw) }); rec.Code != http.StatusUnauthorized {
			t.Fatalf("= %d, want 401", rec.Code)
		}
	})

	t.Run("wrong password is 401", func(t *testing.T) {
		if rec := call(func(r *http.Request) { r.SetBasicAuth(contractsadmin.Username, "wrong") }); rec.Code != http.StatusUnauthorized {
			t.Fatalf("= %d, want 401", rec.Code)
		}
	})

	t.Run("correct credentials pass", func(t *testing.T) {
		rec := call(func(r *http.Request) { r.SetBasicAuth(contractsadmin.Username, pw) })
		if rec.Code != http.StatusOK || !reached {
			t.Fatalf("= %d reached=%v, want 200 + reached", rec.Code, reached)
		}
	})
}

func TestBearerAuth(t *testing.T) {
	const token = "fleet-bootstrap-token"
	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	call := func(h http.Handler, auth string) *httptest.ResponseRecorder {
		reached = false
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/segment", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	h := BearerAuth(token, next)

	t.Run("correct bearer passes", func(t *testing.T) {
		if rec := call(h, "Bearer "+token); rec.Code != http.StatusOK || !reached {
			t.Fatalf("= %d reached=%v, want 200 + reached", rec.Code, reached)
		}
	})
	t.Run("wrong token is 401", func(t *testing.T) {
		if rec := call(h, "Bearer nope"); rec.Code != http.StatusUnauthorized || reached {
			t.Fatalf("= %d reached=%v, want 401", rec.Code, reached)
		}
	})
	t.Run("missing header is 401", func(t *testing.T) {
		if rec := call(h, ""); rec.Code != http.StatusUnauthorized || reached {
			t.Fatalf("= %d reached=%v, want 401", rec.Code, reached)
		}
	})
	t.Run("admin basic creds do not satisfy bearer", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest/segment", nil)
		req.SetBasicAuth("admin", token)
		rec := httptest.NewRecorder()
		reached = false
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || reached {
			t.Fatalf("= %d reached=%v, want 401 (basic auth is not a bearer)", rec.Code, reached)
		}
	})
	t.Run("empty token disables the gate", func(t *testing.T) {
		open := BearerAuth("", next)
		if rec := call(open, ""); rec.Code != http.StatusOK || !reached {
			t.Fatalf("= %d reached=%v, want 200 (empty token = open)", rec.Code, reached)
		}
	})
}
