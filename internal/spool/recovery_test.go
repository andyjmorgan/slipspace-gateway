package spool

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
)

func TestRecover_EmptySpoolIsNoOp(t *testing.T) {
	m := mustManager(t)
	rep, err := Recover(m)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rep != (RecoveryReport{}) {
		t.Errorf("expected zero report, got %+v", rep)
	}
}

func TestRecover_MovesUploadingBackToSealed(t *testing.T) {
	m := mustManager(t)
	// A "crashed mid-upload" segment.
	writeSegmentFile(t, m.UploadingDir(), "1-1.ndjson.zst", "crashed")
	writeSegmentFile(t, m.UploadingDir(), "1-2.ndjson.zst", "crashed-too")

	rep, err := Recover(m)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rep.RecoveredFromUploading != 2 {
		t.Errorf("RecoveredFromUploading = %d, want 2", rep.RecoveredFromUploading)
	}

	sealed, err := m.ListSealed()
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != 2 {
		t.Errorf("sealed = %d, want 2: %v", len(sealed), sealed)
	}

	uploading, err := m.ListUploading()
	if err != nil {
		t.Fatal(err)
	}
	if len(uploading) != 0 {
		t.Errorf("uploading should be empty after recovery, got %v", uploading)
	}
}

func TestRecover_ValidatesCleanActiveSegment(t *testing.T) {
	m := mustManager(t)

	// Write + cleanly close a segment in active/.
	openedAt := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	s, err := OpenSegment(m.ActiveDir(), 1, openedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(makeRecord("a", 1, "openai", "chat_completions")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	rep, err := Recover(m)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rep.ValidatedActive != 1 {
		t.Errorf("ValidatedActive = %d, want 1", rep.ValidatedActive)
	}
	if rep.QuarantinedFromActive != 0 {
		t.Errorf("QuarantinedFromActive = %d, want 0", rep.QuarantinedFromActive)
	}

	// File should still be in active/.
	active, _ := m.ListActive()
	if len(active) != 1 {
		t.Errorf("active = %d, want 1", len(active))
	}
}

func TestRecover_QuarantinesTornActiveSegment(t *testing.T) {
	m := mustManager(t)

	// Write a segment + truncate to simulate a torn zstd frame.
	openedAt := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	s, err := OpenSegment(m.ActiveDir(), 1, openedAt)
	if err != nil {
		t.Fatal(err)
	}
	// Write enough records for a real frame to form.
	for i := 0; i < 50; i++ {
		_ = s.Write(makeRecord("rec", 1, "openai", "chat_completions"))
	}
	path := s.Path()
	_ = s.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Truncate to half the file — the zstd reader will fail to decode
	// the final frame.
	if err := os.Truncate(path, info.Size()/2); err != nil {
		t.Fatal(err)
	}

	rep, err := Recover(m)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rep.QuarantinedFromActive != 1 {
		t.Errorf("QuarantinedFromActive = %d, want 1", rep.QuarantinedFromActive)
	}
	if rep.ValidatedActive != 0 {
		t.Errorf("ValidatedActive = %d, want 0", rep.ValidatedActive)
	}

	// File should now live in quarantine/.
	quarantined, err := filepath.Glob(filepath.Join(m.QuarantineDir(), "*.ndjson.zst"))
	if err != nil {
		t.Fatal(err)
	}
	if len(quarantined) != 1 {
		t.Errorf("quarantine = %d, want 1", len(quarantined))
	}
}

func TestRecover_QuarantinesNonZstdFile(t *testing.T) {
	m := mustManager(t)
	// Drop a non-zstd file in active/ with the right extension. NewReader
	// rejects it and we treat that as "torn".
	writeSegmentFile(t, m.ActiveDir(), "junk-1.ndjson.zst", "not a zstd frame at all")

	rep, err := Recover(m)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if rep.QuarantinedFromActive != 1 {
		t.Errorf("QuarantinedFromActive = %d, want 1: %+v", rep.QuarantinedFromActive, rep)
	}
}

func TestRecover_PropagatesListUploadingError(t *testing.T) {
	m := mustManager(t)
	// Force ListUploading to fail by deleting the dir.
	if err := os.RemoveAll(m.UploadingDir()); err != nil {
		t.Fatal(err)
	}
	_, err := Recover(m)
	if err == nil {
		t.Error("expected error from ListUploading")
	}
}

func TestRecover_PropagatesListActiveError(t *testing.T) {
	m := mustManager(t)
	if err := os.RemoveAll(m.ActiveDir()); err != nil {
		t.Fatal(err)
	}
	_, err := Recover(m)
	if err == nil {
		t.Error("expected error from ListActive")
	}
}

func TestRecover_TransitionFailureFromUploadingSurfaces(t *testing.T) {
	// chmod the uploading dir read-only-no-write so rename out of it
	// fails with permission denied. Drives the error-propagation branch.
	if os.Getuid() == 0 {
		t.Skip("running as root; chmod won't restrict")
	}
	m := mustManager(t)
	writeSegmentFile(t, m.UploadingDir(), "1-1.ndjson.zst", "x")

	if err := os.Chmod(m.UploadingDir(), 0o400); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(m.UploadingDir(), 0o700) //nolint:gosec // G302: restore for TempDir cleanup
	})

	_, err := Recover(m)
	if err == nil {
		t.Error("expected transition error from read-only uploading/")
	}
}

func TestRecover_TransitionFailureFromActiveSurfaces(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; chmod won't restrict")
	}
	m := mustManager(t)
	// A non-zstd file in active/, then make active/ read-only so the
	// rename to quarantine/ fails.
	writeSegmentFile(t, m.ActiveDir(), "1-1.ndjson.zst", "garbage not zstd")
	if err := os.Chmod(m.ActiveDir(), 0o400); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(m.ActiveDir(), 0o700) //nolint:gosec // G302: restore for TempDir cleanup
	})

	_, err := Recover(m)
	if err == nil {
		t.Error("expected transition error from read-only active/")
	}
}

func TestValidateZstdFrames_OpenFailureReturnsError(t *testing.T) {
	ok, err := validateZstdFrames("/no/such/file/at/all")
	if err == nil {
		t.Error("expected open error")
	}
	if ok {
		t.Error("ok should be false on open failure")
	}
}

// Smoke test: validate the encoded record matches what we wrote.
func TestRecover_PreservesRecordContentsOnValidatedActive(t *testing.T) {
	m := mustManager(t)
	openedAt := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	s, err := OpenSegment(m.ActiveDir(), 1, openedAt)
	if err != nil {
		t.Fatal(err)
	}
	want := cc.Record{
		V:             1,
		ID:            "verify",
		TsNs:          12345,
		Seq:           1,
		InstanceID:    "i",
		CorrelationID: "c",
		Configuration: "test",
		Provider:      "openai",
		Protocol:      "chat_completions",
		Request:       cc.RequestPart{Method: "POST", Path: "/x"},
		Response:      cc.ResponsePart{Status: 200},
		SchemaVersion: cc.SchemaVersion,
	}
	if err := s.Write(want); err != nil {
		t.Fatal(err)
	}
	path := s.Path()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Recover(m); err != nil {
		t.Fatal(err)
	}

	// We read the file directly here (not via the test helper) so the
	// recovery + downstream-consumer story is integrated.
	got := readSegment(t, path)
	if len(got) != 1 || got[0].ID != "verify" {
		t.Errorf("got %+v", got)
	}
}
