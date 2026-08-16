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

	// SealedFromActive is the count of active/ segments that decoded
	// cleanly and were sealed so the uploader ships them. The drain
	// goroutine never resumes a pre-crash active segment (writeRecord
	// always opens a fresh one), so a validated leftover only reaches a
	// destination if recovery seals it here.
	SealedFromActive int

	// QuarantinedFromActive is the count of active/ segments that
	// failed frame validation — these were partial writes from a
	// process crash, moved to quarantine/ for operator inspection.
	QuarantinedFromActive int
}

// Recover restores spool invariants after a process restart:
//
//  1. Anything in uploading/ moves back to sealed/. The previous process
//     was mid-upload; the new process retries from scratch. A segment can
//     therefore be delivered more than once. Deduplication is the
//     destination's job: SealedSegment.DeliveryID is stable across
//     retries, so a connector keys its object name or idempotency header
//     off it. The gateway keeps no delivered-id ledger of its own.
//
//  2. Anything in active/ is validated by attempting to decode the
//     zstd stream end-to-end. Cleanly-decoding segments are sealed so
//     the uploader ships them — the drain goroutine opens a fresh
//     segment on its first write and never resumes a pre-crash active
//     file, so an unsealed leftover would otherwise be stranded
//     forever. Bad frames move to quarantine/ — they're not retryable
//     as-is and replaying them would corrupt the spool consumer's input.
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
			if _, err := m.Seal(p); err != nil {
				return rep, fmt.Errorf("spool: seal %q: %w", p, err)
			}
			rep.SealedFromActive++
			continue
		}
		if _, err := m.Quarantine(p); err != nil {
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
