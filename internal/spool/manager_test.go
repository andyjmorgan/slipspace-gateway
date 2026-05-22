package spool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewManager_RejectsEmptyRoot(t *testing.T) {
	_, err := NewManager("")
	if err == nil {
		t.Error("expected error for empty root")
	}
}

func TestNewManager_CreatesAllSubdirs(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(filepath.Join(root, "child"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	for _, d := range []string{m.ActiveDir(), m.SealedDir(), m.UploadingDir(), m.DeadletterDir(), m.QuarantineDir()} {
		info, err := os.Stat(d)
		if err != nil {
			t.Errorf("missing %s: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a dir", d)
		}
	}
	if m.Root() != filepath.Join(root, "child") {
		t.Errorf("Root = %q", m.Root())
	}
}

func TestNewManager_MkdirFailure(t *testing.T) {
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewManager(filepath.Join(blocker, "spool"))
	if err == nil {
		t.Error("expected mkdir failure")
	}
}

func TestManager_SealClaimComplete(t *testing.T) {
	m := mustManager(t)
	active := writeSegmentFile(t, m.ActiveDir(), "1-1.ndjson.zst", "body")

	sealed, err := m.Seal(active)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if filepath.Dir(sealed) != m.SealedDir() {
		t.Errorf("sealed not in SealedDir: %q", sealed)
	}
	if _, err := os.Stat(active); !os.IsNotExist(err) {
		t.Errorf("active should be gone: %v", err)
	}

	uploading, err := m.Claim(sealed)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if filepath.Dir(uploading) != m.UploadingDir() {
		t.Errorf("uploading not in UploadingDir: %q", uploading)
	}

	if err := m.Complete(uploading); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := os.Stat(uploading); !os.IsNotExist(err) {
		t.Errorf("uploading should be gone: %v", err)
	}
}

func TestManager_Deadletter(t *testing.T) {
	m := mustManager(t)
	uploading := writeSegmentFile(t, m.UploadingDir(), "2-1.ndjson.zst", "body")
	dl, err := m.Deadletter(uploading)
	if err != nil {
		t.Fatalf("Deadletter: %v", err)
	}
	if filepath.Dir(dl) != m.DeadletterDir() {
		t.Errorf("dl not in DeadletterDir: %q", dl)
	}
}

func TestManager_Quarantine(t *testing.T) {
	m := mustManager(t)
	active := writeSegmentFile(t, m.ActiveDir(), "3-1.ndjson.zst", "body")
	q, err := m.Quarantine(active)
	if err != nil {
		t.Fatalf("Quarantine: %v", err)
	}
	if filepath.Dir(q) != m.QuarantineDir() {
		t.Errorf("q not in QuarantineDir: %q", q)
	}
}

func TestManager_RejectsPathFromWrongState(t *testing.T) {
	m := mustManager(t)
	// Put a file in sealed/ and try to Seal it again.
	sealed := writeSegmentFile(t, m.SealedDir(), "4-1.ndjson.zst", "body")
	_, err := m.Seal(sealed)
	if err == nil || !strings.Contains(err.Error(), "not under") {
		t.Errorf("expected wrong-state error, got %v", err)
	}
}

func TestManager_ListIsChronological(t *testing.T) {
	m := mustManager(t)
	// Write out of order.
	writeSegmentFile(t, m.SealedDir(), "1715000000000000003-3.ndjson.zst", "c")
	writeSegmentFile(t, m.SealedDir(), "1715000000000000001-1.ndjson.zst", "a")
	writeSegmentFile(t, m.SealedDir(), "1715000000000000002-2.ndjson.zst", "b")
	// Plus an unrelated file the listing should ignore.
	writeSegmentFile(t, m.SealedDir(), ".DS_Store", "junk")
	// And a subdirectory.
	if err := os.Mkdir(filepath.Join(m.SealedDir(), "subdir"), 0o750); err != nil {
		t.Fatal(err)
	}

	got, err := m.ListSealed()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("ListSealed = %d entries, want 3: %v", len(got), got)
	}
	want := []string{
		filepath.Join(m.SealedDir(), "1715000000000000001-1.ndjson.zst"),
		filepath.Join(m.SealedDir(), "1715000000000000002-2.ndjson.zst"),
		filepath.Join(m.SealedDir(), "1715000000000000003-3.ndjson.zst"),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManager_ListUploadingAndActive(t *testing.T) {
	m := mustManager(t)
	writeSegmentFile(t, m.UploadingDir(), "1-1.ndjson.zst", "u")
	writeSegmentFile(t, m.ActiveDir(), "2-1.ndjson.zst", "a")

	up, err := m.ListUploading()
	if err != nil || len(up) != 1 {
		t.Errorf("ListUploading: %v %v", err, up)
	}
	act, err := m.ListActive()
	if err != nil || len(act) != 1 {
		t.Errorf("ListActive: %v %v", err, act)
	}
}

func TestManager_ListEmptyDirIsEmpty(t *testing.T) {
	m := mustManager(t)
	out, err := m.ListSealed()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("empty dir should list empty, got %v", out)
	}
}

func TestManager_ListMissingDirErrors(t *testing.T) {
	m := mustManager(t)
	if err := os.RemoveAll(m.SealedDir()); err != nil {
		t.Fatal(err)
	}
	_, err := m.ListSealed()
	if err == nil {
		t.Error("expected error listing missing dir")
	}
}

func TestManager_CompleteIsIdempotent(t *testing.T) {
	m := mustManager(t)
	uploading := writeSegmentFile(t, m.UploadingDir(), "5-1.ndjson.zst", "x")
	if err := m.Complete(uploading); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if err := m.Complete(uploading); err != nil {
		t.Errorf("second Complete: %v", err)
	}
}

func TestManager_CompleteRejectsWrongState(t *testing.T) {
	m := mustManager(t)
	sealed := writeSegmentFile(t, m.SealedDir(), "6-1.ndjson.zst", "x")
	err := m.Complete(sealed)
	if err == nil {
		t.Error("Complete on sealed should error")
	}
}

func TestManager_CompleteOSErrorSurfaces(t *testing.T) {
	// Make uploading/ read-only so os.Remove inside Complete fails with
	// permission-denied (not ErrNotExist). Drives the error-return.
	if os.Getuid() == 0 {
		t.Skip("running as root; chmod won't restrict")
	}
	m := mustManager(t)
	uploading := writeSegmentFile(t, m.UploadingDir(), "7-1.ndjson.zst", "x")
	if err := os.Chmod(m.UploadingDir(), 0o400); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(m.UploadingDir(), 0o700) //nolint:gosec // G302: restore for TempDir cleanup
	})

	err := m.Complete(uploading)
	if err == nil {
		t.Error("expected remove failure surfaced")
	}
}

func TestManager_RenameFailureSurfaces(t *testing.T) {
	m := mustManager(t)
	// Rename src→dst fails when dst already exists on Linux/macOS only
	// for cross-FS or other quirks; the easiest portable failure here
	// is to seal a path that simply doesn't exist.
	_, err := m.Seal(filepath.Join(m.ActiveDir(), "does-not-exist.ndjson.zst"))
	if err == nil {
		t.Error("expected rename failure for missing src")
	}
}

// --- helpers ---

func mustManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func writeSegmentFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}
