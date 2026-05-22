package spool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// State subdirectories under a connector's spool root. Names match the
// design note so an operator can `ls` and reason about lifecycle.
const (
	stateActive     = "active"
	stateSealed     = "sealed"
	stateUploading  = "uploading"
	stateDeadletter = "deadletter"
	stateQuarantine = "quarantine"
)

// Manager owns the directory layout for one connector's segments. All
// state transitions are filesystem renames within the same FS, which
// gives us atomic semantics — no DB, no manifest file, no torn state.
//
// Layout:
//
//	<root>/active/      currently-appended segments
//	<root>/sealed/      rotated, awaiting upload
//	<root>/uploading/   claimed by an upload worker (rename target)
//	<root>/deadletter/  upload exhausted; operator decision
//	<root>/quarantine/  corrupt active segment from a torn crash
type Manager struct {
	root string
}

// NewManager constructs a Manager rooted at root. The state subdirs are
// created with 0o750 if missing. Existing contents are left in place —
// recovery is the caller's job (via Recover).
func NewManager(root string) (*Manager, error) {
	if root == "" {
		return nil, errors.New("spool: Manager root is required")
	}
	for _, sub := range []string{stateActive, stateSealed, stateUploading, stateDeadletter, stateQuarantine} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o750); err != nil {
			return nil, fmt.Errorf("spool: mkdir %s/%s: %w", root, sub, err)
		}
	}
	return &Manager{root: root}, nil
}

// Root returns the manager's root path.
func (m *Manager) Root() string { return m.root }

// ActiveDir returns the path holding currently-appended segments.
func (m *Manager) ActiveDir() string { return filepath.Join(m.root, stateActive) }

// SealedDir returns the path holding rotated segments awaiting upload.
func (m *Manager) SealedDir() string { return filepath.Join(m.root, stateSealed) }

// UploadingDir returns the path holding segments claimed by an upload
// worker.
func (m *Manager) UploadingDir() string { return filepath.Join(m.root, stateUploading) }

// DeadletterDir returns the path holding upload-exhausted segments.
func (m *Manager) DeadletterDir() string { return filepath.Join(m.root, stateDeadletter) }

// QuarantineDir returns the path holding corrupt segments found during
// recovery (torn zstd frames from a crash mid-write).
func (m *Manager) QuarantineDir() string { return filepath.Join(m.root, stateQuarantine) }

// Seal moves an active segment file to the sealed directory. activePath
// must live under ActiveDir; passing a path elsewhere is a caller bug
// and returns an error. Returns the new sealed-state path.
func (m *Manager) Seal(activePath string) (string, error) {
	return m.transition(activePath, stateActive, stateSealed)
}

// Claim moves a sealed segment to the uploading directory. Concurrent
// upload workers must serialise via rename's atomic semantics: only one
// rename of a given path succeeds; the rest see ENOENT and skip.
func (m *Manager) Claim(sealedPath string) (string, error) {
	return m.transition(sealedPath, stateSealed, stateUploading)
}

// Complete removes a segment from the uploading directory after a
// successful Upload. Idempotent: if the file is already gone, returns
// nil. uploadingPath must live under UploadingDir.
func (m *Manager) Complete(uploadingPath string) error {
	if err := m.assertUnder(uploadingPath, stateUploading); err != nil {
		return err
	}
	if err := os.Remove(uploadingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("spool: remove %q: %w", uploadingPath, err)
	}
	return nil
}

// Deadletter moves an uploading segment to the deadletter directory
// after retries are exhausted. Returns the new deadletter path.
func (m *Manager) Deadletter(uploadingPath string) (string, error) {
	return m.transition(uploadingPath, stateUploading, stateDeadletter)
}

// Quarantine moves an active segment to the quarantine directory after
// recovery detects a torn frame. Returns the new quarantine path.
func (m *Manager) Quarantine(activePath string) (string, error) {
	return m.transition(activePath, stateActive, stateQuarantine)
}

// ListSealed returns the sealed segment file paths sorted by filename
// (which encodes openedAt + seq, so chronological).
func (m *Manager) ListSealed() ([]string, error) {
	return m.listSegments(m.SealedDir())
}

// ListUploading returns the uploading segment file paths sorted by
// filename. Used by Recover to fish unfinished uploads back to sealed.
func (m *Manager) ListUploading() ([]string, error) {
	return m.listSegments(m.UploadingDir())
}

// ListActive returns the active segment file paths sorted by filename.
// Normally a connector has at most one active segment, but a crash can
// leave more than one behind; Recover handles the cleanup.
func (m *Manager) ListActive() ([]string, error) {
	return m.listSegments(m.ActiveDir())
}

// listSegments returns segment files under dir sorted by name. Non-segment
// files (anything not ending in segmentExt) are ignored — the directory
// might have an OS-injected .DS_Store or similar.
func (m *Manager) listSegments(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("spool: read %q: %w", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < len(segmentExt) || name[len(name)-len(segmentExt):] != segmentExt {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	// Filenames lead with unix_ns-seq so lexical sort = chronological.
	sort.Strings(out)
	return out, nil
}

// transition is the workhorse for state moves. Validates the source
// lives under the expected state subdir, then renames into the
// destination state's directory under the same filename.
func (m *Manager) transition(src, fromState, toState string) (string, error) {
	if err := m.assertUnder(src, fromState); err != nil {
		return "", err
	}
	dst := filepath.Join(m.root, toState, filepath.Base(src))
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("spool: rename %q -> %q: %w", src, dst, err)
	}
	return dst, nil
}

// assertUnder confirms p is a direct child of <root>/<state>/. Guards
// against callers passing in a path from the wrong state subdir which
// would otherwise silently succeed (rename across state subdirs is
// valid syscall-wise but breaks our invariants).
func (m *Manager) assertUnder(p, state string) error {
	expected := filepath.Join(m.root, state)
	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("spool: abs %q: %w", p, err)
	}
	if filepath.Dir(abs) != expected {
		return fmt.Errorf("spool: path %q not under %q (got dir %q)", p, expected, filepath.Dir(abs))
	}
	return nil
}
