package arbiter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	detectv1 "github.com/andyjmorgan/slipspace-gateway/gen/slipspace/detect/v1"
)

func TestDetectorClient_Detect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		out, _ := protojson.Marshal(&detectv1.DetectResponse{
			CorrelationId: "c1",
			Status:        detectv1.Status_STATUS_OK,
			Findings:      []*detectv1.Finding{{Category: "pii.email", Score: 0.9}},
		})
		_, _ = w.Write(out)
	}))
	defer srv.Close()

	c := newDetectorClient(nil)
	resp, err := c.Detect(context.Background(), srv.URL, &detectv1.DetectRequest{
		CorrelationId: "c1",
		Unit:          &detectv1.Unit{Text: "reach me at a@b.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetStatus() != detectv1.Status_STATUS_OK || len(resp.GetFindings()) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.GetFindings()[0].GetCategory() != "pii.email" {
		t.Errorf("category = %q", resp.GetFindings()[0].GetCategory())
	}
}

func TestDetectorClient_Detect_DiscardsUnknownFields(t *testing.T) {
	// A detector that adds an unknown field must not break an older Arbiter (ADR-007).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"STATUS_OK","brand_new_field":42,"findings":[]}`))
	}))
	defer srv.Close()
	if _, err := newDetectorClient(nil).Detect(context.Background(), srv.URL, &detectv1.DetectRequest{}); err != nil {
		t.Fatalf("unknown field should be discarded, got %v", err)
	}
}

func TestDetectorClient_Detect_NonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if _, err := newDetectorClient(nil).Detect(context.Background(), srv.URL, &detectv1.DetectRequest{}); err == nil {
		t.Error("expected error on non-200")
	}
}

func TestDetectorClient_Detect_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	if _, err := newDetectorClient(nil).Detect(context.Background(), srv.URL, &detectv1.DetectRequest{}); err == nil {
		t.Error("expected error on malformed response")
	}
}

type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, errIO }
func (errBody) Close() error             { return nil }

type stubDoer struct {
	resp *http.Response
	err  error
}

func (s stubDoer) Do(*http.Request) (*http.Response, error) { return s.resp, s.err }

func TestDetectorClient_TransportError(t *testing.T) {
	c := newDetectorClient(stubDoer{err: errIO})
	if _, err := c.Detect(context.Background(), "http://x", &detectv1.DetectRequest{}); err == nil {
		t.Error("expected transport error")
	}
}

func TestDetectorClient_ReadError(t *testing.T) {
	c := newDetectorClient(stubDoer{resp: &http.Response{StatusCode: 200, Body: errBody{}}})
	if _, err := c.Detect(context.Background(), "http://x", &detectv1.DetectRequest{}); err == nil {
		t.Error("expected body read error")
	}
}

var errIO = errors.New("io boom")
