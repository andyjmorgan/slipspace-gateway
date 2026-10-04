package spool

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a test leaves a goroutine behind. The
// spool owns per-track drain + uploader workers and the stop-join
// helpers; a leaked one after Stop / UnregisterTrack is a lifecycle bug,
// not noise.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
