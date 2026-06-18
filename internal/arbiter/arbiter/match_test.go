package arbiter

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/config"
)

func TestMatchesAnyGlob(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		s        string
		want     bool
	}{
		{"empty patterns", nil, "pii.url", false},
		{"exact", []string{"pii.url"}, "pii.url", true},
		{"exact miss", []string{"pii.url"}, "pii.email", false},
		{"trailing star", []string{"pii.*"}, "pii.url", true},
		{"leading star", []string{"*:internal"}, "env:internal", true},
		{"leading star miss", []string{"*:internal"}, "env:external", false},
		{"suffix glob", []string{"*.url"}, "pii.url", true},
		{"one of many", []string{"injection.*", "pii.*"}, "pii.ssn", true},
		{"star spans whole value (no '/')", []string{"*"}, "tier:gold", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchesAnyGlob(c.patterns, c.s); got != c.want {
				t.Errorf("matchesAnyGlob(%v, %q) = %v, want %v", c.patterns, c.s, got, c.want)
			}
		})
	}
}

func TestTagSelectorAdmits(t *testing.T) {
	cases := []struct {
		name     string
		selector tagSelector
		tags     []string
		want     bool
	}{
		{"empty selector admits all", tagSelector{}, []string{"tier:gold"}, true},
		{"empty selector admits untagged", tagSelector{}, nil, true},
		{"include match", tagSelector{include: []string{"tier:*"}}, []string{"tier:gold"}, true},
		{"include miss", tagSelector{include: []string{"tier:*"}}, []string{"env:prod"}, false},
		{"include excludes untagged", tagSelector{include: []string{"tier:*"}}, nil, false},
		{"exclude match skips", tagSelector{exclude: []string{"env:internal"}}, []string{"env:internal"}, false},
		{"exclude miss admits", tagSelector{exclude: []string{"env:internal"}}, []string{"env:prod"}, true},
		{"exclude wins over include", tagSelector{include: []string{"tier:*"}, exclude: []string{"tier:internal"}}, []string{"tier:internal"}, false},
		{"include hit with unrelated exclude", tagSelector{include: []string{"tier:*"}, exclude: []string{"env:*"}}, []string{"tier:gold", "region:eu"}, true},
		{"any tag excluded skips whole span", tagSelector{exclude: []string{"env:*"}}, []string{"tier:gold", "env:internal"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.selector.admits(c.tags); got != c.want {
				t.Errorf("admits(%v) = %v, want %v", c.tags, got, c.want)
			}
		})
	}
}

func TestSeverityClassifier(t *testing.T) {
	cls := newSeverityClassifier(config.Severity{
		Unmapped: config.SeverityWarning,
		Rules: []config.SeverityRule{
			{Match: "pii.url", Level: config.SeverityInfo},  // specific first
			{Match: "pii.*", Level: config.SeverityWarning}, // general second
			{Match: "injection.*", Level: config.SeverityError},
		},
	})
	cases := []struct {
		category string
		want     string
	}{
		{"pii.url", config.SeverityInfo},      // exact specific wins over pii.*
		{"pii.email", config.SeverityWarning}, // falls through to pii.*
		{"injection.jailbreak", config.SeverityError},
		{"toxicity.threat", config.SeverityWarning}, // no rule -> unmapped
	}
	for _, c := range cases {
		if got := cls.level(c.category); got != c.want {
			t.Errorf("level(%q) = %q, want %q", c.category, got, c.want)
		}
	}
}

func TestSeverityClassifier_EmptyUnmappedDefaultsWarning(t *testing.T) {
	cls := newSeverityClassifier(config.Severity{}) // no rules, no unmapped
	if got := cls.level("anything"); got != config.SeverityWarning {
		t.Errorf("empty unmapped: level = %q, want %q", got, config.SeverityWarning)
	}
}

func TestMaxSeverity(t *testing.T) {
	cases := []struct {
		name   string
		levels []string
		want   string
	}{
		{"none", nil, ""},
		{"single", []string{config.SeverityInfo}, config.SeverityInfo},
		{"error dominates", []string{config.SeverityInfo, config.SeverityError, config.SeverityWarning}, config.SeverityError},
		{"warning over info", []string{config.SeverityInfo, config.SeverityWarning}, config.SeverityWarning},
		{"unknown ranks zero", []string{"", "bogus", config.SeverityInfo}, config.SeverityInfo},
		{"all unknown -> empty", []string{"", "bogus"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := maxSeverity(c.levels); got != c.want {
				t.Errorf("maxSeverity(%v) = %q, want %q", c.levels, got, c.want)
			}
		})
	}
}
