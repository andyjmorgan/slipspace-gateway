package testfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
)

func TestNew_RequiresName(t *testing.T) {
	_, err := New(Options{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "Name") {
		t.Errorf("expected Name-required error, got %v", err)
	}
}

func TestNew_RequiresDir(t *testing.T) {
	_, err := New(Options{Name: "x"})
	if err == nil || !strings.Contains(err.Error(), "Dir") {
		t.Errorf("expected Dir-required error, got %v", err)
	}
}

func TestNew_MkdirFailure(t *testing.T) {
	// Pointing Dir at a path that can't be created (under a regular file)
	// surfaces a typed error, not a panic.
	parent := t.TempDir()
	file := filepath.Join(parent, "blocker")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := New(Options{Name: "x", Dir: filepath.Join(file, "subdir")})
	if err == nil {
		t.Error("expected mkdir failure")
	}
}

func TestNew_DefaultsClockAndInstance(t *testing.T) {
	c, err := New(Options{Name: "x", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if c.instanceID != "local" {
		t.Errorf("instanceID default = %q, want local", c.instanceID)
	}
	if c.clock == nil {
		t.Error("clock should default to non-nil")
	}
}

func TestConnector_NameAndType(t *testing.T) {
	c, err := New(Options{Name: "refinement-test", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != "refinement-test" {
		t.Errorf("Name = %q", c.Name())
	}
	if c.Type() != "testfs" {
		t.Errorf("Type = %q", c.Type())
	}
}

func TestUpload_HappyPath(t *testing.T) {
	dir := t.TempDir()
	c := newFixedClockConnector(t, dir)

	src := writeFixture(t, t.TempDir(), "1234567890-42.ndjson.zst", []byte("payload-bytes"))

	seg := cc.SealedSegment{
		Path:       src,
		Bytes:      int64(len("payload-bytes")),
		Records:    3,
		TsMinNs:    time.Date(2026, 5, 22, 14, 30, 0, 0, time.UTC).UnixNano(),
		TsMaxNs:    time.Date(2026, 5, 22, 14, 35, 0, 0, time.UTC).UnixNano(),
		DeliveryID: "01HW2C8Z000000000000000000",
		Connector:  "test",
	}

	if err := c.Upload(context.Background(), seg); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	want := filepath.Join(dir, "records", "instance=test-instance", "date=2026-05-22", "hour=14",
		"1234567890-42-01HW2C8Z000000000000000000.ndjson.zst")
	got, err := os.ReadFile(want) //nolint:gosec // test asserts file at controlled path
	if err != nil {
		t.Fatalf("read dst %s: %v", want, err)
	}
	if string(got) != "payload-bytes" {
		t.Errorf("dst contents = %q", got)
	}
}

func TestUpload_FallsBackToClockWhenTsMinNsZero(t *testing.T) {
	dir := t.TempDir()
	pinned := time.Date(2026, 5, 22, 10, 15, 0, 0, time.UTC)
	c := &Connector{
		name:       "test",
		dir:        dir,
		clock:      func() time.Time { return pinned },
		instanceID: "ti",
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	src := writeFixture(t, t.TempDir(), "0-1.ndjson.zst", []byte("body"))

	err := c.Upload(context.Background(), cc.SealedSegment{Path: src, DeliveryID: "did"})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	expected := filepath.Join(dir, "records", "instance=ti", "date=2026-05-22", "hour=10", "0-1-did.ndjson.zst")
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("expected file %s missing: %v", expected, err)
	}
}

func TestUpload_NoDeliveryIDPreservesObjectName(t *testing.T) {
	c := newFixedClockConnector(t, t.TempDir())
	src := writeFixture(t, t.TempDir(), "9-1.ndjson.zst", []byte("x"))
	if err := c.Upload(context.Background(), cc.SealedSegment{
		Path:    src,
		TsMinNs: time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC).UnixNano(),
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(c.dir, "records", "instance=*", "date=*", "hour=*", "9-1.ndjson.zst"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Errorf("expected exactly one matching file, got %v", matches)
	}
}

func TestUpload_EmptyPathIsPermanent(t *testing.T) {
	c := newFixedClockConnector(t, t.TempDir())
	err := c.Upload(context.Background(), cc.SealedSegment{})
	if !cc.IsPermanent(err) {
		t.Errorf("expected Permanent error, got %v", err)
	}
}

func TestUpload_CancelledContextIsRetryable(t *testing.T) {
	c := newFixedClockConnector(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.Upload(ctx, cc.SealedSegment{Path: "/anything"})
	if !cc.IsRetryable(err) {
		t.Errorf("expected Retryable error, got %v", err)
	}
}

func TestUpload_DuplicateUploadRefuses(t *testing.T) {
	// O_EXCL means a second Upload of the same segment with same delivery_id
	// after a successful first one errors out. We classify it as Retryable
	// since it suggests a spooler bug rather than a real condition.
	c := newFixedClockConnector(t, t.TempDir())
	src := writeFixture(t, t.TempDir(), "1-1.ndjson.zst", []byte("body"))
	seg := cc.SealedSegment{
		Path:       src,
		TsMinNs:    time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC).UnixNano(),
		DeliveryID: "did",
	}
	if err := c.Upload(context.Background(), seg); err != nil {
		t.Fatalf("first Upload: %v", err)
	}
	err := c.Upload(context.Background(), seg)
	if !cc.IsRetryable(err) {
		t.Errorf("second Upload should be Retryable, got %v", err)
	}
}

func TestUpload_MissingSourceIsRetryable(t *testing.T) {
	c := newFixedClockConnector(t, t.TempDir())
	err := c.Upload(context.Background(), cc.SealedSegment{
		Path:    "/does/not/exist.ndjson.zst",
		TsMinNs: time.Now().UnixNano(),
	})
	if !cc.IsRetryable(err) {
		t.Errorf("expected Retryable error, got %v", err)
	}
}

func TestUpload_CancellationDuringCopyIsHonoured(t *testing.T) {
	// Trigger ctx cancellation mid-copy by handing a large fixture and an
	// already-cancelled ctx. streamCopy should bail at the first read check.
	c := newFixedClockConnector(t, t.TempDir())
	big := make([]byte, 1<<16)
	src := writeFixture(t, t.TempDir(), "2-1.ndjson.zst", big)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.Upload(ctx, cc.SealedSegment{
		Path:    src,
		TsMinNs: time.Now().UnixNano(),
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) && !cc.IsRetryable(err) {
		t.Errorf("expected Retryable or context.Canceled, got %v", err)
	}
}

func TestUpload_PartitionMkdirFailureIsRetryable(t *testing.T) {
	// Drop a regular file at the partition root, so MkdirAll for the
	// hour=XX dir fails (can't make a dir inside a file).
	dir := t.TempDir()
	blocker := filepath.Join(dir, "records")
	if err := os.WriteFile(blocker, []byte("blocker"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	c := newFixedClockConnector(t, dir)
	src := writeFixture(t, t.TempDir(), "1-1.ndjson.zst", []byte("x"))
	err := c.Upload(context.Background(), cc.SealedSegment{
		Path:    src,
		TsMinNs: time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC).UnixNano(),
	})
	if !cc.IsRetryable(err) {
		t.Errorf("expected Retryable from partition mkdir failure, got %v", err)
	}
}

func TestCtxReader_HonoursCancellation(t *testing.T) {
	// Exercise the ctxReader directly so the cancellation-during-copy
	// branch is covered without flakiness.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &ctxReader{ctx: ctx, r: strings.NewReader("never read")}
	buf := make([]byte, 4)
	n, err := r.Read(buf)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v (n=%d)", err, n)
	}
}

func TestCtxReader_DelegatesWhenLive(t *testing.T) {
	r := &ctxReader{ctx: context.Background(), r: strings.NewReader("abc")}
	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 3 || string(buf[:n]) != "abc" {
		t.Errorf("Read = %q (n=%d)", buf[:n], n)
	}
}

// --- helpers ---

func newFixedClockConnector(t *testing.T, dir string) *Connector {
	t.Helper()
	c, err := New(Options{
		Name:       "test",
		Dir:        dir,
		Clock:      func() time.Time { return time.Date(2026, 5, 22, 14, 30, 0, 0, time.UTC) },
		InstanceID: "test-instance",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func writeFixture(t *testing.T, dir, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return p
}
