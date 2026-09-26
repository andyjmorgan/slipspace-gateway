package spool

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSegmentMeta_WriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	seg := filepath.Join(dir, "1-1.ndjson.zst")
	stats := SegmentStats{
		Records:           7,
		BytesUncompressed: 4096,
		TsMinNs:           100,
		TsMaxNs:           900,
		OpenedAt:          time.Now(),
	}
	if err := writeSegmentMeta(seg, stats); err != nil {
		t.Fatalf("writeSegmentMeta: %v", err)
	}
	got, ok, err := readSegmentMeta(seg)
	if err != nil || !ok {
		t.Fatalf("readSegmentMeta = (%+v, %v, %v), want present", got, ok, err)
	}
	want := segmentMeta{Records: 7, BytesUncompressed: 4096, TsMinNs: 100, TsMaxNs: 900}
	if got != want {
		t.Errorf("meta = %+v, want %+v", got, want)
	}
	// The sidecar must not look like a segment to the directory lister.
	m, err := NewManager(filepath.Join(dir, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	writeSegmentFile(t, m.SealedDir(), "1-1.ndjson.zst", "x")
	if err := writeSegmentMeta(filepath.Join(m.SealedDir(), "1-1.ndjson.zst"), stats); err != nil {
		t.Fatal(err)
	}
	sealed, err := m.ListSealed()
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != 1 {
		t.Errorf("ListSealed must ignore the sidecar, got %v", sealed)
	}
}

func TestSegmentMeta_MissingIsNotAnError(t *testing.T) {
	got, ok, err := readSegmentMeta(filepath.Join(t.TempDir(), "nope.ndjson.zst"))
	if err != nil {
		t.Fatalf("missing sidecar should not error: %v", err)
	}
	if ok || got != (segmentMeta{}) {
		t.Errorf("missing sidecar = (%+v, %v), want (zero, false)", got, ok)
	}
}

func TestSegmentMeta_CorruptSurfacesError(t *testing.T) {
	seg := filepath.Join(t.TempDir(), "1-1.ndjson.zst")
	if err := os.WriteFile(metaPath(seg), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readSegmentMeta(seg); err == nil {
		t.Error("corrupt sidecar should surface an error so it is logged")
	}
}

func TestSegmentMeta_UnreadableSurfacesError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: file modes do not deny reads")
	}
	seg := filepath.Join(t.TempDir(), "1-1.ndjson.zst")
	if err := os.WriteFile(metaPath(seg), []byte("{}"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readSegmentMeta(seg); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Errorf("unreadable sidecar should surface a non-ENOENT error, got %v", err)
	}
}

func TestSegmentMeta_WriteFailureSurfaces(t *testing.T) {
	// A directory occupying the sidecar path makes WriteFile fail.
	seg := filepath.Join(t.TempDir(), "1-1.ndjson.zst")
	if err := os.Mkdir(metaPath(seg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSegmentMeta(seg, SegmentStats{}); err == nil {
		t.Error("expected write failure")
	}
}

func TestSegmentMeta_MoveAndRemoveTolerateMissing(t *testing.T) {
	dir := t.TempDir()
	if err := moveSegmentMeta(filepath.Join(dir, "a.ndjson.zst"), filepath.Join(dir, "b.ndjson.zst")); err != nil {
		t.Errorf("moving a missing sidecar should be a no-op, got %v", err)
	}
	if err := removeSegmentMeta(filepath.Join(dir, "a.ndjson.zst")); err != nil {
		t.Errorf("removing a missing sidecar should be a no-op, got %v", err)
	}
	// A real one moves and then removes.
	src := filepath.Join(dir, "c.ndjson.zst")
	dst := filepath.Join(dir, "d.ndjson.zst")
	if err := writeSegmentMeta(src, SegmentStats{Records: 1}); err != nil {
		t.Fatal(err)
	}
	if err := moveSegmentMeta(src, dst); err != nil {
		t.Fatalf("moveSegmentMeta: %v", err)
	}
	if _, ok, _ := readSegmentMeta(dst); !ok {
		t.Error("sidecar did not land at the destination")
	}
	if err := removeSegmentMeta(dst); err != nil {
		t.Fatalf("removeSegmentMeta: %v", err)
	}
	if _, err := os.Stat(metaPath(dst)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sidecar should be gone, stat err = %v", err)
	}
}

func TestSegmentMeta_MoveFailureSurfaces(t *testing.T) {
	// Renaming a sidecar onto a path whose parent does not exist fails
	// with something other than ENOENT-on-source.
	dir := t.TempDir()
	src := filepath.Join(dir, "c.ndjson.zst")
	if err := writeSegmentMeta(src, SegmentStats{Records: 1}); err != nil {
		t.Fatal(err)
	}
	if err := moveSegmentMeta(src, filepath.Join(dir, "missing-dir", "d.ndjson.zst")); err == nil {
		t.Error("expected move failure into a missing directory")
	}
}

func TestSegmentMeta_RemoveFailureSurfaces(t *testing.T) {
	// A non-empty directory at the sidecar path makes os.Remove fail with
	// ENOTEMPTY, which is not ErrNotExist.
	seg := filepath.Join(t.TempDir(), "1-1.ndjson.zst")
	if err := os.MkdirAll(filepath.Join(metaPath(seg), "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeSegmentMeta(seg); err == nil {
		t.Error("expected remove failure on a non-empty directory")
	}
}
