package testfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/connector"
)

// Options configures a Connector. Dir is required; all other fields have
// sensible defaults.
type Options struct {
	// Name is the operator-visible connector name (matches YAML in
	// production connectors). Required.
	Name string

	// Dir is the absolute path of the destination root. The connector
	// writes ndjson.zst files under
	//
	//   <Dir>/records/instance=<instance>/date=YYYY-MM-DD/hour=HH/<filename>
	//
	// Required.
	Dir string

	// Clock supplies the wall-clock used for date/hour partitioning.
	// Defaults to time.Now when nil. Tests override to pin partition keys.
	Clock func() time.Time

	// InstanceID is the gateway pod's stable identifier. Stamped into the
	// partition path so concurrent replicas don't collide. Defaults to
	// "local" when empty — only suitable for tests / make dev.
	InstanceID string
}

// Connector copies sealed segments to a local directory tree mirroring
// the partition shape S3 and Azure connectors will write. Implements
// connector.Connector.
type Connector struct {
	name       string
	dir        string
	clock      func() time.Time
	instanceID string
}

// Compile-time interface assertion — keeps the impl honest if the
// interface evolves.
var _ connector.Connector = (*Connector)(nil)

// New constructs a Connector. Returns an error if required fields are
// missing or if Dir cannot be prepared.
func New(opts Options) (*Connector, error) {
	if opts.Name == "" {
		return nil, errors.New("testfs: Name is required")
	}
	if opts.Dir == "" {
		return nil, errors.New("testfs: Dir is required")
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	instanceID := opts.InstanceID
	if instanceID == "" {
		instanceID = "local"
	}
	if err := os.MkdirAll(opts.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("testfs: mkdir %q: %w", opts.Dir, err)
	}
	return &Connector{
		name:       opts.Name,
		dir:        opts.Dir,
		clock:      clock,
		instanceID: instanceID,
	}, nil
}

// Name implements connector.Connector.
func (c *Connector) Name() string { return c.name }

// Type implements connector.Connector. Always "testfs".
func (c *Connector) Type() string { return "testfs" }

// Upload copies the sealed segment file to the destination directory.
// The partition key is computed from the segment's TsMinNs so consumers
// can reason about which records live where without opening files.
func (c *Connector) Upload(ctx context.Context, seg cc.SealedSegment) error {
	if seg.Path == "" {
		return &cc.Permanent{Err: errors.New("testfs: SealedSegment.Path is empty")}
	}
	if err := ctx.Err(); err != nil {
		return &cc.Retryable{Err: fmt.Errorf("context cancelled before upload: %w", err)}
	}

	partition := c.partitionDir(seg)
	if err := os.MkdirAll(partition, 0o750); err != nil {
		return &cc.Retryable{Err: fmt.Errorf("testfs: mkdir partition %q: %w", partition, err)}
	}

	dst := filepath.Join(partition, c.objectName(seg))
	if err := copyFile(ctx, seg.Path, dst); err != nil {
		return &cc.Retryable{Err: fmt.Errorf("testfs: copy to %q: %w", dst, err)}
	}
	return nil
}

func (c *Connector) partitionDir(seg cc.SealedSegment) string {
	var t time.Time
	if seg.TsMinNs > 0 {
		t = time.Unix(0, seg.TsMinNs).UTC()
	} else {
		t = c.clock().UTC()
	}
	return filepath.Join(
		c.dir,
		"records",
		"instance="+c.instanceID,
		"date="+t.Format("2006-01-02"),
		fmt.Sprintf("hour=%02d", t.Hour()),
	)
}

func (c *Connector) objectName(seg cc.SealedSegment) string {
	// Match the design note's S3 object naming: <unix_ns>-<seq>-<delivery_id>.ndjson.zst
	// The segment file on disk already has <unix_ns>-<seq>.ndjson.zst; we
	// stitch the delivery_id in between so the destination object is
	// uniquely named even across retries with the same segment.
	base := filepath.Base(seg.Path)
	const ext = ".ndjson.zst"
	stem := base
	if len(stem) > len(ext) && stem[len(stem)-len(ext):] == ext {
		stem = stem[:len(stem)-len(ext)]
	}
	if seg.DeliveryID != "" {
		return stem + "-" + seg.DeliveryID + ext
	}
	return stem + ext
}

// copyFile is a context-honouring file copy. Streams via io.Copy through
// a ctx-aware reader so cancellation is honoured between chunks. O_EXCL
// on dst refuses to silently overwrite, but Upload wraps the failure in
// *cc.Retryable, so a re-delivered segment retries on the normal schedule
// and deadletters after MaxAttempts rather than failing fast as a
// permanent error; the spooler should be deleting on success.
func copyFile(ctx context.Context, src, dst string) error {
	// G304: src is a path the spooler produced; the gosec false positive
	// is explicit.
	srcF, err := os.Open(src) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer func() { _ = srcF.Close() }()

	dstF, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: dst composed from partitionDir + objectName, not user input
	if err != nil {
		return fmt.Errorf("create dst: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = dstF.Close()
		}
	}()

	if _, err := io.Copy(dstF, &ctxReader{ctx: ctx, r: srcF}); err != nil {
		return err
	}
	// Sync before Close so a successful Upload truly means the bytes are
	// durable; a subsequent dstF.Close() returning an error after Sync
	// succeeds is essentially impossible on a healthy FS.
	if err := dstF.Sync(); err != nil {
		return fmt.Errorf("fsync dst: %w", err)
	}
	closed = true
	return dstF.Close()
}

// ctxReader wraps an io.Reader to check ctx.Err() before every Read.
// Used to make io.Copy honour cancellation without us re-implementing
// the copy loop.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
