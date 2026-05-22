package s3

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
)

// fakeS3 captures PutObject calls + lets the test program return values.
type fakeS3 struct {
	mu      sync.Mutex
	calls   []putCall
	respond func(call putCall) (*s3sdk.PutObjectOutput, error)
}

type putCall struct {
	Bucket string
	Key    string
	Body   []byte
}

func (f *fakeS3) PutObject(_ context.Context, in *s3sdk.PutObjectInput, _ ...func(*s3sdk.Options)) (*s3sdk.PutObjectOutput, error) {
	body, _ := io.ReadAll(in.Body)
	call := putCall{
		Bucket: awssdk.ToString(in.Bucket),
		Key:    awssdk.ToString(in.Key),
		Body:   body,
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if f.respond != nil {
		return f.respond(call)
	}
	return &s3sdk.PutObjectOutput{}, nil
}

func (f *fakeS3) lastCall(t *testing.T) putCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("expected at least one PutObject call")
	}
	return f.calls[len(f.calls)-1]
}

// ---------- New ----------

func TestNew_RejectsWrongType(t *testing.T) {
	_, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{Type: "webhook", Name: "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "want s3") {
		t.Errorf("expected wrong-type error, got %v", err)
	}
}

func TestNew_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		cfg  contractsconfig.Connector
		want string
	}{
		{"no name", contractsconfig.Connector{Type: "s3", Bucket: "b", Region: "r"}, "Name"},
		{"no bucket", contractsconfig.Connector{Type: "s3", Name: "x", Region: "r"}, "Bucket"},
		{"no region", contractsconfig.Connector{Type: "s3", Name: "x", Bucket: "b"}, "Region"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(context.Background(), Options{Config: tc.cfg, Client: &fakeS3{}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v", err)
			}
		})
	}
}

func TestNew_DefaultsAppliedWhenClientInjected(t *testing.T) {
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{
			Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
		},
		Client: &fakeS3{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.instanceID != "local" {
		t.Errorf("instanceID default = %q", c.instanceID)
	}
	if c.clock == nil {
		t.Error("clock should default")
	}
}

// ---------- Name + Type ----------

func TestConnector_NameAndType(t *testing.T) {
	c := mustNew(t, contractsconfig.Connector{
		Type: "s3", Name: "refine-s3", Bucket: "b", Region: "r",
	})
	if c.Name() != "refine-s3" {
		t.Errorf("Name = %q", c.Name())
	}
	if c.Type() != "s3" {
		t.Errorf("Type = %q", c.Type())
	}
}

// ---------- Upload happy path ----------

func TestUpload_PutsCorrectObject(t *testing.T) {
	fake := &fakeS3{}
	c := mustNewWithClient(t, contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "my-bucket", Prefix: "production", Region: "r",
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
	if call.Bucket != "my-bucket" {
		t.Errorf("bucket = %q", call.Bucket)
	}
	wantKey := "production/records/instance=test-instance/date=2026-05-22/hour=14/1715000000000000001-42-deliv-1.ndjson.zst"
	if call.Key != wantKey {
		t.Errorf("key = %q\nwant %q", call.Key, wantKey)
	}
	if string(call.Body) != "payload" {
		t.Errorf("body = %q", call.Body)
	}
}

func TestUpload_NoPrefixOmitsLeadingSegment(t *testing.T) {
	fake := &fakeS3{}
	c := mustNewWithClient(t, contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "r",
	}, fake)
	c.instanceID = "i"
	c.clock = func() time.Time { return time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC) }

	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	_ = c.Upload(context.Background(), cc.SealedSegment{
		Path:    src,
		TsMinNs: time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC).UnixNano(),
	})
	if got := fake.lastCall(t).Key; !strings.HasPrefix(got, "records/instance=i/") {
		t.Errorf("key = %q, expected leading 'records/'", got)
	}
}

func TestUpload_FallsBackToClockWhenTsMinNsZero(t *testing.T) {
	fake := &fakeS3{}
	c := mustNewWithClient(t, contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "r",
	}, fake)
	c.instanceID = "i"
	pinned := time.Date(2026, 5, 22, 9, 0, 0, 0, time.UTC)
	c.clock = func() time.Time { return pinned }

	src := writeFixture(t, "0-1.ndjson.zst", []byte("x"))
	_ = c.Upload(context.Background(), cc.SealedSegment{
		Path:       src,
		DeliveryID: "d",
	})
	got := fake.lastCall(t).Key
	if !strings.Contains(got, "date=2026-05-22") || !strings.Contains(got, "hour=09") {
		t.Errorf("key fallback to clock failed: %q", got)
	}
}

func TestUpload_NoDeliveryIDOmitsSuffix(t *testing.T) {
	fake := &fakeS3{}
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "s3", Name: "x", Bucket: "b", Region: "r"}, fake)
	c.instanceID = "i"
	c.clock = func() time.Time { return time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC) }

	src := writeFixture(t, "9-1.ndjson.zst", []byte("x"))
	_ = c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC).UnixNano()})
	got := fake.lastCall(t).Key
	if !strings.HasSuffix(got, "/9-1.ndjson.zst") {
		t.Errorf("key without delivery id = %q", got)
	}
}

// ---------- Upload error classification ----------

func TestUpload_EmptyPathPermanent(t *testing.T) {
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "s3", Name: "x", Bucket: "b", Region: "r"}, &fakeS3{})
	err := c.Upload(context.Background(), cc.SealedSegment{})
	if !cc.IsPermanent(err) {
		t.Errorf("expected Permanent, got %v", err)
	}
}

func TestUpload_MissingFilePermanent(t *testing.T) {
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "s3", Name: "x", Bucket: "b", Region: "r"}, &fakeS3{})
	err := c.Upload(context.Background(), cc.SealedSegment{Path: "/no/such/file"})
	if !cc.IsPermanent(err) {
		t.Errorf("expected Permanent for missing file, got %v", err)
	}
}

func TestUpload_5xxRetryable(t *testing.T) {
	fake := &fakeS3{respond: func(putCall) (*s3sdk.PutObjectOutput, error) {
		return nil, fakeHTTPErr(503)
	}}
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "s3", Name: "x", Bucket: "b", Region: "r"}, fake)
	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Now().UnixNano()})
	if !cc.IsRetryable(err) {
		t.Errorf("5xx should be Retryable, got %v", err)
	}
}

func TestUpload_429Retryable(t *testing.T) {
	fake := &fakeS3{respond: func(putCall) (*s3sdk.PutObjectOutput, error) {
		return nil, fakeHTTPErr(429)
	}}
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "s3", Name: "x", Bucket: "b", Region: "r"}, fake)
	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Now().UnixNano()})
	if !cc.IsRetryable(err) {
		t.Errorf("429 should be Retryable, got %v", err)
	}
}

func TestUpload_4xxPermanent(t *testing.T) {
	fake := &fakeS3{respond: func(putCall) (*s3sdk.PutObjectOutput, error) {
		return nil, fakeHTTPErr(403)
	}}
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "s3", Name: "x", Bucket: "b", Region: "r"}, fake)
	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Now().UnixNano()})
	if !cc.IsPermanent(err) {
		t.Errorf("403 should be Permanent, got %v", err)
	}
}

func TestUpload_ContextCancelledRetryable(t *testing.T) {
	fake := &fakeS3{respond: func(putCall) (*s3sdk.PutObjectOutput, error) {
		return nil, context.Canceled
	}}
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "s3", Name: "x", Bucket: "b", Region: "r"}, fake)
	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Now().UnixNano()})
	if !cc.IsRetryable(err) {
		t.Errorf("ctx.Canceled should be Retryable, got %v", err)
	}
}

func TestUpload_UnexpectedEOFRetryable(t *testing.T) {
	fake := &fakeS3{respond: func(putCall) (*s3sdk.PutObjectOutput, error) {
		return nil, io.ErrUnexpectedEOF
	}}
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "s3", Name: "x", Bucket: "b", Region: "r"}, fake)
	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Now().UnixNano()})
	if !cc.IsRetryable(err) {
		t.Errorf("io.ErrUnexpectedEOF should be Retryable, got %v", err)
	}
}

func TestUpload_UnknownErrorDefaultsRetryable(t *testing.T) {
	fake := &fakeS3{respond: func(putCall) (*s3sdk.PutObjectOutput, error) {
		return nil, errors.New("unknown sdk failure")
	}}
	c := mustNewWithClient(t, contractsconfig.Connector{Type: "s3", Name: "x", Bucket: "b", Region: "r"}, fake)
	src := writeFixture(t, "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, TsMinNs: time.Now().UnixNano()})
	if !cc.IsRetryable(err) {
		t.Errorf("unknown error should default to Retryable, got %v", err)
	}
}

// ---------- SecretLoader ----------

func TestDefaultSecretLoader_Env(t *testing.T) {
	t.Setenv("TEST_S3_SECRET", "value-from-env")
	got, err := DefaultSecretLoader("env:TEST_S3_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	if got != "value-from-env" {
		t.Errorf("got %q", got)
	}
}

func TestDefaultSecretLoader_EnvMissing(t *testing.T) {
	_, err := DefaultSecretLoader("env:THIS_VAR_IS_NOT_SET_PROBABLY_EVER")
	if err == nil {
		t.Error("expected missing-env error")
	}
}

func TestDefaultSecretLoader_File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secret")
	if err := os.WriteFile(p, []byte("file-contents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DefaultSecretLoader("file:" + p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "file-contents" {
		t.Errorf("got %q", got)
	}
}

func TestDefaultSecretLoader_FileMissing(t *testing.T) {
	_, err := DefaultSecretLoader("file:/no/such/file/here")
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

func TestBuildClient_WorkloadIdentityNoRefs(t *testing.T) {
	_, err := buildClient(context.Background(), contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
	}, DefaultSecretLoader)
	if err != nil {
		t.Errorf("workload_identity should build cleanly: %v", err)
	}
}

func TestBuildClient_StaticReadsSecrets(t *testing.T) {
	t.Setenv("AK", "AKIATEST")
	t.Setenv("SK", "SecretValue123")
	_, err := buildClient(context.Background(), contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
		Auth: &contractsconfig.ConnectorAuth{ //nolint:gosec // G101: env: indirection, not a literal credential
			Mode:               contractsconfig.AuthModeStatic,
			AccessKeyIDRef:     "env:AK",
			SecretAccessKeyRef: "env:SK",
		},
	}, DefaultSecretLoader)
	if err != nil {
		t.Errorf("static mode build: %v", err)
	}
}

func TestBuildClient_StaticMissingAccessKey(t *testing.T) {
	_, err := buildClient(context.Background(), contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
		Auth: &contractsconfig.ConnectorAuth{ //nolint:gosec // G101: env: indirection, not a literal credential
			Mode:               contractsconfig.AuthModeStatic,
			AccessKeyIDRef:     "env:DEFINITELY_NOT_SET",
			SecretAccessKeyRef: "env:DEFINITELY_NOT_SET_2",
		},
	}, DefaultSecretLoader)
	if err == nil || !strings.Contains(err.Error(), "access_key_id_ref") {
		t.Errorf("expected access_key_id_ref error, got %v", err)
	}
}

func TestBuildClient_StaticMissingSecretKey(t *testing.T) {
	t.Setenv("AK_OK", "x")
	_, err := buildClient(context.Background(), contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
		Auth: &contractsconfig.ConnectorAuth{ //nolint:gosec // G101: env: indirection, not a literal credential
			Mode:               contractsconfig.AuthModeStatic,
			AccessKeyIDRef:     "env:AK_OK",
			SecretAccessKeyRef: "env:DEFINITELY_NOT_SET_3",
		},
	}, DefaultSecretLoader)
	if err == nil || !strings.Contains(err.Error(), "secret_access_key_ref") {
		t.Errorf("expected secret_access_key_ref error, got %v", err)
	}
}

func TestNew_BuildsClientWhenNil(t *testing.T) {
	// Exercises the Client==nil branch by calling New without injection.
	// workload_identity (default) means buildClient won't hit any
	// missing-credential errors even with no real AWS env set.
	c, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{
			Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
		},
	})
	if err != nil {
		t.Fatalf("New with nil Client should build: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil Connector")
	}
}

func TestNew_BuildClientFailureSurfaces(t *testing.T) {
	// static mode with a missing env ref forces buildClient to error,
	// which New propagates.
	_, err := New(context.Background(), Options{
		Config: contractsconfig.Connector{
			Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
			Auth: &contractsconfig.ConnectorAuth{ //nolint:gosec // G101: env: indirection, not a literal credential
				Mode:               contractsconfig.AuthModeStatic,
				AccessKeyIDRef:     "env:DEFINITELY_NOT_SET_NEW_5",
				SecretAccessKeyRef: "env:DEFINITELY_NOT_SET_NEW_6",
			},
		},
	})
	if err == nil {
		t.Error("expected buildClient error to surface")
	}
}

func TestBuildClient_AssumeRoleWithExternalID(t *testing.T) {
	t.Setenv("EXT_ID_OK", "external-id-value")
	_, err := buildClient(context.Background(), contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
		Auth: &contractsconfig.ConnectorAuth{ //nolint:gosec // G101: env: indirection, not a literal credential
			Mode:          contractsconfig.AuthModeAssumeRole,
			RoleARN:       "arn:aws:iam::1:role/x",
			ExternalIDRef: "env:EXT_ID_OK",
		},
	}, DefaultSecretLoader)
	if err != nil {
		t.Errorf("assume_role with external_id_ref should build: %v", err)
	}
}

func TestBuildClient_AssumeRoleNoExternalID(t *testing.T) {
	_, err := buildClient(context.Background(), contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
		Auth: &contractsconfig.ConnectorAuth{ //nolint:gosec // G101: env: indirection, not a literal credential
			Mode:    contractsconfig.AuthModeAssumeRole,
			RoleARN: "arn:aws:iam::1:role/x",
		},
	}, DefaultSecretLoader)
	if err != nil {
		t.Errorf("assume_role without external_id should build: %v", err)
	}
}

func TestBuildClient_AssumeRoleExternalIDMissing(t *testing.T) {
	_, err := buildClient(context.Background(), contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
		Auth: &contractsconfig.ConnectorAuth{ //nolint:gosec // G101: env: indirection, not a literal credential
			Mode:          contractsconfig.AuthModeAssumeRole,
			RoleARN:       "arn:aws:iam::1:role/x",
			ExternalIDRef: "env:DEFINITELY_NOT_SET_4",
		},
	}, DefaultSecretLoader)
	if err == nil || !strings.Contains(err.Error(), "external_id_ref") {
		t.Errorf("expected external_id_ref error, got %v", err)
	}
}

func TestBuildClient_UnsupportedMode(t *testing.T) {
	_, err := buildClient(context.Background(), contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
		Auth: &contractsconfig.ConnectorAuth{Mode: "carrier-pigeon"},
	}, DefaultSecretLoader)
	if err == nil || !strings.Contains(err.Error(), "unsupported auth mode") {
		t.Errorf("got %v", err)
	}
}

func TestBuildClient_EndpointAndPathStyle(t *testing.T) {
	c, err := buildClient(context.Background(), contractsconfig.Connector{
		Type: "s3", Name: "x", Bucket: "b", Region: "us-east-1",
		EndpointURL:  "https://minio.local:9000",
		UsePathStyle: true,
	}, DefaultSecretLoader)
	if err != nil {
		t.Fatalf("buildClient: %v", err)
	}
	if c == nil {
		t.Fatal("client nil")
	}
}

// ---------- helpers ----------

func mustNew(t *testing.T, cfg contractsconfig.Connector) *Connector {
	t.Helper()
	return mustNewWithClient(t, cfg, &fakeS3{})
}

func mustNewWithClient(t *testing.T, cfg contractsconfig.Connector, fake s3Putter) *Connector {
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

// fakeHTTPErr constructs a smithy http.ResponseError carrying the given
// HTTP status so classifyError's status-based dispatch is exercised
// without needing a live HTTP transport.
func fakeHTTPErr(status int) error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{
			Response: &http.Response{
				StatusCode: status,
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Request:    &http.Request{URL: &url.URL{Scheme: "https", Host: "example", Path: "/x"}},
			},
		},
		Err: errors.New("synthetic"),
	}
}
