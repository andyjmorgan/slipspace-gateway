package rules_test

import (
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/middleware/rules"
)

// TestMutableState_Clone_DropHeaders proves the setHeader Remove set
// survives the per-attempt clone the resilience orchestrator takes, and
// that the clone's slice is independent of the original's.
func TestMutableState_Clone_DropHeaders(t *testing.T) {
	t.Parallel()
	s := &rules.MutableState{DropHeaders: []string{"X-A", "X-B"}}
	clone := s.Clone()

	if len(clone.DropHeaders) != 2 || clone.DropHeaders[0] != "X-A" || clone.DropHeaders[1] != "X-B" {
		t.Fatalf("clone.DropHeaders = %v, want [X-A X-B]", clone.DropHeaders)
	}
	clone.DropHeader("X-C")
	clone.UndropHeader("X-A")
	if len(s.DropHeaders) != 2 || s.DropHeaders[0] != "X-A" {
		t.Fatalf("original mutated through clone: %v", s.DropHeaders)
	}

	empty := (&rules.MutableState{}).Clone()
	if empty.DropHeaders != nil {
		t.Fatalf("Clone allocated DropHeaders for nil source: %v", empty.DropHeaders)
	}
}
