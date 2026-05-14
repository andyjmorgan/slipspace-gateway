package bus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Default identifiers for the gateway reporting infrastructure.
const (
	DefaultStreamName        = "GATEWAY_EVENTS"
	DefaultObjectStoreBucket = "GATEWAY_EVENT_STASH"

	defaultStreamMaxAge      = time.Hour
	defaultObjectStoreMaxAge = 75 * time.Minute
	defaultMaxMsgsPerSubject = 50000
)

// StreamManager is the narrow subset of jetstream.JetStream required to
// provision a stream. Both jetstream.JetStream and integration test
// helpers satisfy it.
type StreamManager interface {
	CreateStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error)
}

// ObjectStoreManager is the narrow subset of jetstream.JetStream required
// to provision an Object Store bucket.
type ObjectStoreManager interface {
	CreateObjectStore(ctx context.Context, cfg jetstream.ObjectStoreConfig) (jetstream.ObjectStore, error)
	ObjectStore(ctx context.Context, bucket string) (jetstream.ObjectStore, error)
}

// EnsureStream creates the gateway events stream if it does not exist.
// "Already exists" errors are treated as success so the call is idempotent
// and safe to invoke on every startup. The stream config is not reconciled
// — if the existing stream has different subjects, retention, or storage,
// EnsureStream returns nil and leaves the operator to migrate.
//
// "Already exists" detection falls back to a substring match on the error
// message because the JetStream client wraps the sentinel inconsistently
// across server versions; see isAlreadyExists for the full reasoning.
func EnsureStream(ctx context.Context, js StreamManager, name string, subjects []string) error {
	if name == "" {
		return errors.New("bus: stream name required")
	}
	if len(subjects) == 0 {
		return errors.New("bus: at least one subject required")
	}

	cfg := jetstream.StreamConfig{
		Name:              name,
		Subjects:          subjects,
		Retention:         jetstream.LimitsPolicy,
		Storage:           jetstream.FileStorage,
		MaxAge:            defaultStreamMaxAge,
		MaxMsgsPerSubject: defaultMaxMsgsPerSubject,
	}

	if _, err := js.CreateStream(ctx, cfg); err != nil {
		if isAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("bus: create stream %s: %w", name, err)
	}
	return nil
}

// EnsureObjectStore creates the gateway stash bucket if it does not
// exist. The call is idempotent: on "already exists" the existing bucket
// is looked up and returned so callers receive a usable handle either
// way. As with EnsureStream, the bucket config is not reconciled — only
// existence is asserted.
//
// "Already exists" detection falls back to a substring match on the error
// message; see isAlreadyExists for the full reasoning.
func EnsureObjectStore(ctx context.Context, js ObjectStoreManager, bucket string) (jetstream.ObjectStore, error) {
	if bucket == "" {
		return nil, errors.New("bus: bucket name required")
	}

	cfg := jetstream.ObjectStoreConfig{
		Bucket:   bucket,
		TTL:      defaultObjectStoreMaxAge,
		Storage:  jetstream.FileStorage,
		Replicas: 1,
	}

	store, err := js.CreateObjectStore(ctx, cfg)
	if err == nil {
		return store, nil
	}
	if !isAlreadyExists(err) {
		return nil, fmt.Errorf("bus: create object store %s: %w", bucket, err)
	}

	store, err = js.ObjectStore(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("bus: lookup existing object store %s: %w", bucket, err)
	}
	return store, nil
}

// isAlreadyExists matches the JetStream "already in use" sentinel errors,
// falling back to a substring check on the message because the
// CreateObjectStore path returns a wrapped *jsError whose underlying
// sentinel (jetstream.ErrBucketExists) is exposed only by message —
// errors.Is does not unwrap to it cleanly across all server versions.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
		return true
	}
	if errors.Is(err, jetstream.ErrBucketExists) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already in use") || strings.Contains(msg, "already exists")
}
