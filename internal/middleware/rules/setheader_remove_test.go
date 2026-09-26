package rules

import (
	"testing"

	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

func setHeader(t *testing.T, s *MutableState, name string, op contractsrules.HeaderOp, value string) {
	t.Helper()
	if _, err := applyAction(&contractsrules.SetHeaderAction{HeaderName: name, HeaderAction: op, HeaderValue: value}, s, nil); err != nil {
		t.Fatalf("setHeader %s %s: %v", op, name, err)
	}
}

// TestApplySetHeader_RemoveRecordsDropHeader pins issue #564: Remove must
// record the header on state.DropHeaders so the forwarder strips the
// inbound client header, not merely delete a value an earlier rule wrote.
func TestApplySetHeader_RemoveRecordsDropHeader(t *testing.T) {
	t.Parallel()

	t.Run("records canonical name once", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		setHeader(t, s, "x-internal-token", contractsrules.HeaderRemove, "")
		setHeader(t, s, "X-Internal-Token", contractsrules.HeaderRemove, "")
		if len(s.DropHeaders) != 1 || s.DropHeaders[0] != "X-Internal-Token" {
			t.Fatalf("DropHeaders = %v, want [X-Internal-Token]", s.DropHeaders)
		}
	})

	t.Run("Set after Remove un-records", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		setHeader(t, s, "X-Tier", contractsrules.HeaderRemove, "")
		setHeader(t, s, "x-tier", contractsrules.HeaderSet, "gold")
		if len(s.DropHeaders) != 0 {
			t.Fatalf("DropHeaders = %v, want empty after Set", s.DropHeaders)
		}
		if got := s.OutgoingHeaders.Get("X-Tier"); got != "gold" {
			t.Fatalf("X-Tier = %q, want gold", got)
		}
	})

	t.Run("Append and Prepend after Remove un-record", func(t *testing.T) {
		t.Parallel()
		for _, op := range []contractsrules.HeaderOp{contractsrules.HeaderAppend, contractsrules.HeaderPrepend} {
			s := freshState(t)
			setHeader(t, s, "X-Tier", contractsrules.HeaderRemove, "")
			setHeader(t, s, "X-Tier", op, "v")
			if len(s.DropHeaders) != 0 {
				t.Fatalf("%s: DropHeaders = %v, want empty", op, s.DropHeaders)
			}
		}
	})

	t.Run("Remove after Set drops and clears the value", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		setHeader(t, s, "X-Tier", contractsrules.HeaderSet, "gold")
		setHeader(t, s, "X-Tier", contractsrules.HeaderRemove, "")
		if got := s.OutgoingHeaders.Get("X-Tier"); got != "" {
			t.Fatalf("X-Tier = %q, want empty", got)
		}
		if len(s.DropHeaders) != 1 || s.DropHeaders[0] != "X-Tier" {
			t.Fatalf("DropHeaders = %v, want [X-Tier]", s.DropHeaders)
		}
	})

	t.Run("un-removing an unrelated name leaves others intact", func(t *testing.T) {
		t.Parallel()
		s := freshState(t)
		setHeader(t, s, "X-A", contractsrules.HeaderRemove, "")
		setHeader(t, s, "X-B", contractsrules.HeaderRemove, "")
		setHeader(t, s, "X-A", contractsrules.HeaderSet, "1")
		if len(s.DropHeaders) != 1 || s.DropHeaders[0] != "X-B" {
			t.Fatalf("DropHeaders = %v, want [X-B]", s.DropHeaders)
		}
	})
}

func TestMutableState_DropHeader_NilAndEmpty(t *testing.T) {
	t.Parallel()
	var nilState *MutableState
	nilState.DropHeader("X-A")   // must not panic
	nilState.UndropHeader("X-A") // must not panic

	s := &MutableState{}
	s.DropHeader("")
	if len(s.DropHeaders) != 0 {
		t.Fatalf("empty name recorded: %v", s.DropHeaders)
	}
	s.UndropHeader("X-Never-Recorded")
	if len(s.DropHeaders) != 0 {
		t.Fatalf("DropHeaders = %v, want empty", s.DropHeaders)
	}
}
