package azureblob

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
)

// fakeUploader captures UploadStream calls. Tests program the response.
type fakeUploader struct {
	mu      sync.Mutex
	calls   []uploadCall
	respond func(uploadCall) (azblob.UploadStreamResponse, error)
}

type uploadCall struct {
	Container string
	Blob      string
	Body      []byte
}

func (f *fakeUploader) UploadStream(_ context.Context, container, blob string, body io.Reader, _ *azblob.UploadStreamOptions) (azblob.UploadStreamResponse, error) {
	b, _ := io.ReadAll(body)
	call := uploadCall{Container: container, Blob: blob, Body: b}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if f.respond != nil {
		return f.respond(call)
	}
	return azblob.UploadStreamResponse{}, nil
}

func (f *fakeUploader) lastCall(t *testing.T) uploadCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("expected at least one UploadStream call")
	}
	return f.calls[len(f.calls)-1]
}

// ---------- New ----------

func TestNew_RejectsWrongType(t *testing.T) {
	_, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{Type: "s3", Name: "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "want azure_blob") {
		t.Errorf("expected wrong-type error, got %v", err)
	}
}

func TestNew_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		cfg  contractsconfig.Connector
		want string
	}{
		{"no name", contractsconfig.Connector{Type: "azure_blob", Account: "a", Container: "c"}, "Name"},
		{"no account", contractsconfig.Connector{Type: "azure_blob", Name: "x", Container: "c"}, "Account"},
		{"no container", contractsconfig.Connector{Type: "azure_blob", Name: "x", Account: "a"}, "Container"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(context.Background(), Options{Config: tc.cfg, Client: &fakeUploader{}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestNew_DefaultsApplied(t *testing.T) {
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{Type: "azure_blob", Name: "x", Account: "a", Container: "c"},
		Client: &fakeUploader{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.instanceID != "local" {
		t.Errorf("instanceID = %q", c.instanceID)
	}
	if c.clock == nil {
		t.Error("clock should default")
	}
}

// ---------- Name + Type ----------

func TestConnector_NameAndType(t *testing.T) {
	c := mustNewWithClient(t, contractsconfig.Connector{
		Type: "azure_blob", Name: "refine-azure", Account: "a", Container: "c",
	}, &fakeUploader{})
	if c.Name() != "refine-azure" {
		t.Errorf("Name = %q", c.Name())
	}
	if c.Type() != "azure_blob" {
		t.Errorf("Type = %q", c.Type())
	}
}

// ---------- Upload happy path ----------

func TestUpload_PutsCorrectBlob(t *testing.T) {
	fake := &fakeUploader{}
	c := mustNewWithClient(t, contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "audit", Prefix: "production",
	}, fake)
	c.instanceID = "test-instance"
	c.clock = func() time.Time { return time.Date(2026, 5, 22, 14, 30, 0, 0, time.UTC) }

	src := writeFixture(t, "1715000000000000001-42.ndjson.zst", []byte("payload"))
	seg := cc.SealedSegment{
		Path:       src,
		TsMinNs:    time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC).UnixNano(),
		DeliveryID: "deliv-1",
	}
	if err := c.Upload(context.Background(), seg); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	call := fake.lastCall(t)
	if call.Container != "audit" {
		t.Errorf("container = %q", call.Container)
	}
	wantBlob := "production/records/instance=test-instance/date=2026-05-22/hour=14/1715000000000000001-42-deliv-1.ndjson.zst"
	if call.Blob != wantBlob {
		t.Errorf("blob = %q\nwant %q", call.Blob, wantBlob)
	}
	if string(call.Body) != "payload" {
		t.Errorf("body = %q", call.Body)
	}
}

func TestUpload_NoPrefixOmitsLeadingSegment(t *testing.T) {
	fake := &fakeUploader{}
	c := mustNewWithClient(t, contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "c",
	}, fake)
	c.instanceID = "i"
	c.clock = func() time.Time { return time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC) }

	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	_ = c.Upload(context.Background(), cc.SealedSegment{
		Path: src, TsMinNs: time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC).UnixNano(),
	})
	if got := fake.lastCall(t).Blob; !strings.HasPrefix(got, "records/instance=i/") {
		t.Errorf("blob = %q", got)
	}
}

func TestUpload_FallsBackToClockWhenTsMinNsZero(t *testing.T) {
	fake := &fakeUploader{}
	c := mustNewWithClient(t, contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "c",
	}, fake)
	c.instanceID = "i"
	pinned := time.Date(2026, 5, 22, 9, 0, 0, 0, time.UTC)
	c.clock = func() time.Time { return pinned }

	src := writeFixture(t, "0-1.ndjson.zst", []byte("x"))
	_ = c.Upload(context.Background(), cc.SealedSegment{Path: src, DeliveryID: "d"})
	got := fake.lastCall(t).Blob
	if !strings.Contains(got, "date=2026-05-22") || !strings.Contains(got, "hour=09") {
		t.Errorf("blob fallback to clock failed: %q", got)
	}
}

func TestUpload_NoDeliveryIDOmitsSuffix(t *testing.T) {
	fake := &fakeUploader{}
	c := mustNewWithClient(t, contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "c",
	}, fake)
	c.instanceID = "i"
	c.clock = func() time.Time { return time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC) }
	src := writeFixture(t, "9-1.ndjson.zst", []byte("x"))
	_ = c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC).UnixNano()})
	got := fake.lastCall(t).Blob
	if !strings.HasSuffix(got, "/9-1.ndjson.zst") {
		t.Errorf("blob without delivery id = %q", got)
	}
}

// ---------- Upload error classification ----------

func TestUpload_EmptyPathPermanent(t *testing.T) {
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "azure_blob", Name: "x", Account: "a", Container: "c"}, &fakeUploader{})
	err := c.Upload(context.Background(), cc.SealedSegment{})
	if !cc.IsPermanent(err) {
		t.Errorf("expected Permanent, got %v", err)
	}
}

func TestUpload_MissingFilePermanent(t *testing.T) {
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "azure_blob", Name: "x", Account: "a", Container: "c"}, &fakeUploader{})
	err := c.Upload(context.Background(), cc.SealedSegment{Path: "/no/such/file"})
	if !cc.IsPermanent(err) {
		t.Errorf("expected Permanent for missing file, got %v", err)
	}
}

func TestUpload_5xxRetryable(t *testing.T) {
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "azure_blob", Name: "x", Account: "a", Container: "c"},
		&fakeUploader{respond: func(uploadCall) (azblob.UploadStreamResponse, error) {
			return azblob.UploadStreamResponse{}, fakeRespErr(503)
		}})
	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Now().UnixNano()})
	if !cc.IsRetryable(err) {
		t.Errorf("5xx should be Retryable, got %v", err)
	}
}

func TestUpload_429Retryable(t *testing.T) {
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "azure_blob", Name: "x", Account: "a", Container: "c"},
		&fakeUploader{respond: func(uploadCall) (azblob.UploadStreamResponse, error) {
			return azblob.UploadStreamResponse{}, fakeRespErr(429)
		}})
	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Now().UnixNano()})
	if !cc.IsRetryable(err) {
		t.Errorf("429 should be Retryable, got %v", err)
	}
}

func TestUpload_4xxPermanent(t *testing.T) {
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "azure_blob", Name: "x", Account: "a", Container: "c"},
		&fakeUploader{respond: func(uploadCall) (azblob.UploadStreamResponse, error) {
			return azblob.UploadStreamResponse{}, fakeRespErr(403)
		}})
	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Now().UnixNano()})
	if !cc.IsPermanent(err) {
		t.Errorf("403 should be Permanent, got %v", err)
	}
}

func TestUpload_ContextCancelledRetryable(t *testing.T) {
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "azure_blob", Name: "x", Account: "a", Container: "c"},
		&fakeUploader{respond: func(uploadCall) (azblob.UploadStreamResponse, error) {
			return azblob.UploadStreamResponse{}, context.Canceled
		}})
	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Now().UnixNano()})
	if !cc.IsRetryable(err) {
		t.Errorf("ctx.Canceled should be Retryable, got %v", err)
	}
}

func TestUpload_UnknownErrorDefaultsRetryable(t *testing.T) {
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "azure_blob", Name: "x", Account: "a", Container: "c"},
		&fakeUploader{respond: func(uploadCall) (azblob.UploadStreamResponse, error) {
			return azblob.UploadStreamResponse{}, errors.New("unknown")
		}})
	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Now().UnixNano()})
	if !cc.IsRetryable(err) {
		t.Errorf("unknown should default to Retryable, got %v", err)
	}
}

// ---------- SecretLoader ----------

func TestDefaultSecretLoader_Env(t *testing.T) {
	t.Setenv("TEST_AZURE_REF", "value-from-env")
	got, err := DefaultSecretLoader("env:TEST_AZURE_REF")
	if err != nil {
		t.Fatal(err)
	}
	if got != "value-from-env" {
		t.Errorf("got %q", got)
	}
}

func TestDefaultSecretLoader_EnvMissing(t *testing.T) {
	_, err := DefaultSecretLoader("env:THIS_VAR_IS_NOT_SET_PROBABLY_EVER_AZ")
	if err == nil {
		t.Error("expected missing-env error")
	}
}

func TestDefaultSecretLoader_File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secret")
	if err := os.WriteFile(p, []byte("contents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DefaultSecretLoader("file:" + p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "contents" {
		t.Errorf("got %q", got)
	}
}

func TestDefaultSecretLoader_FileMissing(t *testing.T) {
	_, err := DefaultSecretLoader("file:/no/such/file/at/all")
	if err == nil {
		t.Error("expected missing-file error")
	}
}

func TestDefaultSecretLoader_BadPrefix(t *testing.T) {
	_, err := DefaultSecretLoader("plain")
	if err == nil {
		t.Error("expected prefix error")
	}
}

// ---------- buildClient ----------

func TestBuildClient_WorkloadIdentity(t *testing.T) {
	_, err := buildClient(contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "c",
	}, DefaultSecretLoader, "")
	// DefaultAzureCredential constructor doesn't actually try to use
	// any credential until first request; should build cleanly here.
	if err != nil {
		t.Errorf("workload_identity should build: %v", err)
	}
}

func TestBuildClient_SASTokenSuccess(t *testing.T) {
	t.Setenv("AZ_SAS", "sv=2024-08-04&ss=b&srt=co&sp=rwdlacx&se=2030-01-01T00:00:00Z&sig=abc")
	cfg := contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "c",
		Auth: &contractsconfig.ConnectorAuth{ //nolint:gosec // G101: env: indirection, not a literal credential
			Mode: "sas_token", SASTokenRef: "env:AZ_SAS",
		},
	}
	_, err := buildClient(cfg, DefaultSecretLoader, "")
	if err != nil {
		t.Errorf("sas_token build: %v", err)
	}
}

func TestBuildClient_SASTokenMissing(t *testing.T) {
	cfg := contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "c",
		Auth: &contractsconfig.ConnectorAuth{ //nolint:gosec // G101: env: indirection, not a literal credential
			Mode: "sas_token", SASTokenRef: "env:DEFINITELY_NOT_SET_AZ",
		},
	}
	_, err := buildClient(cfg, DefaultSecretLoader, "")
	if err == nil || !strings.Contains(err.Error(), "sas_token_ref") {
		t.Errorf("got %v", err)
	}
}

func TestBuildClient_AccountKeySuccess(t *testing.T) {
	// Azurite's well-known dev key — not a real credential.
	const key = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==" //nolint:gosec // G101: Azurite's documented public default key
	t.Setenv("AZ_KEY", key)
	_, err := buildClient(contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "devstoreaccount1", Container: "c",
		Auth: &contractsconfig.ConnectorAuth{Mode: "account_key", AccountKeyRef: "env:AZ_KEY"},
	}, DefaultSecretLoader, "")
	if err != nil {
		t.Errorf("account_key build: %v", err)
	}
}

func TestBuildClient_AccountKeyMissing(t *testing.T) {
	_, err := buildClient(contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "c",
		Auth: &contractsconfig.ConnectorAuth{Mode: "account_key", AccountKeyRef: "env:DEFINITELY_NOT_SET_AZ_KEY"},
	}, DefaultSecretLoader, "")
	if err == nil || !strings.Contains(err.Error(), "account_key_ref") {
		t.Errorf("got %v", err)
	}
}

func TestBuildClient_AccountKeyBadValue(t *testing.T) {
	// NewSharedKeyCredential validates base64; non-base64 input fails.
	t.Setenv("AZ_BAD_KEY", "not-base-64-content!!!")
	_, err := buildClient(contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "c",
		Auth: &contractsconfig.ConnectorAuth{Mode: "account_key", AccountKeyRef: "env:AZ_BAD_KEY"},
	}, DefaultSecretLoader, "")
	if err == nil || !strings.Contains(err.Error(), "shared key credential") {
		t.Errorf("got %v", err)
	}
}

func TestBuildClient_UnsupportedMode(t *testing.T) {
	_, err := buildClient(contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "c",
		Auth: &contractsconfig.ConnectorAuth{Mode: "carrier-pigeon"},
	}, DefaultSecretLoader, "")
	if err == nil || !strings.Contains(err.Error(), "unsupported auth mode") {
		t.Errorf("got %v", err)
	}
}

func TestBuildClient_ServiceURLOverride(t *testing.T) {
	_, err := buildClient(contractsconfig.Connector{
		Type: "azure_blob", Name: "x", Account: "a", Container: "c",
	}, DefaultSecretLoader, "http://127.0.0.1:10000/devstoreaccount1")
	if err != nil {
		t.Errorf("override should build: %v", err)
	}
}

func TestNew_BuildsClientWhenNil(t *testing.T) {
	// Exercises the Client==nil branch via workload_identity (the
	// credential constructor doesn't try to reach Azure at build time).
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{
			Type: "azure_blob", Name: "x", Account: "a", Container: "c",
		},
	})
	if err != nil {
		t.Fatalf("New with nil Client: %v", err)
	}
	if c == nil {
		t.Fatal("nil Connector")
	}
}

func TestNew_BuildClientFailureSurfaces(t *testing.T) {
	_, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{
			Type: "azure_blob", Name: "x", Account: "a", Container: "c",
			Auth: &contractsconfig.ConnectorAuth{
				Mode: "account_key", AccountKeyRef: "env:DEFINITELY_NOT_SET_NEW_AZ",
			},
		},
	})
	if err == nil {
		t.Error("expected buildClient error to surface from New")
	}
}

func TestClassifyError_3xxFallsThroughToRetryable(t *testing.T) {
	// Defensive: a 3xx response shouldn't normally happen on PUT but
	// the classifyError default is Retryable.
	err := classifyError(fakeRespErr(304))
	if !cc.IsRetryable(err) {
		t.Errorf("3xx default = %v, want Retryable", err)
	}
}

func TestClassifyError_NilIsNil(t *testing.T) {
	if err := classifyError(nil); err != nil {
		t.Errorf("classifyError(nil) = %v", err)
	}
}

// ---------- helpers ----------

func mustNewWithClient(t *testing.T, cfg contractsconfig.Connector, fake uploader) *Connector {
	t.Helper()
	c, err := New(context.Background(), Options{Config: cfg, Client: fake})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func writeFixture(t *testing.T, name string, body []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeRespErr constructs an azcore.ResponseError carrying the given
// HTTP status — drives classifyError's status-based dispatch without a
// live transport.
func fakeRespErr(status int) error {
	return &azcore.ResponseError{
		StatusCode: status,
		RawResponse: &http.Response{
			StatusCode: status,
			Header:     http.Header{},
		},
		ErrorCode: "Synthetic",
	}
}
