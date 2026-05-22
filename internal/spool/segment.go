package spool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
)

// zstdNewWriter wraps zstd.NewWriter so tests can substitute a failing
// encoder. The package variable indirection is the cheapest way to make
// the otherwise-infallible defensive branch testable.
var zstdNewWriter = func(w *os.File) (*zstd.Encoder, error) {
	return zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedFastest))
}

// segmentExt is the on-disk extension every spool segment carries.
const segmentExt = ".ndjson.zst"

// Segment is one currently-being-appended ndjson.zst file. Records are
// JSON-encoded one per line and streamed through a single zstd encoder
// so the dictionary builds across records — that's where the bulk of
// the compression ratio comes from.
//
// A Segment is not safe for concurrent Write calls. The owning writer
// goroutine serialises access. ShouldRotate and Stats are safe to call
// concurrently with the writer (atomic-ish counters under the encoder
// mutex).
type Segment struct {
	// path is the absolute path to the active file on disk.
	path string

	// seq is the per-instance monotonic counter encoded in the filename.
	seq uint64

	// openedAt is wall-clock when the segment was opened. Used by
	// ShouldRotate's age check.
	openedAt time.Time

	mu sync.Mutex

	// file is the raw OS file handle. The encoder writes into it; we
	// fsync via file.Sync() before Close so a successful rotation means
	// the bytes are durable.
	file *os.File

	// enc is the streaming zstd encoder wrapping file. Closing enc
	// flushes the final frame.
	enc *zstd.Encoder

	// closed is set by Close so a re-entrant Close is a no-op. Subsequent
	// Write calls return ErrSegmentClosed.
	closed bool

	// records counts records successfully Encode'd into this segment.
	records int

	// bytesUncompressed sums the byte lengths of every record (json + \n)
	// before zstd. Reported via Stats for the manifest's
	// bytes_uncompressed field.
	bytesUncompressed int64

	// tsMinNs and tsMaxNs track the per-segment range of Record.TsNs.
	// Reported via Stats; populated by Write.
	tsMinNs int64
	tsMaxNs int64
}

// ErrSegmentClosed is returned by Write after Close has been called.
var ErrSegmentClosed = errors.New("spool: segment closed")

// OpenSegment creates a new active segment file under dir. The filename
// follows the design-note pattern:
//
//	<openedAt.UnixNano()>-<seq>.ndjson.zst
//
// dir must already exist (the Manager handles that). seq is generated
// by the caller via the meta/sequence counter.
func OpenSegment(dir string, seq uint64, openedAt time.Time) (*Segment, error) {
	name := segmentFilename(openedAt, seq)
	path := filepath.Join(dir, name)

	// O_EXCL because the seq counter is monotonic — a collision here
	// means seq was reused, which is a bug in the caller. Better to
	// surface than silently overwrite.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: path composed by Manager from segmentFilename, not user input
	if err != nil {
		return nil, fmt.Errorf("spool: open active segment %q: %w", path, err)
	}
	// Defensive — zstd.NewWriter with WithEncoderLevel is infallible in
	// current klauspost/compress, but a future version could change.
	// Tests substitute zstdNewWriter to exercise this path.
	enc, err := zstdNewWriter(f)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("spool: zstd encoder: %w", err)
	}
	return &Segment{
		path:     path,
		seq:      seq,
		openedAt: openedAt,
		file:     f,
		enc:      enc,
	}, nil
}

// Path returns the absolute path to the segment file on disk.
func (s *Segment) Path() string { return s.path }

// Seq returns the per-instance sequence number this segment was opened
// with.
func (s *Segment) Seq() uint64 { return s.seq }

// Write JSON-encodes rec as one ndjson line and streams it through the
// encoder. Updates counters for ShouldRotate / Stats. Records that fail
// to JSON-encode are not written — that should be impossible for the
// concrete Record type, but the error is returned anyway for the rare
// case of a body containing non-UTF8 invalid JSON.
func (s *Segment) Write(rec cc.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrSegmentClosed
	}

	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("spool: marshal record id=%s: %w", rec.ID, err)
	}
	// Append the ndjson \n.
	line = append(line, '\n')

	if _, err := s.enc.Write(line); err != nil {
		return fmt.Errorf("spool: encoder write: %w", err)
	}

	s.records++
	s.bytesUncompressed += int64(len(line))
	if s.tsMinNs == 0 || rec.TsNs < s.tsMinNs {
		s.tsMinNs = rec.TsNs
	}
	if rec.TsNs > s.tsMaxNs {
		s.tsMaxNs = rec.TsNs
	}
	return nil
}

// Close flushes the zstd encoder, fsyncs the file, and closes it. After
// Close, Path's file is a valid, complete zstd-framed ndjson file.
// Close is idempotent — second and subsequent calls are no-ops.
//
// All three closing steps run regardless of intermediate failures; the
// returned error joins any that fired so the caller sees everything that
// went wrong, not just the first failure.
func (s *Segment) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return errors.Join(s.enc.Close(), s.file.Sync(), s.file.Close())
}

// ShouldRotate reports whether the segment has hit its rotation
// triggers. Either max_bytes (uncompressed) or max_age fires rotation;
// design note settled on uncompressed because compressed size is hard
// to predict and the operator-facing intent is "limit the batch's data
// volume".
func (s *Segment) ShouldRotate(maxBytes int64, maxAge time.Duration, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxBytes > 0 && s.bytesUncompressed >= maxBytes {
		return true
	}
	if maxAge > 0 && now.Sub(s.openedAt) >= maxAge {
		return true
	}
	return false
}

// Stats returns the segment's accumulated counters. Safe to call
// concurrently with Write.
type Stats struct {
	Records           int
	BytesUncompressed int64
	TsMinNs           int64
	TsMaxNs           int64
	OpenedAt          time.Time
}

// Stats returns a snapshot of the segment's counters.
func (s *Segment) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		Records:           s.records,
		BytesUncompressed: s.bytesUncompressed,
		TsMinNs:           s.tsMinNs,
		TsMaxNs:           s.tsMaxNs,
		OpenedAt:          s.openedAt,
	}
}

// segmentFilename produces the on-disk name <unix_ns>-<seq>.ndjson.zst.
// Exposed unexported so tests can compare without re-implementing.
func segmentFilename(openedAt time.Time, seq uint64) string {
	return fmt.Sprintf("%d-%d%s", openedAt.UnixNano(), seq, segmentExt)
}
