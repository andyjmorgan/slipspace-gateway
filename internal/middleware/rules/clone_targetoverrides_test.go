package rules_test

import (
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
)

// TestMutableState_Clone_TargetOverrides proves the per-attempt transport
// overrides (issue #409) survive Clone and that the clone's map is
// independent of the original's.
func TestMutableState_Clone_TargetOverrides(t *testing.T) {
	t.Parallel()
	s := &rules.MutableState{
		TargetPath:  "/openai/deployments/gpt4o/chat/completions",
		TargetQuery: map[string]string{"api-version": "2025-01-01"},
	}
	clone := s.Clone()

	if clone.TargetPath != s.TargetPath {
		t.Fatalf("clone.TargetPath = %q, want %q", clone.TargetPath, s.TargetPath)
	}
	if clone.TargetQuery["api-version"] != "2025-01-01" {
		t.Fatalf("clone.TargetQuery = %v", clone.TargetQuery)
	}
	clone.TargetQuery["api-version"] = "mutated"
	clone.TargetPath = "/elsewhere"
	if s.TargetQuery["api-version"] != "2025-01-01" || s.TargetPath != "/openai/deployments/gpt4o/chat/completions" {
		t.Fatalf("original mutated through clone: %q %v", s.TargetPath, s.TargetQuery)
	}

	empty := (&rules.MutableState{}).Clone()
	if empty.TargetQuery != nil || empty.TargetPath != "" {
		t.Fatalf("Clone allocated overrides for nil source: %q %v", empty.TargetPath, empty.TargetQuery)
	}
}
