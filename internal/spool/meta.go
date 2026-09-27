package spool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// metaExt is appended to a segment's full filename to name its stats
// sidecar: <unix_ns>-<seq>.ndjson.zst.meta.json. The sidecar does not end
// in segmentExt, so Manager.listSegments never mistakes it for a segment.
const metaExt = ".meta.json"

// segmentMeta is the durable form of SegmentStats, written next to a
// segment when it seals so the uploader can populate
// contracts/connector.SealedSegment after a restart, when the in-memory
// Segment that accumulated the counters is gone. Field names are wire
// stable: the sidecar is read back by a later binary.
type segmentMeta struct {
	// Records is the number of ndjson lines in the segment.
	Records int `json:"records"`

	// BytesUncompressed is the sum of record byte lengths before zstd.
	BytesUncompressed int64 `json:"bytes_uncompressed"`

	// TsMinNs and TsMaxNs bound Record.TsNs across the segment; the
	// connectors partition object keys on TsMinNs.
	TsMinNs int64 `json:"ts_min_ns"`
	TsMaxNs int64 `json:"ts_max_ns"`
}

// metaPath returns the sidecar path for the segment at segmentPath. The
// sidecar always sits in the same state directory as its segment.
func metaPath(segmentPath string) string { return segmentPath + metaExt }

// writeSegmentMeta persists stats as the sidecar for segmentPath. Written
// in the active/ directory immediately before Seal so the two files
// change state together; a crash between the two leaves an orphan
// sidecar in active/, which the next Seal (via Recover) carries along or
// which is simply ignored by every listing.
func writeSegmentMeta(segmentPath string, stats SegmentStats) error {
	body, err := json.Marshal(segmentMeta{
		Records:           stats.Records,
		BytesUncompressed: stats.BytesUncompressed,
		TsMinNs:           stats.TsMinNs,
		TsMaxNs:           stats.TsMaxNs,
	})
	if err != nil {
		return fmt.Errorf("spool: marshal segment meta: %w", err)
	}
	if err := os.WriteFile(metaPath(segmentPath), body, 0o600); err != nil {
		return fmt.Errorf("spool: write segment meta: %w", err)
	}
	return nil
}

// readSegmentMeta loads the sidecar for segmentPath. A missing sidecar
// (a segment sealed by a pre-sidecar binary, or a Recover-sealed
// active/ leftover) returns (zero, false, nil) so the caller falls back
// to its own clock; a present-but-unreadable sidecar returns an error so
// the corruption is logged rather than silently treated as absent.
func readSegmentMeta(segmentPath string) (segmentMeta, bool, error) {
	raw, err := os.ReadFile(metaPath(segmentPath)) //nolint:gosec // G304: path derived from a Manager-owned segment path
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return segmentMeta{}, false, nil
		}
		return segmentMeta{}, false, fmt.Errorf("spool: read segment meta: %w", err)
	}
	var m segmentMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return segmentMeta{}, false, fmt.Errorf("spool: decode segment meta %q: %w", metaPath(segmentPath), err)
	}
	return m, true, nil
}

// moveSegmentMeta carries a sidecar across a state transition alongside
// its segment. A missing sidecar is not an error — the segment may
// predate the sidecar, or have been sealed by Recover.
func moveSegmentMeta(srcSegment, dstSegment string) error {
	// Probe the source first: rename reports ENOENT for a missing
	// destination directory too, and that one is a real failure.
	if _, err := os.Lstat(metaPath(srcSegment)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("spool: stat segment meta: %w", err)
	}
	if err := os.Rename(metaPath(srcSegment), metaPath(dstSegment)); err != nil {
		return fmt.Errorf("spool: move segment meta: %w", err)
	}
	return nil
}

// removeSegmentMeta deletes a sidecar; idempotent like Manager.Complete.
func removeSegmentMeta(segmentPath string) error {
	if err := os.Remove(metaPath(segmentPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("spool: remove segment meta: %w", err)
	}
	return nil
}
