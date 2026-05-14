package bus_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/andyjmorgan/sluice-gateway/internal/bus"
)

type stubStreamMgr struct {
	calls       atomic.Int32
	streamCfg   jetstream.StreamConfig
	createErr   error
	tolerateErr bool
}

func (s *stubStreamMgr) CreateStream(_ context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error) {
	s.streamCfg = cfg
	if n := s.calls.Add(1); n > 1 && s.tolerateErr {
		return nil, jetstream.ErrStreamNameAlreadyInUse
	}
	if s.createErr != nil {
		return nil, s.createErr
	}
	return nil, nil
}

type stubObjMgr struct {
	createCalls atomic.Int32
	lookupCalls atomic.Int32
	cfg         jetstream.ObjectStoreConfig
	createErr   error
	lookupErr   error
}

func (s *stubObjMgr) CreateObjectStore(_ context.Context, cfg jetstream.ObjectStoreConfig) (jetstream.ObjectStore, error) {
	s.createCalls.Add(1)
	s.cfg = cfg
	if s.createErr != nil {
		return nil, s.createErr
	}
	return nil, nil
}

func (s *stubObjMgr) ObjectStore(_ context.Context, _ string) (jetstream.ObjectStore, error) {
	s.lookupCalls.Add(1)
	if s.lookupErr != nil {
		return nil, s.lookupErr
	}
	return nil, nil
}

func TestEnsureStream_CreatesWithExpectedConfig(t *testing.T) {
	t.Parallel()

	mgr := &stubStreamMgr{}
	if err := bus.EnsureStream(context.Background(), mgr, bus.DefaultStreamName, []string{"gateway.>"}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	got := mgr.streamCfg
	if got.Name != bus.DefaultStreamName {
		t.Errorf("Name = %q, want %q", got.Name, bus.DefaultStreamName)
	}
	if len(got.Subjects) != 1 || got.Subjects[0] != "gateway.>" {
		t.Errorf("Subjects = %v, want [gateway.>]", got.Subjects)
	}
	if got.Storage != jetstream.FileStorage {
		t.Errorf("Storage = %v, want FileStorage", got.Storage)
	}
	if got.Retention != jetstream.LimitsPolicy {
		t.Errorf("Retention = %v, want LimitsPolicy", got.Retention)
	}
	if got.MaxMsgsPerSubject != 50000 {
		t.Errorf("MaxMsgsPerSubject = %d, want 50000", got.MaxMsgsPerSubject)
	}
	if got.MaxAge.Hours() != 1 {
		t.Errorf("MaxAge = %s, want 1h", got.MaxAge)
	}
}

func TestEnsureStream_RejectsEmptyName(t *testing.T) {
	t.Parallel()

	mgr := &stubStreamMgr{}
	if err := bus.EnsureStream(context.Background(), mgr, "", []string{"gateway.>"}); err == nil {
		t.Fatal("expected error on empty name")
	}
}

func TestEnsureStream_RejectsEmptySubjects(t *testing.T) {
	t.Parallel()

	mgr := &stubStreamMgr{}
	if err := bus.EnsureStream(context.Background(), mgr, bus.DefaultStreamName, nil); err == nil {
		t.Fatal("expected error on empty subjects")
	}
}

func TestEnsureStream_IdempotentOnSentinel(t *testing.T) {
	t.Parallel()

	mgr := &stubStreamMgr{createErr: jetstream.ErrStreamNameAlreadyInUse}
	if err := bus.EnsureStream(context.Background(), mgr, bus.DefaultStreamName, []string{"gateway.>"}); err != nil {
		t.Fatalf("expected nil on already-exists, got %v", err)
	}
}

func TestEnsureStream_IdempotentOnAlreadyExistsString(t *testing.T) {
	t.Parallel()

	mgr := &stubStreamMgr{createErr: errors.New("stream already in use elsewhere")}
	if err := bus.EnsureStream(context.Background(), mgr, bus.DefaultStreamName, []string{"gateway.>"}); err != nil {
		t.Fatalf("expected nil on already-exists string, got %v", err)
	}
}

func TestEnsureStream_PropagatesUnknownErrors(t *testing.T) {
	t.Parallel()

	mgr := &stubStreamMgr{createErr: errors.New("boom")}
	if err := bus.EnsureStream(context.Background(), mgr, bus.DefaultStreamName, []string{"gateway.>"}); err == nil {
		t.Fatal("expected propagation of unknown error")
	}
}

func TestEnsureObjectStore_CreatesWithExpectedConfig(t *testing.T) {
	t.Parallel()

	mgr := &stubObjMgr{}
	if _, err := bus.EnsureObjectStore(context.Background(), mgr, bus.DefaultObjectStoreBucket); err != nil {
		t.Fatalf("EnsureObjectStore: %v", err)
	}
	if mgr.createCalls.Load() != 1 {
		t.Fatalf("CreateObjectStore calls = %d, want 1", mgr.createCalls.Load())
	}
	if mgr.cfg.Bucket != bus.DefaultObjectStoreBucket {
		t.Errorf("Bucket = %q, want %q", mgr.cfg.Bucket, bus.DefaultObjectStoreBucket)
	}
	if mgr.cfg.TTL.Minutes() != 75 {
		t.Errorf("TTL = %s, want 75m", mgr.cfg.TTL)
	}
	if mgr.cfg.Storage != jetstream.FileStorage {
		t.Errorf("Storage = %v, want FileStorage", mgr.cfg.Storage)
	}
	if mgr.cfg.Replicas != 1 {
		t.Errorf("Replicas = %d, want 1", mgr.cfg.Replicas)
	}
}

func TestEnsureObjectStore_RejectsEmptyBucket(t *testing.T) {
	t.Parallel()

	mgr := &stubObjMgr{}
	if _, err := bus.EnsureObjectStore(context.Background(), mgr, ""); err == nil {
		t.Fatal("expected error on empty bucket")
	}
}

func TestEnsureObjectStore_IdempotentOnSentinel(t *testing.T) {
	t.Parallel()

	mgr := &stubObjMgr{createErr: jetstream.ErrBucketExists}
	if _, err := bus.EnsureObjectStore(context.Background(), mgr, bus.DefaultObjectStoreBucket); err != nil {
		t.Fatalf("expected nil on already-exists, got %v", err)
	}
	if mgr.lookupCalls.Load() != 1 {
		t.Fatalf("ObjectStore lookup calls = %d, want 1 after already-exists", mgr.lookupCalls.Load())
	}
}

func TestEnsureObjectStore_IdempotentOnAlreadyExistsString(t *testing.T) {
	t.Parallel()

	mgr := &stubObjMgr{createErr: errors.New("the bucket already exists, sorry")}
	if _, err := bus.EnsureObjectStore(context.Background(), mgr, bus.DefaultObjectStoreBucket); err != nil {
		t.Fatalf("expected nil on already-exists string, got %v", err)
	}
}

func TestEnsureObjectStore_PropagatesUnknownErrors(t *testing.T) {
	t.Parallel()

	mgr := &stubObjMgr{createErr: errors.New("permission denied")}
	if _, err := bus.EnsureObjectStore(context.Background(), mgr, bus.DefaultObjectStoreBucket); err == nil {
		t.Fatal("expected unknown error to propagate")
	}
}

func TestEnsureStream_NilCreateErrorPath(t *testing.T) {
	t.Parallel()

	mgr := &stubStreamMgr{}
	for i := 0; i < 2; i++ {
		if err := bus.EnsureStream(context.Background(), mgr, bus.DefaultStreamName, []string{"gateway.>"}); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}

func TestEnsureObjectStore_PropagatesLookupErrorAfterAlreadyExists(t *testing.T) {
	t.Parallel()

	mgr := &stubObjMgr{
		createErr: jetstream.ErrBucketExists,
		lookupErr: errors.New("lookup nope"),
	}
	if _, err := bus.EnsureObjectStore(context.Background(), mgr, bus.DefaultObjectStoreBucket); err == nil {
		t.Fatal("expected lookup error to propagate when create returns already-exists")
	}
}
