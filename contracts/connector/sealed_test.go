package connector

import "testing"

func TestSealedSegment_ZeroValue(t *testing.T) {
	// SealedSegment is a value type passed by the spooler. Confirm the
	// zero value is usable without any required-field surprises.
	var s SealedSegment
	if s.Path != "" || s.Bytes != 0 || s.Records != 0 {
		t.Errorf("zero SealedSegment unexpectedly populated: %+v", s)
	}
}

func TestSealedSegment_AssignmentRoundTrip(t *testing.T) {
	want := SealedSegment{
		Path:              "/tmp/spool/refinement/sealed/1.ndjson.zst",
		Bytes:             6443211,
		BytesUncompressed: 67108864,
		Records:           1024,
		TsMinNs:           1715000000000000000,
		TsMaxNs:           1715000005000000000,
		DeliveryID:        "01HW2C8Z000000000000000000",
		Connector:         "refinement-s3",
	}
	got := want
	if got != want {
		t.Errorf("assignment round-trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}
