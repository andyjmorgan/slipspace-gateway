package arbiter

import (
	"path"

	"github.com/andyjmorgan/sluice-gateway/internal/arbiter/config"
)

// matchesAnyGlob reports whether s matches any of the glob patterns
// (path.Match semantics — '*' spans any run of non-'/' runes, and tags/
// categories carry no '/', so it spans the whole value). Patterns are validated
// at config load (config.validGlob), so a malformed pattern cannot reach here;
// defensively, path.ErrBadPattern is treated as no match rather than a panic.
func matchesAnyGlob(patterns []string, s string) bool {
	for _, p := range patterns {
		if ok, err := path.Match(p, s); err == nil && ok {
			return true
		}
	}
	return false
}

// tagSelector decides whether a span's request tags admit it for scanning. It
// is the compiled form of scanner.scan_tags; the zero value (no include, no
// exclude) admits everything, so the gate is inert until configured.
type tagSelector struct {
	// include, when non-empty, restricts scanning to spans with a matching tag.
	include []string
	// exclude skips spans with a matching tag and wins over include.
	exclude []string
}

// admits reports whether a span carrying tags should be scanned: no tag may
// match an exclude glob (exclude wins), and — when include is non-empty — at
// least one tag must match an include glob. A span with no tags is admitted
// only when include is empty, so a non-empty include excludes untagged spans.
func (t tagSelector) admits(tags []string) bool {
	for _, tag := range tags {
		if matchesAnyGlob(t.exclude, tag) {
			return false
		}
	}
	if len(t.include) == 0 {
		return true
	}
	for _, tag := range tags {
		if matchesAnyGlob(t.include, tag) {
			return true
		}
	}
	return false
}

// severityRule is a compiled category→level entry.
type severityRule struct {
	match string
	level string
}

// severityClassifier maps a finding category to a severity level. The first
// matching rule (in declared order) wins, so an operator orders specific globs
// before general ones; a category matching no rule gets the unmapped default.
type severityClassifier struct {
	rules    []severityRule
	unmapped string
}

// newSeverityClassifier compiles the config severity block. An empty unmapped
// level falls back to warning so a classifier built from a hand-constructed
// config (tests) still returns a real level.
func newSeverityClassifier(c config.Severity) severityClassifier {
	unmapped := c.Unmapped
	if unmapped == "" {
		unmapped = config.SeverityWarning
	}
	rules := make([]severityRule, len(c.Rules))
	for i, r := range c.Rules {
		rules[i] = severityRule{match: r.Match, level: r.Level}
	}
	return severityClassifier{rules: rules, unmapped: unmapped}
}

// level returns the severity for a finding category.
func (c severityClassifier) level(category string) string {
	for _, r := range c.rules {
		if ok, err := path.Match(r.match, category); err == nil && ok {
			return r.level
		}
	}
	return c.unmapped
}

// severityRank orders the levels for verdict roll-up (higher is more severe). An
// empty or unknown level ranks 0 so it never beats a classified finding.
func severityRank(level string) int {
	switch level {
	case config.SeverityError:
		return 3
	case config.SeverityWarning:
		return 2
	case config.SeverityInfo:
		return 1
	default:
		return 0
	}
}

// maxSeverity returns the highest-ranked level among levels, or "" if none rank
// above 0 — the verdict's rolled-up severity.
func maxSeverity(levels []string) string {
	best, bestRank := "", 0
	for _, l := range levels {
		if r := severityRank(l); r > bestRank {
			best, bestRank = l, r
		}
	}
	return best
}
