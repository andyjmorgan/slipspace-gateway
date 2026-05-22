package spool

import (
	"fmt"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
)

// RecoveryReport summarises the outcome of a Recover pass.
type RecoveryReport struct {
	// RecoveredFromUploading is the count of files moved from uploading/
	// back to sealed/ — a crash mid-upload looks like an unclaimed
	// segment from the next process's POV.
	RecoveredFromUploading int

	// ValidatedActive is the count of active/ segments that decoded
	// cleanly (a full crash-time fsync wasn't necessary; the previous
	// process flushed before exit).
	ValidatedActive int

	// QuarantinedFromActive is the count of active/ segments that
	// failed frame validation — these were partial writes from a
	// process crash, moved to quarantine/ for operator inspection.
	QuarantinedFromActive int
}

// Recover restores spool invariants after a process restart:
//
//  1. Anything in uploading/ moves back to sealed/. The previous process
//     was mid-upload; the new process retries from scratch. Idempotency
//     on the destination side is provided by SealedSegment.DeliveryID
//     which the caller persists in a sidecar — out of scope for this PR.
//
//  2. Anything in active/ is validated by attempting to decode the
//     zstd stream end-to-end. Good frames stay (the next process will
//     either resume appending or seal+rotate them on first write).
//     Bad frames move to quarantine/ — they're not retryable as-is and
//     replaying them would corrupt the spool consumer's input.
//
// Recover is safe to call on a fresh spool — empty directories are
// silently zero work.
func Recover(m *Manager) (RecoveryReport, error) {
	var rep RecoveryReport

	uploading, err := m.ListUploading()
	if err != nil {
		return rep, fmt.Errorf("spool: list uploading: %w", err)
	}
	for _, p := range uploading {
		if _, err := m.transition(p, stateUploading, stateSealed); err != nil {
			return rep, fmt.Errorf("spool: move %q to sealed: %w", p, err)
		}
		rep.RecoveredFromUploading++
	}

	active, err := m.ListActive()
	if err != nil {
		return rep, fmt.Errorf("spool: list active: %w", err)
	}
	for _, p := range active {
		valid, vErr := validateZstdFrames(p)
		if vErr != nil {
			return rep, fmt.Errorf("spool: validate %q: %w", p, vErr)
		}
		if valid {
			rep.ValidatedActive++
			continue
		}
		if _, err := m.transition(p, stateActive, stateQuarantine); err != nil {
			return rep, fmt.Errorf("spool: quarantine %q: %w", p, err)
		}
		rep.QuarantinedFromActive++
	}

	return rep, nil
}

// validateZstdFrames opens the file and decodes the zstd stream to
// completion. Returns (true, nil) on clean decode, (false, nil) on a
// truncated/torn frame (the common crash case), and (false, err) only
// for OS-level failures opening the file.
func validateZstdFrames(path string) (bool, error) {
	// G304: path comes from a Manager-owned directory listing; explicit
	// allow.
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return false, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()

	dec, err := zstd.NewReader(f)
	if err != nil {
		// NewReader failure on a non-empty stream is treated as a torn
		// frame — that's what we expect on a crashed mid-write.
		return false, nil
	}
	defer dec.Close()

	// Drain to EOF. A torn frame surfaces here as an unexpected EOF.
	if _, err := io.Copy(io.Discard, dec); err != nil {
		return false, nil
	}
	return true, nil
}
