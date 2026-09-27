package main

import (
	"context"
	"testing"
	"time"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	cc "github.com/andyjmorgan/slipspace-gateway/contracts/connector"
	"github.com/andyjmorgan/slipspace-gateway/internal/spool"
)

// stubConnector is the minimal connector.Connector the option-mapping
// tests need; Upload is never called.
type stubConnector struct{ name string }

func (s stubConnector) Name() string                                   { return s.name }
func (s stubConnector) Type() string                                   { return "stub" }
func (s stubConnector) Upload(context.Context, cc.SealedSegment) error { return nil }

// TestSpoolTrackOptions_UploadTimeoutAlwaysWired pins #523: production
// tracks must never run Connector.Upload without a per-attempt deadline.
func TestSpoolTrackOptions_UploadTimeoutAlwaysWired(t *testing.T) {
	c := stubConnector{name: "audit"}

	unset := spoolTrackOptions(contractsconfig.Connector{Name: "audit", Type: "s3"}, c)
	if want := time.Duration(contractsconfig.DefaultUploadTimeoutSeconds) * time.Second; unset.UploadAttemptTimeout != want {
		t.Errorf("unset upload_timeout_seconds → UploadAttemptTimeout = %v, want default %v", unset.UploadAttemptTimeout, want)
	}
	if unset.Connector == nil || unset.Connector.Name() != "audit" {
		t.Errorf("connector not carried onto the track options: %+v", unset.Connector)
	}
	if unset.Rotation != (spool.RotationOpts{}) {
		t.Errorf("no rotation block should leave RotationOpts zero, got %+v", unset.Rotation)
	}

	explicit := spoolTrackOptions(contractsconfig.Connector{
		Name: "audit", Type: "s3", UploadTimeoutSeconds: 15,
		Rotation: &contractsconfig.ConnectorRotation{MaxBytes: 1024, MaxAgeSeconds: 7},
	}, c)
	if explicit.UploadAttemptTimeout != 15*time.Second {
		t.Errorf("explicit upload_timeout_seconds: 15 → %v, want 15s", explicit.UploadAttemptTimeout)
	}
	if explicit.Rotation.MaxBytes != 1024 || explicit.Rotation.MaxAge != 7*time.Second {
		t.Errorf("rotation not mapped: %+v", explicit.Rotation)
	}
}
