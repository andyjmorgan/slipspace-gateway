package config

import (
	"reflect"
	"testing"
)

func TestGroup_TargetNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		targets []Target
		want    []string
	}{
		{"empty group", nil, []string{}},
		{"single target keeps provider name", []Target{{Provider: "openai"}}, []string{"openai"}},
		{
			"distinct providers keep plain names",
			[]Target{{Provider: "openai", Alias: "a"}, {Provider: "anthropic", Alias: "b"}},
			[]string{"openai", "anthropic"},
		},
		{
			"same provider twice disambiguated by alias",
			[]Target{{Provider: "openai", Alias: "gpt-4o"}, {Provider: "openai", Alias: "gpt-4o-mini"}},
			[]string{"openai#gpt-4o", "openai#gpt-4o-mini"},
		},
		{
			"same provider without alias uses 1-based position",
			[]Target{{Provider: "openai", Path: "/a"}, {Provider: "openai", Path: "/b"}},
			[]string{"openai#1", "openai#2"},
		},
		{
			"mixed alias and no alias",
			[]Target{{Provider: "openai"}, {Provider: "openai", Alias: "canary"}, {Provider: "anthropic"}},
			[]string{"openai#1", "openai#canary", "anthropic"},
		},
		{
			"alias collision falls back to position",
			[]Target{{Provider: "openai", Alias: "same", Path: "/a"}, {Provider: "openai", Alias: "same", Path: "/b"}},
			[]string{"openai#same", "openai#2"},
		},
		{
			"three arms on one provider",
			[]Target{{Provider: "ollama", Alias: "q7b"}, {Provider: "ollama", Alias: "q14b"}, {Provider: "ollama"}},
			[]string{"ollama#q7b", "ollama#q14b", "ollama#3"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Group{Targets: tc.targets}.TargetNames()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("TargetNames() = %v; want %v", got, tc.want)
			}
			seen := make(map[string]bool, len(got))
			for _, n := range got {
				if seen[n] {
					t.Errorf("TargetNames() produced duplicate %q in %v", n, got)
				}
				seen[n] = true
			}
		})
	}
}

func TestGroup_TargetNames_DoesNotMutateTargets(t *testing.T) {
	t.Parallel()
	g := Group{Targets: []Target{{Provider: "openai"}, {Provider: "openai", Alias: "x"}}}
	before := make([]Target, len(g.Targets))
	copy(before, g.Targets)
	_ = g.TargetNames()
	if !reflect.DeepEqual(before, g.Targets) {
		t.Errorf("TargetNames mutated the targets: %+v", g.Targets)
	}
}
