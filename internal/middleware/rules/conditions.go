package rules

import (
	"regexp"
	"strings"

	contractsrules "github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

// matchCondition dispatches a Condition to its concrete evaluator.
// Unknown discriminators (UnknownCondition) return false — forward-
// compatibility: a newer rule kind authored against a future control
// plane should be inert on an older gateway, not crash it.
//
// depth tracks recursive descent into RuleGroup children; maxDepth
// caps the cap operator-authored pathological YAML can reach. When
// the cap is hit, the call returns false and signals via the
// supplied groupDepthExceeded callback so the caller can increment
// the gateway.rule.errors.total counter.
func matchCondition(
	cond contractsrules.Condition,
	gc GatewayContext,
	depth, maxDepth int,
	groupDepthExceeded func(),
) bool {
	if cond == nil {
		return false
	}
	switch c := cond.(type) {
	case *contractsrules.ProviderCondition:
		return invertIf(c.Not, matchProvider(*c, gc))
	case *contractsrules.EndpointCondition:
		return invertIf(c.Not, matchEndpoint(*c, gc))
	case *contractsrules.ModelNameCondition:
		return invertIf(c.Not, matchModelName(*c, gc))
	case *contractsrules.HeaderCondition:
		return invertIf(c.Not, matchHeader(*c, gc))
	case *contractsrules.TagCondition:
		return invertIf(c.Not, matchTag(*c, gc))
	case *contractsrules.RuleGroup:
		return invertIf(c.Not, matchGroup(*c, gc, depth, maxDepth, groupDepthExceeded))
	case *contractsrules.UnknownCondition:
		return false
	default:
		return false
	}
}

func invertIf(not, result bool) bool {
	if not {
		return !result
	}
	return result
}

func matchProvider(c contractsrules.ProviderCondition, gc GatewayContext) bool {
	if c.Operator != contractsrules.EnumEquals {
		return false
	}
	return c.ExpectedProvider == gc.Provider
}

func matchEndpoint(c contractsrules.EndpointCondition, gc GatewayContext) bool {
	if c.Operator != contractsrules.EnumEquals {
		return false
	}
	return c.ExpectedEndpoint == gc.Endpoint
}

func matchModelName(c contractsrules.ModelNameCondition, gc GatewayContext) bool {
	return matchString(c.Operator, c.ExpectedModelName, gc.Model, c.CaseInsensitive)
}

// matchHeader returns true when the headers contain a key satisfying
// KeyOperator+KeyPattern, and (when ValueOperator is set) its joined
// value satisfies ValueOperator+ValuePattern. Multi-value headers
// join with ", " per RFC 7230 §3.2.2 — same convention as Go's
// http.Header.Get when rendered for the wire.
func matchHeader(c contractsrules.HeaderCondition, gc GatewayContext) bool {
	if gc.Headers == nil {
		return false
	}
	for k, vs := range gc.Headers {
		if !matchString(c.KeyOperator, c.KeyPattern, k, c.CaseInsensitive) {
			continue
		}
		if c.ValueOperator == nil {
			return true
		}
		joined := strings.Join(vs, ", ")
		if matchString(*c.ValueOperator, c.ValuePattern, joined, c.CaseInsensitive) {
			return true
		}
	}
	return false
}

// matchTag returns true when gc.Tags contains the rule's
// ExpectedTag. Only EnumEquals is meaningful for a set-valued
// condition; any other operator evaluates to false (forward-compat
// for a future control plane that might mint operator values this
// build does not yet model).
func matchTag(c contractsrules.TagCondition, gc GatewayContext) bool {
	if c.Operator != contractsrules.EnumEquals {
		return false
	}
	if c.ExpectedTag == "" {
		return false
	}
	for _, t := range gc.Tags {
		if t == c.ExpectedTag {
			return true
		}
	}
	return false
}

// matchGroup recursively evaluates RuleGroup children with the
// configured logical operator. LogicalAnd short-circuits on the
// first false child; LogicalOr short-circuits on the first true.
// Empty Children always evaluates to false — a vacuously-true And
// could fire every rule and is more likely an authoring mistake
// than an intent.
func matchGroup(
	g contractsrules.RuleGroup,
	gc GatewayContext,
	depth, maxDepth int,
	groupDepthExceeded func(),
) bool {
	if depth >= maxDepth {
		if groupDepthExceeded != nil {
			groupDepthExceeded()
		}
		return false
	}
	if len(g.Children) == 0 {
		return false
	}
	switch g.LogicalOperator {
	case contractsrules.LogicalAnd:
		for _, child := range g.Children {
			if !matchCondition(child, gc, depth+1, maxDepth, groupDepthExceeded) {
				return false
			}
		}
		return true
	case contractsrules.LogicalOr:
		for _, child := range g.Children {
			if matchCondition(child, gc, depth+1, maxDepth, groupDepthExceeded) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// matchString applies the configured StringOperator with optional
// case folding. Unknown operators return false; an invalid Regex
// pattern returns false (the rule is inert until fixed, rather than
// crashing the request).
func matchString(op contractsrules.StringOperator, pattern, subject string, caseInsensitive bool) bool {
	if caseInsensitive {
		pattern = strings.ToLower(pattern)
		subject = strings.ToLower(subject)
	}
	switch op {
	case contractsrules.StringEquals:
		return subject == pattern
	case contractsrules.StringStartsWith:
		return strings.HasPrefix(subject, pattern)
	case contractsrules.StringEndsWith:
		return strings.HasSuffix(subject, pattern)
	case contractsrules.StringContains:
		return strings.Contains(subject, pattern)
	case contractsrules.StringRegex:
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false
		}
		return re.MatchString(subject)
	default:
		return false
	}
}
