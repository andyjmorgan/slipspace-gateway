package agentroute

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/contracts/advise"
)

func testAdviseRequest() advise.Request {
	return advise.Request{
		Configuration:    "cfg-test",
		Protocol:         "messages",
		Provider:         "anthropic",
		Model:            "big-model-x",
		ConversationID:   "conv-1",
		AgentFamily:      FamilyClaudeCode,
		Entrypoint:       "sdk-cli",
		IsSubagent:       true,
		FirstUserMessage: "summarize the file",
		ToolNames:        []string{"Read"},
	}
}

func TestClient_Advise_HappyPath(t *testing.T) {
	const (
		gatewayID = "gw-test-1"
		secret    = "shared-hmac-secret" //nolint:gosec // test fixture, not a credential
	)

	type captured struct {
		method      string
		contentType string
		gatewayID   string
		signature   string
		body        []byte
	}
	var got captured

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("server: read body: %v", err)
		}
		got = captured{
			method:      r.Method,
			contentType: r.Header.Get("Content-Type"),
			gatewayID:   r.Header.Get("X-Slipspace-Gateway-Id"),
			signature:   r.Header.Get("X-Slipspace-Signature"),
			body:        body,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"switch":true,"model":"cheap-model-a","reason":"trivial task","confidence":0.92}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, gatewayID, []byte(secret), time.Second)
	v, err := c.Advise(context.Background(), testAdviseRequest())
	if err != nil {
		t.Fatalf("Advise: %v", err)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got.contentType)
	}
	if got.gatewayID != gatewayID {
		t.Errorf("X-Slipspace-Gateway-Id = %q, want %q", got.gatewayID, gatewayID)
	}

	// The signature must be the hex HMAC-SHA256 of the exact received body
	// under the shared secret — recomputed here server-side.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(got.body)
	wantSig := hex.EncodeToString(mac.Sum(nil))
	if got.signature != wantSig {
		t.Errorf("X-Slipspace-Signature = %q, want %q", got.signature, wantSig)
	}

	if !v.Switch {
		t.Error("verdict Switch = false, want true")
	}
	if v.Model != "cheap-model-a" {
		t.Errorf("verdict Model = %q, want cheap-model-a", v.Model)
	}
	if v.Reason != "trivial task" {
		t.Errorf("verdict Reason = %q, want %q", v.Reason, "trivial task")
	}
	if v.Confidence != 0.92 {
		t.Errorf("verdict Confidence = %v, want 0.92", v.Confidence)
	}
}

func TestClient_Advise_Errors(t *testing.T) {
	t.Run("non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		c := NewClient(srv.URL, "gw", []byte("s"), time.Second)
		if _, err := c.Advise(context.Background(), testAdviseRequest()); err == nil {
			t.Fatal("Advise on 500 returned nil error, want error")
		}
	})

	t.Run("malformed JSON body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"switch": tru`))
		}))
		defer srv.Close()

		c := NewClient(srv.URL, "gw", []byte("s"), time.Second)
		if _, err := c.Advise(context.Background(), testAdviseRequest()); err == nil {
			t.Fatal("Advise on malformed body returned nil error, want error")
		}
	})

	t.Run("unreachable server", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close() // port is now closed

		c := NewClient(url, "gw", []byte("s"), time.Second)
		if _, err := c.Advise(context.Background(), testAdviseRequest()); err == nil {
			t.Fatal("Advise against closed server returned nil error, want error")
		}
	})

	t.Run("request build fails on malformed endpoint", func(t *testing.T) {
		// A control byte in the URL makes http.NewRequestWithContext reject it
		// before any network I/O.
		c := NewClient("http://\x7f", "gw", []byte("s"), time.Second)
		if _, err := c.Advise(context.Background(), testAdviseRequest()); err == nil {
			t.Fatal("Advise with malformed endpoint returned nil error, want error")
		}
	})

	t.Run("response body read error", func(t *testing.T) {
		// Declaring a Content-Length larger than the bytes written makes the
		// server abort the response mid-body: the client's read fails with an
		// unexpected EOF after the 200 status was already accepted.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "4096")
			_, _ = w.Write([]byte(`{"swi`))
		}))
		defer srv.Close()

		c := NewClient(srv.URL, "gw", []byte("s"), time.Second)
		if _, err := c.Advise(context.Background(), testAdviseRequest()); err == nil {
			t.Fatal("Advise with truncated body returned nil error, want error")
		}
	})
}
