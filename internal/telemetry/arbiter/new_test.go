package arbiter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	detectv1 "github.com/andyjmorgan/sluice-gateway/gen/slipspace/detect/v1"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/config"
	"github.com/andyjmorgan/sluice-gateway/internal/telemetry/store"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestNew(t *testing.T) {
	cfg := config.Config{Scanner: config.Scanner{
		Enabled:     true,
		EvidenceKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)),
		Detectors: []config.Detector{
			{CheckType: "toxicity", Endpoint: "http://tox"},
			{CheckType: "injection", Endpoint: "http://inj", Threshold: 0.5},
		},
	}}
	s, err := New(context.Background(), cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	// check types come back sorted regardless of config order.
	if got := s.CheckTypes(); len(got) != 2 || got[0] != "injection" || got[1] != "toxicity" {
		t.Errorf("check types = %+v, want [injection toxicity]", got)
	}
	if s.detectTimeout != detectTimeout || s.maxAttempts != maxAttempts {
		t.Errorf("tunables not defaulted: %+v", s)
	}
}

func TestNew_BadEvidenceKey(t *testing.T) {
	cfg := config.Config{Scanner: config.Scanner{Enabled: true, EvidenceKey: "!!not-base64"}}
	if _, err := New(context.Background(), cfg, testLogger()); err == nil {
		t.Error("expected error for bad evidence key")
	}
}

// --- error-branch coverage: a store that fails selected ops ---

type errStore struct {
	*fakeStore
	failList         bool
	failComplete     bool
	failClaim        bool
	failReady        bool
	failInconclusive bool
	failRetry        bool
	failFinding      bool
	failEvidence     bool
}

func (e errStore) ListFindings(ctx context.Context, c string) ([]store.Finding, error) {
	if e.failList {
		return nil, errors.New("list boom")
	}
	return e.fakeStore.ListFindings(ctx, c)
}

func (e errStore) CompleteCheckTask(ctx context.Context, corr, unit, check, status string, r json.RawMessage) error {
	if e.failComplete {
		return errors.New("complete boom")
	}
	return e.fakeStore.CompleteCheckTask(ctx, corr, unit, check, status, r)
}

func TestReduce_ListError(t *testing.T) {
	fake := newFakeStore()
	s := testScanner(fake, "")
	s.store = errStore{fakeStore: fake, failList: true}
	// Must not panic / must not write a verdict on a list error.
	s.reduce(context.Background(), "nope")
	if _, ok := fake.verdict("nope"); ok {
		t.Error("verdict written despite list error")
	}
}

func TestComplete_ErrorLogged(t *testing.T) {
	ctx := context.Background()
	srv := detectorServer(t, func(req *detectv1.DetectRequest) (*detectv1.DetectResponse, int) {
		return &detectv1.DetectResponse{CorrelationId: req.GetCorrelationId(), Status: detectv1.Status_STATUS_OK}, http.StatusOK
	})
	defer srv.Close()
	fake := newFakeStore()
	s := testScanner(fake, srv.URL)
	s.store = errStore{fakeStore: fake, failComplete: true}
	e := spanWith("c", map[string]any{"input_messages": []map[string]any{msg("user", textPart("hi"))}})
	_ = fake.UpsertRequestEventWithChecks(ctx, e, s.Explode(e))
	claimed, _ := fake.ClaimCheckTasks(ctx, 10, 120)
	for _, task := range claimed {
		s.process(ctx, task) // complete errors internally; must not panic
	}
}
