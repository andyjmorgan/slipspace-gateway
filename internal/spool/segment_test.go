package spool

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
)

func TestOpenSegment_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	openedAt := time.Date(2026, 5, 22, 14, 0, 0, 123456789, time.UTC)
	s, err := OpenSegment(dir, 42, openedAt)
	if err != nil {
		t.Fatalf("OpenSegment: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	wantName := segmentFilename(openedAt, 42)
	if filepath.Base(s.Path()) != wantName {
		t.Errorf("Path filename = %q, want %q", filepath.Base(s.Path()), wantName)
	}
	if s.Seq() != 42 {
		t.Errorf("Seq = %d, want 42", s.Seq())
	}
	if _, err := os.Stat(s.Path()); err != nil {
		t.Errorf("file should exist on disk: %v", err)
	}
}

func TestOpenSegment_RejectsCollision(t *testing.T) {
	dir := t.TempDir()
	openedAt := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	s1, err := OpenSegment(dir, 1, openedAt)
	if err != nil {
		t.Fatalf("first OpenSegment: %v", err)
	}
	t.Cleanup(func() { _ = s1.Close() })

	_, err = OpenSegment(dir, 1, openedAt)
	if err == nil {
		t.Error("second OpenSegment with same seq+openedAt should fail O_EXCL")
	}
}

func TestOpenSegment_NonExistentDirErrors(t *testing.T) {
	_, err := OpenSegment("/no/such/dir/anywhere", 1, time.Now())
	if err == nil {
		t.Error("expected error opening segment in missing dir")
	}
}

func TestOpenSegment_ZstdNewWriterFailureCleansUp(t *testing.T) {
	// Substitute the encoder constructor to force a failure. Verifies
	// the defensive cleanup branch removes the half-opened file.
	orig := zstdNewWriter
	t.Cleanup(func() { zstdNewWriter = orig })
	zstdNewWriter = func(*os.File) (*zstd.Encoder, error) {
		return nil, errors.New("synthetic encoder failure")
	}

	dir := t.TempDir()
	_, err := OpenSegment(dir, 99, time.Now())
	if err == nil {
		t.Fatal("expected encoder failure")
	}
	// The half-opened file should have been cleaned up.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected dir empty after failure, got %v", entries)
	}
}

func TestSegment_WriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenSegment(dir, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	recs := []cc.Record{
		makeRecord("01HW2C00", 1715000000000000001, "openai", "chat_completions"),
		makeRecord("01HW2C01", 1715000000000000002, "anthropic", "messages"),
		makeRecord("01HW2C02", 1715000000000000003, "gemini", "generate_content"),
	}
	for _, r := range recs {
		if err := s.Write(r); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := readSegment(t, s.Path())
	if len(got) != len(recs) {
		t.Fatalf("read %d records, wrote %d", len(got), len(recs))
	}
	for i := range got {
		if got[i].ID != recs[i].ID {
			t.Errorf("rec %d: id = %q, want %q", i, got[i].ID, recs[i].ID)
		}
	}
}

func TestSegment_StatsTrackRange(t *testing.T) {
	dir := t.TempDir()
	openedAt := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	s, err := OpenSegment(dir, 1, openedAt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	tsNsLo := int64(1715000000000000123)
	tsNsHi := int64(1715000000000099999)
	_ = s.Write(makeRecord("a", tsNsHi, "openai", "chat_completions"))
	_ = s.Write(makeRecord("b", tsNsLo, "openai", "chat_completions"))
	_ = s.Write(makeRecord("c", tsNsHi-1, "openai", "chat_completions"))

	st := s.Stats()
	if st.Records != 3 {
		t.Errorf("Records = %d, want 3", st.Records)
	}
	if st.TsMinNs != tsNsLo {
		t.Errorf("TsMinNs = %d, want %d", st.TsMinNs, tsNsLo)
	}
	if st.TsMaxNs != tsNsHi {
		t.Errorf("TsMaxNs = %d, want %d", st.TsMaxNs, tsNsHi)
	}
	if st.BytesUncompressed <= 0 {
		t.Errorf("BytesUncompressed = %d, want > 0", st.BytesUncompressed)
	}
	if !st.OpenedAt.Equal(openedAt) {
		t.Errorf("OpenedAt = %v, want %v", st.OpenedAt, openedAt)
	}
}

func TestSegment_ShouldRotate(t *testing.T) {
	dir := t.TempDir()
	openedAt := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	s, err := OpenSegment(dir, 1, openedAt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := openedAt
	if s.ShouldRotate(1<<30, time.Hour, now) {
		t.Error("empty segment should not rotate")
	}

	// Trip byte cap.
	_ = s.Write(makeRecord("a", 1, "openai", "chat_completions"))
	if !s.ShouldRotate(1, time.Hour, now) {
		t.Error("expected byte-cap rotation to fire")
	}

	// Trip age cap.
	if !s.ShouldRotate(1<<30, time.Nanosecond, now.Add(time.Second)) {
		t.Error("expected age-cap rotation to fire")
	}

	// Both caps disabled = no rotate.
	if s.ShouldRotate(0, 0, now.Add(time.Hour)) {
		t.Error("zero caps should never rotate")
	}
}

func TestSegment_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenSegment(dir, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestSegment_WriteAfterCloseReturnsErr(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenSegment(dir, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	err = s.Write(makeRecord("x", 1, "openai", "chat_completions"))
	if !errors.Is(err, ErrSegmentClosed) {
		t.Errorf("Write after Close: got %v, want ErrSegmentClosed", err)
	}
}

func TestSegment_WriteFailsOnInvalidJSONRawMessage(t *testing.T) {
	// json.RawMessage validation only fires at Marshal time — a non-JSON
	// byte slice in there surfaces as an encoder error. Drives Write's
	// json.Marshal failure branch.
	dir := t.TempDir()
	s, err := OpenSegment(dir, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	bad := makeRecord("bad", 1, "openai", "chat_completions")
	bad.Request.Body = []byte("not valid json {")
	if err := s.Write(bad); err == nil {
		t.Error("expected Marshal failure for invalid RawMessage")
	}
}

func TestSegment_CloseUnderlyingFileGoneSurfaces(t *testing.T) {
	// Closing the file out from under the segment makes Sync error.
	dir := t.TempDir()
	s, err := OpenSegment(dir, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(makeRecord("x", 1, "openai", "chat_completions")); err != nil {
		t.Fatal(err)
	}
	// Force the underlying file closed; Close should now surface a
	// Sync or final-Close error.
	if err := s.file.Close(); err != nil {
		t.Fatalf("manual file close: %v", err)
	}
	err = s.Close()
	if err == nil {
		t.Error("expected error after underlying file closed")
	}
}

func TestSegmentFilename_Format(t *testing.T) {
	got := segmentFilename(time.Unix(0, 1715000000123456789), 42)
	want := "1715000000123456789-42.ndjson.zst"
	if got != want {
		t.Errorf("filename = %q, want %q", got, want)
	}
}

// --- helpers ---

func makeRecord(id string, tsNs int64, provider, protocol string) cc.Record {
	return cc.Record{
		V:             1,
		ID:            id,
		TsNs:          tsNs,
		Seq:           1,
		InstanceID:    "test-instance",
		CorrelationID: "corr-" + id,
		Configuration: "test",
		Provider:      provider,
		Protocol:      protocol,
		Request:       cc.RequestPart{Method: "POST", Path: "/x"},
		Response:      cc.ResponsePart{Status: 200},
		SchemaVersion: cc.SchemaVersion,
	}
}

// readSegment decodes a sealed segment file back into records. Used by
// tests to assert what was written; also exercises the on-disk format.
func readSegment(t *testing.T, path string) []cc.Record {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test reads a controlled path
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })

	dec, err := zstd.NewReader(f)
	if err != nil {
		t.Fatalf("zstd NewReader: %v", err)
	}
	t.Cleanup(dec.Close)

	var raw bytes.Buffer
	if _, err := io.Copy(&raw, dec); err != nil {
		t.Fatalf("zstd decode: %v", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(raw.String()))
	scanner.Buffer(make([]byte, 1<<16), 1<<20)
	var out []cc.Record
	for scanner.Scan() {
		var r cc.Record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatalf("unmarshal line: %v", err)
		}
		out = append(out, r)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}
	return out
}
