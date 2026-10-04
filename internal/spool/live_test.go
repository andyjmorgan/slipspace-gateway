package spool

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These pin the live-connector contract (#567): tracks come and go while
// the spool is running, records enqueued once RegisterTrack returns are
// routed, and UnregisterTrack leaves the on-disk backlog for the next
// track of the same name to recover.

func landed(destDir string) int {
	matches, _ := filepath.Glob(filepath.Join(destDir, "records", "instance=*", "date=*", "hour=*", "*.ndjson.zst"))
	return len(matches)
}

func segmentsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatalf("readdir %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".zst" {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestRegisterTrack_AfterStart_RoutesRecords(t *testing.T) {
	s := newTestSpool(t)
	startStop(t, s) // zero tracks at Start

	destDir := t.TempDir()
	mustRegister(t, s, RegisterTrackOptions{
		Connector:          newTestfs(t, "late", destDir),
		Rotation:           RotationOpts{MaxBytes: 1 << 16, MaxAge: 50 * time.Millisecond},
		UploadPollInterval: 20 * time.Millisecond,
	})
	if got := s.TrackNames(); len(got) != 1 || got[0] != "late" {
		t.Fatalf("TrackNames = %v, want [late]", got)
	}

	s.Enqueue(makeTestRecord("r", 1), "late")
	if !waitFor(3*time.Second, func() bool { return landed(destDir) >= 1 }) {
		t.Fatalf("record enqueued after live RegisterTrack never reached the destination; stats=%+v", s.Stats().Tracks["late"])
	}
	if got := s.Stats().Unrouted["late"]; got != 0 {
		t.Errorf("Unrouted[late] = %d, want 0", got)
	}
}

func TestRegisterTrack_AfterStart_RecoversExistingBacklog(t *testing.T) {
	root := t.TempDir()
	s := mustSpool(t, Options{Root: root, Logger: discardLogger()})
	startStop(t, s)

	// Seed an orphan in uploading/ as a crashed predecessor would have
	// left it. Live registration must run Recover exactly like Start.
	mgr, err := NewManager(filepath.Join(root, "records", "orphaned"))
	if err != nil {
		t.Fatal(err)
	}
	seedSegment(t, mgr.UploadingDir(), 1, makeTestRecord("orphan", 1))

	destDir := t.TempDir()
	mustRegister(t, s, RegisterTrackOptions{
		Connector:          newTestfs(t, "orphaned", destDir),
		UploadPollInterval: 20 * time.Millisecond,
	})
	if !waitFor(3*time.Second, func() bool { return landed(destDir) >= 1 }) {
		t.Fatalf("orphaned segment was not recovered + shipped by live RegisterTrack; uploading=%v sealed=%v",
			segmentsIn(t, mgr.UploadingDir()), segmentsIn(t, mgr.SealedDir()))
	}
}

func TestUnregisterTrack_UnknownName(t *testing.T) {
	s := newTestSpool(t)
	err := s.UnregisterTrack("nope", time.Second)
	if !errors.Is(err, ErrUnknownTrack) {
		t.Fatalf("err = %v, want ErrUnknownTrack", err)
	}
}

func TestUnregisterTrack_BeforeStart(t *testing.T) {
	s := newTestSpool(t)
	mustRegister(t, s, RegisterTrackOptions{Connector: newTestfs(t, "early", t.TempDir())})
	if err := s.UnregisterTrack("early", time.Second); err != nil {
		t.Fatalf("UnregisterTrack before Start: %v", err)
	}
	if got := s.TrackNames(); len(got) != 0 {
		t.Errorf("TrackNames = %v, want empty", got)
	}
	// Re-registering the same name is legal once unregistered.
	mustRegister(t, s, RegisterTrackOptions{Connector: newTestfs(t, "early", t.TempDir())})
}

// TestUnregisterTrack_ThenEnqueueCountsUnrouted: once a track is gone its
// name is unrouted, and the drop is counted rather than silent.
func TestUnregisterTrack_ThenEnqueueCountsUnrouted(t *testing.T) {
	s := newTestSpool(t)
	mustRegister(t, s, RegisterTrackOptions{Connector: newTestfs(t, "gone", t.TempDir())})
	startStop(t, s)
	if err := s.UnregisterTrack("gone", 2*time.Second); err != nil {
		t.Fatalf("UnregisterTrack: %v", err)
	}
	s.Enqueue(makeTestRecord("x", 1), "gone")
	if got := s.Stats().Unrouted["gone"]; got != 1 {
		t.Errorf("Unrouted[gone] = %d, want 1", got)
	}
}

// TestUnregisterTrack_EditKeepsBacklogForReplacement is the live-edit
// flow: the old track (a destination that keeps failing) is unregistered
// mid-retry, its segment stays on disk, and a replacement track under the
// same name recovers and ships it. Nothing is lost across the swap.
func TestUnregisterTrack_EditKeepsBacklogForReplacement(t *testing.T) {
	root := t.TempDir()
	s := mustSpool(t, Options{Root: root, Logger: discardLogger()})
	startStop(t, s)

	broken := &namedFake{name: "edit-me", failWith: errors.New("destination down")}
	mustRegister(t, s, RegisterTrackOptions{
		Connector:          broken,
		Rotation:           RotationOpts{MaxBytes: 1 << 16, MaxAge: 30 * time.Millisecond},
		UploadPollInterval: 20 * time.Millisecond,
		// A long backoff parks the uploader in its retry sleep with the
		// segment claimed in uploading/ — the shape UnregisterTrack must
		// leave recoverable.
		Retry: RetryOpts{BaseBackoff: time.Hour, MaxBackoff: time.Hour, Multiplier: 1, MaxAttempts: 5},
	})
	s.Enqueue(makeTestRecord("keep", 1), "edit-me")
	if !waitFor(3*time.Second, func() bool { return broken.Calls() >= 1 }) {
		t.Fatal("old destination never saw an upload attempt")
	}

	if err := s.UnregisterTrack("edit-me", 2*time.Second); err != nil {
		t.Fatalf("UnregisterTrack: %v", err)
	}
	mgr, err := NewManager(filepath.Join(root, "records", "edit-me"))
	if err != nil {
		t.Fatal(err)
	}
	onDisk := len(segmentsIn(t, mgr.UploadingDir())) + len(segmentsIn(t, mgr.SealedDir()))
	if onDisk != 1 {
		t.Fatalf("segments on disk after unregister = %d, want 1 (uploading=%v sealed=%v)",
			onDisk, segmentsIn(t, mgr.UploadingDir()), segmentsIn(t, mgr.SealedDir()))
	}
	if got := s.Stats().Tracks["edit-me"]; got != (TrackStats{}) {
		t.Errorf("unregistered track still reported in Stats: %+v", got)
	}

	destDir := t.TempDir()
	mustRegister(t, s, RegisterTrackOptions{
		Connector:          newTestfs(t, "edit-me", destDir),
		UploadPollInterval: 20 * time.Millisecond,
	})
	if !waitFor(3*time.Second, func() bool { return landed(destDir) >= 1 }) {
		t.Fatalf("replacement track did not recover + ship the predecessor's segment; stats=%+v", s.Stats().Tracks["edit-me"])
	}
}

// TestUnregisterTrack_AbortsWedgedUpload: an Upload that ignores stopCh
// (only ctx cancels it) must not hold UnregisterTrack past its deadline —
// the abort path cancels the track's context and the second join wins.
func TestUnregisterTrack_AbortsWedgedUpload(t *testing.T) {
	s := newTestSpool(t)
	startStop(t, s)

	wedged := &namedFake{name: "wedged", failWith: errors.New("never"), failureBackoff: time.Hour}
	mustRegister(t, s, RegisterTrackOptions{
		Connector:          wedged,
		Rotation:           RotationOpts{MaxBytes: 1 << 16, MaxAge: 30 * time.Millisecond},
		UploadPollInterval: 20 * time.Millisecond,
	})
	s.Enqueue(makeTestRecord("w", 1), "wedged")
	if !waitFor(3*time.Second, func() bool { return wedged.Calls() >= 1 }) {
		t.Fatal("upload never started")
	}

	start := time.Now()
	if err := s.UnregisterTrack("wedged", 100*time.Millisecond); err != nil {
		t.Fatalf("UnregisterTrack: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("UnregisterTrack took %v; the abort path should bound it near 2x timeout", elapsed)
	}
}

func TestBreakerState_String(t *testing.T) {
	cases := map[BreakerState]string{
		BreakerClosed:   "closed",
		BreakerHalfOpen: "half_open",
		BreakerOpen:     "open",
		BreakerState(9): "unknown",
	}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Errorf("BreakerState(%d).String() = %q, want %q", int(state), got, want)
		}
	}
}
