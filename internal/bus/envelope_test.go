package bus_test

import (
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/andyjmorgan/sluice-gateway/internal/bus"
)

func TestEnvelope_InlineRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	src := bus.Envelope{
		SchemaVersion: bus.SchemaVersion,
		EventID:       "evt-1",
		EventType:     "request",
		Timestamp:     now,
		Mode:          bus.PayloadInline,
		InlinePayload: []byte(`{"k":"v"}`),
	}

	data, err := msgpack.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got bus.Envelope
	if err := msgpack.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.SchemaVersion != src.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, src.SchemaVersion)
	}
	if got.EventID != src.EventID {
		t.Errorf("EventID = %q, want %q", got.EventID, src.EventID)
	}
	if got.EventType != src.EventType {
		t.Errorf("EventType = %q, want %q", got.EventType, src.EventType)
	}
	if !got.Timestamp.Equal(src.Timestamp) {
		t.Errorf("Timestamp = %s, want %s", got.Timestamp, src.Timestamp)
	}
	if got.Mode != src.Mode {
		t.Errorf("Mode = %d, want %d", got.Mode, src.Mode)
	}
	if string(got.InlinePayload) != string(src.InlinePayload) {
		t.Errorf("InlinePayload = %q, want %q", got.InlinePayload, src.InlinePayload)
	}
	if got.ObjectRef != nil {
		t.Errorf("ObjectRef = %+v, want nil for inline mode", got.ObjectRef)
	}
}

func TestEnvelope_StashedRoundTrip(t *testing.T) {
	t.Parallel()

	src := bus.Envelope{
		SchemaVersion: bus.SchemaVersion,
		EventID:       "evt-2",
		EventType:     "unmapped",
		Timestamp:     time.Now().UTC(),
		Mode:          bus.PayloadStashed,
		ObjectRef: &bus.ObjectRef{
			Bucket: bus.DefaultObjectStoreBucket,
			Key:    "evt-2",
			Size:   1_048_576,
		},
	}

	data, err := msgpack.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got bus.Envelope
	if err := msgpack.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Mode != bus.PayloadStashed {
		t.Fatalf("Mode = %d, want PayloadStashed", got.Mode)
	}
	if got.InlinePayload != nil {
		t.Errorf("InlinePayload non-nil for stashed envelope: %q", got.InlinePayload)
	}
	if got.ObjectRef == nil {
		t.Fatal("ObjectRef nil for stashed envelope")
	}
	if got.ObjectRef.Bucket != src.ObjectRef.Bucket {
		t.Errorf("Bucket = %q, want %q", got.ObjectRef.Bucket, src.ObjectRef.Bucket)
	}
	if got.ObjectRef.Key != src.ObjectRef.Key {
		t.Errorf("Key = %q, want %q", got.ObjectRef.Key, src.ObjectRef.Key)
	}
	if got.ObjectRef.Size != src.ObjectRef.Size {
		t.Errorf("Size = %d, want %d", got.ObjectRef.Size, src.ObjectRef.Size)
	}
}

// TestEnvelope_FieldTagsMatchHarness verifies the publisher and the e2e
// harness Envelope decoders read each other's bytes byte-for-byte. The
// harness lives in test/e2e/harness/events.go behind the e2e build tag, so
// we encode against the same field names (v/id/t/ts/m/p/o/b/k/s) here
// rather than importing it.
func TestEnvelope_FieldTagsMatchHarness(t *testing.T) {
	t.Parallel()

	src := bus.Envelope{
		SchemaVersion: 1,
		EventID:       "id-abc",
		EventType:     "request",
		Timestamp:     time.Unix(1_700_000_000, 0).UTC(),
		Mode:          bus.PayloadInline,
		InlinePayload: []byte("hello"),
	}

	data, err := msgpack.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var asMap map[string]any
	if err := msgpack.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("unmarshal as map: %v", err)
	}

	wantKeys := []string{"v", "id", "t", "ts", "m", "p"}
	for _, k := range wantKeys {
		if _, ok := asMap[k]; !ok {
			t.Errorf("encoded envelope missing key %q (got keys %v)", k, mapKeys(asMap))
		}
	}
	if _, ok := asMap["o"]; ok {
		t.Errorf("inline envelope should omit %q key (got keys %v)", "o", mapKeys(asMap))
	}
}

func TestObjectRef_FieldTagsMatchHarness(t *testing.T) {
	t.Parallel()

	src := bus.Envelope{
		SchemaVersion: 1,
		EventID:       "id-stash",
		EventType:     "request",
		Timestamp:     time.Now().UTC(),
		Mode:          bus.PayloadStashed,
		ObjectRef: &bus.ObjectRef{
			Bucket: "B",
			Key:    "K",
			Size:   42,
		},
	}

	data, err := msgpack.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var asMap map[string]any
	if err := msgpack.Unmarshal(data, &asMap); err != nil {
		t.Fatalf("unmarshal as map: %v", err)
	}

	ref, ok := asMap["o"].(map[string]any)
	if !ok {
		t.Fatalf("envelope missing object ref under key %q, got %T", "o", asMap["o"])
	}
	for _, k := range []string{"b", "k", "s"} {
		if _, ok := ref[k]; !ok {
			t.Errorf("object ref missing key %q (got keys %v)", k, mapKeys(ref))
		}
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
