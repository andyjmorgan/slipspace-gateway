package bodycapture_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/middleware/bodycapture"
	"github.com/andyjmorgan/sluice-gateway/protocols/openai/chat"
)

func TestRemarshalTyped_NoCaptured(t *testing.T) {
	t.Parallel()
	got, err := bodycapture.RemarshalTyped(context.Background())
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if got != nil {
		t.Errorf("bytes = %q; want nil when no captured body on ctx", string(got))
	}
}

func TestRemarshalTyped_NilBodyPassthrough(t *testing.T) {
	t.Parallel()
	ctx := bodycapture.WithCaptured(context.Background(), bodycapture.Captured{
		Kind: bodycapture.KindPassthrough,
		Raw:  []byte(`{}`),
		Body: nil,
	})
	got, err := bodycapture.RemarshalTyped(ctx)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if got != nil {
		t.Errorf("bytes = %q; want nil for passthrough/no-typed-body", string(got))
	}
}

func TestRemarshalTyped_OpenAIChat_RoundTrip(t *testing.T) {
	t.Parallel()
	req := &chat.ChatCompletionRequest{}
	if err := req.UnmarshalJSON([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)); err != nil {
		t.Fatalf("unmarshal seed: %v", err)
	}

	ctx := bodycapture.WithCaptured(context.Background(), bodycapture.Captured{
		Kind: bodycapture.KindChat,
		Body: req,
	})
	got, err := bodycapture.RemarshalTyped(ctx)
	if err != nil {
		t.Fatalf("RemarshalTyped: %v", err)
	}
	if !strings.Contains(string(got), `"model":"gpt-4o"`) || !strings.Contains(string(got), `"role":"user"`) {
		t.Errorf("re-marshalled bytes missing expected fields: %s", string(got))
	}
}

func TestRemarshalTyped_Idempotent(t *testing.T) {
	t.Parallel()
	req := &chat.ChatCompletionRequest{}
	if err := req.UnmarshalJSON([]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)); err != nil {
		t.Fatalf("unmarshal seed: %v", err)
	}
	ctx := bodycapture.WithCaptured(context.Background(), bodycapture.Captured{Body: req})

	a, err := bodycapture.RemarshalTyped(ctx)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := bodycapture.RemarshalTyped(ctx)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("non-idempotent: %q vs %q", string(a), string(b))
	}
}

func TestRemarshalTyped_NilContext(t *testing.T) {
	t.Parallel()
	got, err := bodycapture.RemarshalTyped(nil) //nolint:staticcheck // exercising the nil-ctx branch
	if err != nil {
		t.Fatalf("err = %v; want nil for nil ctx (FromContext returns false)", err)
	}
	if got != nil {
		t.Errorf("bytes = %q; want nil", string(got))
	}
}

func TestApplyBodyBytes_ReplacesBodyAndContentLength(t *testing.T) {
	t.Parallel()
	r, _ := http.NewRequest(http.MethodPost, "/x", strings.NewReader("old"))
	r.ContentLength = 3

	newBody := []byte(`{"updated":true}`)
	bodycapture.ApplyBodyBytes(r, newBody)

	if r.ContentLength != int64(len(newBody)) {
		t.Errorf("ContentLength = %d; want %d", r.ContentLength, len(newBody))
	}
	if got := r.Header.Get("Content-Length"); got != "16" {
		t.Errorf("Content-Length header = %q; want 16", got)
	}
	gotBody, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(gotBody) != string(newBody) {
		t.Errorf("body = %q; want %q", string(gotBody), string(newBody))
	}
}

func TestApplyBodyBytes_EmptyBytes(t *testing.T) {
	t.Parallel()
	r, _ := http.NewRequest(http.MethodPost, "/x", strings.NewReader("old"))
	bodycapture.ApplyBodyBytes(r, nil)

	if r.ContentLength != 0 {
		t.Errorf("ContentLength = %d; want 0", r.ContentLength)
	}
	if got := r.Header.Get("Content-Length"); got != "0" {
		t.Errorf("Content-Length header = %q; want 0", got)
	}
	gotBody, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(gotBody) != 0 {
		t.Errorf("body = %q; want empty", string(gotBody))
	}
}

func TestApplyBodyBytes_Idempotent(t *testing.T) {
	t.Parallel()
	r, _ := http.NewRequest(http.MethodPost, "/x", strings.NewReader("old"))

	bytes := []byte("hello")
	bodycapture.ApplyBodyBytes(r, bytes)
	bodycapture.ApplyBodyBytes(r, bytes)

	if r.ContentLength != 5 {
		t.Errorf("ContentLength = %d; want 5", r.ContentLength)
	}
	gotBody, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(gotBody) != "hello" {
		t.Errorf("body = %q; want hello", string(gotBody))
	}
}
