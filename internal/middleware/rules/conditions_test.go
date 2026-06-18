package rules

import (
	"net/http"
	"testing"

	contractsrules "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

func ctxWith(provider, protocol, model string, headers http.Header) GatewayContext {
	return GatewayContext{
		Provider: provider,
		Protocol: protocol,
		Model:    model,
		Headers:  headers,
	}
}

func TestMatchProvider(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cond contractsrules.ProviderCondition
		gc   GatewayContext
		want bool
	}{
		{
			"equals match",
			contractsrules.ProviderCondition{Type: "provider", Operator: contractsrules.EnumEquals, ExpectedProvider: "openai"},
			ctxWith("openai", "", "", nil),
			true,
		},
		{
			"equals no match",
			contractsrules.ProviderCondition{Type: "provider", Operator: contractsrules.EnumEquals, ExpectedProvider: "openai"},
			ctxWith("anthropic", "", "", nil),
			false,
		},
		{
			"Not inverts a true match",
			contractsrules.ProviderCondition{Type: "provider", Operator: contractsrules.EnumEquals, ExpectedProvider: "openai", Not: true},
			ctxWith("openai", "", "", nil),
			false,
		},
		{
			"Not inverts a false match",
			contractsrules.ProviderCondition{Type: "provider", Operator: contractsrules.EnumEquals, ExpectedProvider: "openai", Not: true},
			ctxWith("anthropic", "", "", nil),
			true,
		},
		{
			"unknown operator returns false",
			contractsrules.ProviderCondition{Type: "provider", Operator: "GreaterThan", ExpectedProvider: "openai"},
			ctxWith("openai", "", "", nil),
			false,
		},
		{
			"empty inputs",
			contractsrules.ProviderCondition{Type: "provider", Operator: contractsrules.EnumEquals},
			ctxWith("", "", "", nil),
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.cond
			got := matchCondition(&c, tc.gc, 0, 8, nil)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchProtocol(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cond contractsrules.ProtocolCondition
		gc   GatewayContext
		want bool
	}{
		{
			"equals match",
			contractsrules.ProtocolCondition{Operator: contractsrules.EnumEquals, ExpectedProtocol: "chat_completions"},
			ctxWith("", "chat_completions", "", nil),
			true,
		},
		{
			"equals no match",
			contractsrules.ProtocolCondition{Operator: contractsrules.EnumEquals, ExpectedProtocol: "chat_completions"},
			ctxWith("", "messages", "", nil),
			false,
		},
		{
			"Not inversion",
			contractsrules.ProtocolCondition{Operator: contractsrules.EnumEquals, ExpectedProtocol: "chat_completions", Not: true},
			ctxWith("", "chat_completions", "", nil),
			false,
		},
		{
			"unknown operator returns false",
			contractsrules.ProtocolCondition{Operator: "Contains", ExpectedProtocol: "chat"},
			ctxWith("", "chat_completions", "", nil),
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.cond
			got := matchCondition(&c, tc.gc, 0, 8, nil)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchModelName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cond contractsrules.ModelNameCondition
		gc   GatewayContext
		want bool
	}{
		{
			"equals exact",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringEquals, ExpectedModelName: "gpt-4o-mini"},
			ctxWith("", "", "gpt-4o-mini", nil),
			true,
		},
		{
			"equals mismatch",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringEquals, ExpectedModelName: "gpt-4o"},
			ctxWith("", "", "gpt-4o-mini", nil),
			false,
		},
		{
			"starts_with match",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringStartsWith, ExpectedModelName: "claude-"},
			ctxWith("", "", "claude-haiku-4-5", nil),
			true,
		},
		{
			"ends_with match",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringEndsWith, ExpectedModelName: "-mini"},
			ctxWith("", "", "gpt-4o-mini", nil),
			true,
		},
		{
			"contains match",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringContains, ExpectedModelName: "haiku"},
			ctxWith("", "", "claude-haiku-4-5", nil),
			true,
		},
		{
			"regex match",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringRegex, ExpectedModelName: `^gpt-\d`},
			ctxWith("", "", "gpt-4o", nil),
			true,
		},
		{
			"regex no match",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringRegex, ExpectedModelName: `^claude-\d`},
			ctxWith("", "", "gpt-4o", nil),
			false,
		},
		{
			"invalid regex returns false (does not panic)",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringRegex, ExpectedModelName: `^[unclosed`},
			ctxWith("", "", "gpt-4o", nil),
			false,
		},
		{
			"case insensitive folds both sides",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringEquals, ExpectedModelName: "GPT-4o", CaseInsensitive: true},
			ctxWith("", "", "gpt-4o", nil),
			true,
		},
		{
			"case sensitive is the default",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringEquals, ExpectedModelName: "GPT-4o"},
			ctxWith("", "", "gpt-4o", nil),
			false,
		},
		{
			"Not inverts match",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringStartsWith, ExpectedModelName: "claude-", Not: true},
			ctxWith("", "", "claude-haiku-4-5", nil),
			false,
		},
		{
			"unknown operator returns false",
			contractsrules.ModelNameCondition{Operator: "Greater", ExpectedModelName: "gpt-4o"},
			ctxWith("", "", "gpt-4o", nil),
			false,
		},
		{
			"empty model with empty pattern equals true",
			contractsrules.ModelNameCondition{Operator: contractsrules.StringEquals, ExpectedModelName: ""},
			ctxWith("", "", "", nil),
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.cond
			got := matchCondition(&c, tc.gc, 0, 8, nil)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchHeader(t *testing.T) {
	t.Parallel()

	op := func(o contractsrules.StringOperator) *contractsrules.StringOperator { return &o }

	multi := http.Header{}
	multi.Add("X-Tenant", "alpha")
	multi.Add("X-Tenant", "beta")

	cases := []struct {
		name string
		cond contractsrules.HeaderCondition
		gc   GatewayContext
		want bool
	}{
		{
			"key only — present",
			contractsrules.HeaderCondition{KeyOperator: contractsrules.StringEquals, KeyPattern: "X-Test"},
			ctxWith("", "", "", http.Header{"X-Test": []string{"v"}}),
			true,
		},
		{
			"key only — absent",
			contractsrules.HeaderCondition{KeyOperator: contractsrules.StringEquals, KeyPattern: "X-Missing"},
			ctxWith("", "", "", http.Header{"X-Test": []string{"v"}}),
			false,
		},
		{
			"key + value match",
			contractsrules.HeaderCondition{
				KeyOperator: contractsrules.StringEquals, KeyPattern: "X-Test",
				ValueOperator: op(contractsrules.StringEquals), ValuePattern: "v",
			},
			ctxWith("", "", "", http.Header{"X-Test": []string{"v"}}),
			true,
		},
		{
			"value mismatch",
			contractsrules.HeaderCondition{
				KeyOperator: contractsrules.StringEquals, KeyPattern: "X-Test",
				ValueOperator: op(contractsrules.StringEquals), ValuePattern: "v",
			},
			ctxWith("", "", "", http.Header{"X-Test": []string{"other"}}),
			false,
		},
		{
			"multi-value joins with comma + space",
			contractsrules.HeaderCondition{
				KeyOperator: contractsrules.StringEquals, KeyPattern: "X-Tenant",
				ValueOperator: op(contractsrules.StringEquals), ValuePattern: "alpha, beta",
			},
			ctxWith("", "", "", multi),
			true,
		},
		{
			"case insensitive key match",
			contractsrules.HeaderCondition{
				KeyOperator: contractsrules.StringEquals, KeyPattern: "x-test", CaseInsensitive: true,
			},
			ctxWith("", "", "", http.Header{"X-Test": []string{"v"}}),
			true,
		},
		{
			"Not inversion",
			contractsrules.HeaderCondition{
				KeyOperator: contractsrules.StringEquals, KeyPattern: "X-Test", Not: true,
			},
			ctxWith("", "", "", http.Header{"X-Test": []string{"v"}}),
			false,
		},
		{
			"nil headers returns false",
			contractsrules.HeaderCondition{KeyOperator: contractsrules.StringEquals, KeyPattern: "X-Test"},
			ctxWith("", "", "", nil),
			false,
		},
		{
			"prefix key operator",
			contractsrules.HeaderCondition{KeyOperator: contractsrules.StringStartsWith, KeyPattern: "X-"},
			ctxWith("", "", "", http.Header{"X-Tenant": []string{"alpha"}}),
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.cond
			got := matchCondition(&c, tc.gc, 0, 8, nil)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchGroup(t *testing.T) {
	t.Parallel()

	provOpenAI := &contractsrules.ProviderCondition{Operator: contractsrules.EnumEquals, ExpectedProvider: "openai"}
	provAnthropic := &contractsrules.ProviderCondition{Operator: contractsrules.EnumEquals, ExpectedProvider: "anthropic"}

	t.Run("AND all true", func(t *testing.T) {
		t.Parallel()
		g := &contractsrules.RuleGroup{
			LogicalOperator: contractsrules.LogicalAnd,
			Children:        []contractsrules.Condition{provOpenAI, provOpenAI},
		}
		if !matchCondition(g, ctxWith("openai", "", "", nil), 0, 8, nil) {
			t.Error("AND with all-true children should match")
		}
	})

	t.Run("AND short-circuits on first false", func(t *testing.T) {
		t.Parallel()
		g := &contractsrules.RuleGroup{
			LogicalOperator: contractsrules.LogicalAnd,
			Children:        []contractsrules.Condition{provAnthropic, provOpenAI},
		}
		if matchCondition(g, ctxWith("openai", "", "", nil), 0, 8, nil) {
			t.Error("AND should not match when first child is false")
		}
	})

	t.Run("OR true wins", func(t *testing.T) {
		t.Parallel()
		g := &contractsrules.RuleGroup{
			LogicalOperator: contractsrules.LogicalOr,
			Children:        []contractsrules.Condition{provAnthropic, provOpenAI},
		}
		if !matchCondition(g, ctxWith("openai", "", "", nil), 0, 8, nil) {
			t.Error("OR should match when any child is true")
		}
	})

	t.Run("OR all false", func(t *testing.T) {
		t.Parallel()
		g := &contractsrules.RuleGroup{
			LogicalOperator: contractsrules.LogicalOr,
			Children:        []contractsrules.Condition{provAnthropic, provAnthropic},
		}
		if matchCondition(g, ctxWith("openai", "", "", nil), 0, 8, nil) {
			t.Error("OR should not match when all children are false")
		}
	})

	t.Run("empty children is false", func(t *testing.T) {
		t.Parallel()
		g := &contractsrules.RuleGroup{LogicalOperator: contractsrules.LogicalAnd}
		if matchCondition(g, ctxWith("openai", "", "", nil), 0, 8, nil) {
			t.Error("empty children should not match")
		}
	})

	t.Run("unknown logical operator is false", func(t *testing.T) {
		t.Parallel()
		g := &contractsrules.RuleGroup{
			LogicalOperator: "Xor",
			Children:        []contractsrules.Condition{provOpenAI},
		}
		if matchCondition(g, ctxWith("openai", "", "", nil), 0, 8, nil) {
			t.Error("unknown logical operator should not match")
		}
	})

	t.Run("Not inverts group", func(t *testing.T) {
		t.Parallel()
		g := &contractsrules.RuleGroup{
			LogicalOperator: contractsrules.LogicalAnd,
			Children:        []contractsrules.Condition{provOpenAI},
			Not:             true,
		}
		if matchCondition(g, ctxWith("openai", "", "", nil), 0, 8, nil) {
			t.Error("Not on a true group should invert to false")
		}
	})

	t.Run("depth cap returns false and fires callback", func(t *testing.T) {
		t.Parallel()
		called := 0
		// Build a nested AND { AND { provOpenAI } } and cap at depth 1 so the
		// inner group's child evaluation trips the cap.
		inner := &contractsrules.RuleGroup{
			LogicalOperator: contractsrules.LogicalAnd,
			Children:        []contractsrules.Condition{provOpenAI},
		}
		outer := &contractsrules.RuleGroup{
			LogicalOperator: contractsrules.LogicalAnd,
			Children:        []contractsrules.Condition{inner},
		}
		got := matchCondition(outer, ctxWith("openai", "", "", nil), 0, 1, func() { called++ })
		if got {
			t.Error("depth-capped recursion should not match")
		}
		if called == 0 {
			t.Error("groupDepthExceeded callback should have fired at least once")
		}
	})
}

func TestMatchCondition_NilAndUnknown(t *testing.T) {
	t.Parallel()

	if matchCondition(nil, ctxWith("openai", "", "", nil), 0, 8, nil) {
		t.Error("nil condition should not match")
	}

	uc := &contractsrules.UnknownCondition{Type: "futureType"}
	if matchCondition(uc, ctxWith("openai", "", "", nil), 0, 8, nil) {
		t.Error("UnknownCondition should not match (forward-compat)")
	}
}
