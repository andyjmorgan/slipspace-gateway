package main

import (
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
)

// TestResilienceBridge_CarriesTargetOverrides pins issue #409 at the
// selection → orchestrator seam: the synthesised ResilienceTarget must carry
// the selected target's authored Path / Query so buildAttemptState can stamp
// them onto the per-attempt state for the final handler to re-apply.
func TestResilienceBridge_CarriesTargetOverrides(t *testing.T) {
	t.Parallel()

	t.Run("single binding", func(t *testing.T) {
		t.Parallel()
		rc := singleTargetConfig(selection.Target{
			Provider:      "azure",
			Alias:         "gpt4o-deployment",
			PathOverride:  "/openai/deployments/gpt4o/chat/completions",
			QueryOverride: map[string]string{"api-version": "2025-01-01"},
		})
		if len(rc.Targets) != 1 {
			t.Fatalf("targets = %d, want 1", len(rc.Targets))
		}
		tg := rc.Targets[0]
		if tg.Path != "/openai/deployments/gpt4o/chat/completions" {
			t.Errorf("Path = %q", tg.Path)
		}
		if tg.Query["api-version"] != "2025-01-01" {
			t.Errorf("Query = %v", tg.Query)
		}
		if len(tg.Actions) != 2 {
			t.Errorf("actions = %d, want changeProvider + changeModelName", len(tg.Actions))
		}
	})

	t.Run("group targets, per target", func(t *testing.T) {
		t.Parallel()
		rc := groupToResilienceConfig("ha", selection.Group{
			Mode: "failover",
			Targets: []selection.Target{
				{Provider: "p1", PathOverride: "/p1/path", QueryOverride: map[string]string{"v": "1"}},
				{Provider: "p2"},
			},
		})
		if len(rc.Targets) != 2 {
			t.Fatalf("targets = %d, want 2", len(rc.Targets))
		}
		if rc.Targets[0].Path != "/p1/path" || rc.Targets[0].Query["v"] != "1" {
			t.Errorf("targets[0] = %+v, want overrides carried", rc.Targets[0])
		}
		if rc.Targets[1].Path != "" || rc.Targets[1].Query != nil {
			t.Errorf("targets[1] = %+v, want no overrides", rc.Targets[1])
		}
	})

	t.Run("no overrides stay empty", func(t *testing.T) {
		t.Parallel()
		rc := singleTargetConfig(selection.Target{Provider: "openai"})
		if rc.Targets[0].Path != "" || rc.Targets[0].Query != nil {
			t.Errorf("unexpected overrides: %+v", rc.Targets[0])
		}
	})
}
