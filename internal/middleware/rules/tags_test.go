package rules

import (
	"errors"
	"testing"

	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

func TestApplyAddTag(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	if _, err := applyAction(&contractsrules.AddTagAction{Tag: "tier:gold"}, s, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !s.HasTag("tier:gold") {
		t.Errorf("HasTag(tier:gold) = false; want true")
	}
}

func TestApplyAddTag_Idempotent(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	for i := 0; i < 3; i++ {
		if _, err := applyAction(&contractsrules.AddTagAction{Tag: "audit:pii"}, s, nil); err != nil {
			t.Fatalf("apply iter %d: %v", i, err)
		}
	}
	if len(s.Tags) != 1 || s.Tags[0] != "audit:pii" {
		t.Errorf("Tags = %v; want one [audit:pii]", s.Tags)
	}
}

func TestApplyAddTag_EmptyTagRejected(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	if _, err := applyAction(&contractsrules.AddTagAction{Tag: "   "}, s, nil); !errors.Is(err, errEmptyValue) {
		t.Fatalf("err = %v; want errEmptyValue", err)
	}
	if len(s.Tags) != 0 {
		t.Errorf("Tags = %v; want empty after rejected addTag", s.Tags)
	}
}

func TestApplyAddTag_AccumulatesInOrder(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	for _, tag := range []string{"a", "b", "c"} {
		if _, err := applyAction(&contractsrules.AddTagAction{Tag: tag}, s, nil); err != nil {
			t.Fatalf("apply %q: %v", tag, err)
		}
	}
	want := []string{"a", "b", "c"}
	if len(s.Tags) != len(want) {
		t.Fatalf("len = %d; want %d (Tags=%v)", len(s.Tags), len(want), s.Tags)
	}
	for i, v := range want {
		if s.Tags[i] != v {
			t.Errorf("Tags[%d] = %q; want %q", i, s.Tags[i], v)
		}
	}
}

func TestMatchTag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		gc   GatewayContext
		cond contractsrules.TagCondition
		want bool
	}{
		{
			name: "present",
			gc:   GatewayContext{Tags: []string{"vip", "audit:pii"}},
			cond: contractsrules.TagCondition{Operator: contractsrules.EnumEquals, ExpectedTag: "vip"},
			want: true,
		},
		{
			name: "absent",
			gc:   GatewayContext{Tags: []string{"vip"}},
			cond: contractsrules.TagCondition{Operator: contractsrules.EnumEquals, ExpectedTag: "audit:pii"},
			want: false,
		},
		{
			name: "empty tag set",
			gc:   GatewayContext{},
			cond: contractsrules.TagCondition{Operator: contractsrules.EnumEquals, ExpectedTag: "vip"},
			want: false,
		},
		{
			name: "empty expected tag",
			gc:   GatewayContext{Tags: []string{""}},
			cond: contractsrules.TagCondition{Operator: contractsrules.EnumEquals, ExpectedTag: ""},
			want: false,
		},
		{
			name: "unknown operator falls to false",
			gc:   GatewayContext{Tags: []string{"vip"}},
			cond: contractsrules.TagCondition{Operator: contractsrules.EnumOperator("Wat"), ExpectedTag: "vip"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := matchTag(tc.cond, tc.gc); got != tc.want {
				t.Errorf("matchTag = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestLiveGatewayContext_PropagatesTags proves the per-iteration
// GatewayContext rebuild copies the accumulated tag set onto gc so a
// later rule's TagCondition sees what earlier rules attached.
func TestLiveGatewayContext_PropagatesTags(t *testing.T) {
	t.Parallel()
	s := freshState(t)
	s.AddTag("vip")
	s.AddTag("beta")
	gc := liveGatewayContext(GatewayContext{}, s, nil)
	if len(gc.Tags) != 2 || gc.Tags[0] != "vip" || gc.Tags[1] != "beta" {
		t.Errorf("gc.Tags = %v; want [vip beta]", gc.Tags)
	}
}

// TestMutableState_HasTag_NilReceiverSafe defends the nil receiver
// branch — the reporter calls HasTag/AddTag through a state that may
// be nil when the rules middleware was bypassed.
func TestMutableState_HasTag_NilReceiverSafe(t *testing.T) {
	t.Parallel()
	var s *MutableState
	if s.HasTag("anything") {
		t.Errorf("HasTag on nil receiver returned true")
	}
	if s.AddTag("nope") {
		t.Errorf("AddTag on nil receiver returned true")
	}
}
