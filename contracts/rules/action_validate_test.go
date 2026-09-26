package rules_test

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/slipspace-gateway/contracts/rules"
)

// ruleWith wraps one action's YAML in a minimal valid RuleContract so the
// table below exercises only the action's validate() hook.
func ruleWith(actionYAML string) string {
	return "name: t\ncondition:\n  type: protocol\n  operator: equals\n  expectedProtocol: chat\nactions:\n" + actionYAML
}

// TestActionValidate_LoadTimeHooks pins issue #527: every action type
// implements validate(), so an empty required field (or a malformed value
// the runtime would reject on every match) fails RuleContract.Validate at
// config load and at the admin write API, instead of per request.
func TestActionValidate_LoadTimeHooks(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		action  string
		wantErr error // nil = must validate clean
	}{
		// changeProvider
		{"changeProvider ok", "  - type: changeProvider\n    newProvider: openai\n", nil},
		{"changeProvider empty", "  - type: changeProvider\n    newProvider: \"  \"\n", rules.ErrEmptyActionField},
		{"changeProvider missing", "  - type: changeProvider\n", rules.ErrEmptyActionField},

		// changeModelName
		{"changeModelName ok", "  - type: changeModelName\n    newModelName: gpt-4o\n", nil},
		{"changeModelName empty", "  - type: changeModelName\n    newModelName: \"\"\n", rules.ErrEmptyActionField},

		// changeUrl
		{"changeUrl ok", "  - type: changeUrl\n    newUrl: https://example.com/v1\n", nil},
		{"changeUrl empty", "  - type: changeUrl\n", rules.ErrEmptyActionField},
		{"changeUrl unparsable", "  - type: changeUrl\n    newUrl: \"http://[::1\"\n", rules.ErrInvalidActionField},

		// changeApiKey
		{"changeApiKey literal ok", "  - type: changeApiKey\n    apiKey: sk-x\n", nil},
		{"changeApiKey useSlipSpaceKey ok without literal", "  - type: changeApiKey\n    useSlipSpaceKey: true\n", nil},
		{"changeApiKey empty literal", "  - type: changeApiKey\n    apiKey: \"\"\n", rules.ErrEmptyActionField},
		{"changeApiKey missing", "  - type: changeApiKey\n", rules.ErrEmptyActionField},

		// setHeader
		{"setHeader Set ok", "  - type: setHeader\n    headerName: X-A\n    headerAction: Set\n    headerValue: v\n", nil},
		{"setHeader Remove ok without value", "  - type: setHeader\n    headerName: X-A\n    headerAction: Remove\n", nil},
		{"setHeader Set with empty value ok", "  - type: setHeader\n    headerName: X-A\n    headerAction: Set\n    headerValue: \"\"\n", nil},
		{"setHeader empty name", "  - type: setHeader\n    headerName: \"\"\n    headerAction: Set\n", rules.ErrEmptyActionField},
		{"setHeader missing action", "  - type: setHeader\n    headerName: X-A\n", rules.ErrInvalidActionField},
		{"setHeader lowercase action", "  - type: setHeader\n    headerName: X-A\n    headerAction: set\n", rules.ErrInvalidActionField},
		{"setHeader unknown action", "  - type: setHeader\n    headerName: X-A\n    headerAction: Toggle\n", rules.ErrInvalidActionField},

		// appendQueryString
		{"appendQueryString ok", "  - type: appendQueryString\n    key: api-version\n    value: \"2024-02-01\"\n", nil},
		{"appendQueryString empty value ok", "  - type: appendQueryString\n    key: flag\n", nil},
		{"appendQueryString empty key", "  - type: appendQueryString\n    key: \"\"\n    value: v\n", rules.ErrEmptyActionField},

		// returnStatusCode
		{"returnStatusCode ok", "  - type: returnStatusCode\n    statusCode: 429\n", nil},
		{"returnStatusCode boundaries ok", "  - type: returnStatusCode\n    statusCode: 100\n  - type: returnStatusCode\n    statusCode: 599\n", nil},
		{"returnStatusCode missing", "  - type: returnStatusCode\n    body: nope\n", rules.ErrInvalidActionField},
		{"returnStatusCode too low", "  - type: returnStatusCode\n    statusCode: 99\n", rules.ErrInvalidActionField},
		{"returnStatusCode too high", "  - type: returnStatusCode\n    statusCode: 600\n", rules.ErrInvalidActionField},

		// llmImpersonation
		{"llmImpersonation ok", "  - type: llmImpersonation\n    message: blocked\n", nil},
		{"llmImpersonation empty", "  - type: llmImpersonation\n    message: \"\"\n", rules.ErrEmptyActionField},

		// addTag
		{"addTag ok", "  - type: addTag\n    tag: tier:gold\n", nil},
		{"addTag empty", "  - type: addTag\n    tag: \"\"\n", rules.ErrEmptyActionField},
		{"addTag whitespace", "  - type: addTag\n    tag: \"   \"\n", rules.ErrEmptyActionField},

		// useResiliencePolicy
		{"useResiliencePolicy ok", "  - type: useResiliencePolicy\n    policyName: ha\n", nil},
		{"useResiliencePolicy empty", "  - type: useResiliencePolicy\n    policyName: \"\"\n", rules.ErrEmptyActionField},

		// the pre-existing hooks still hold
		{"translate empty", "  - type: translate\n    targetProtocol: \"\"\n", rules.ErrEmptyTranslateTarget},
		{"rewriteField bad target", "  - type: rewriteField\n    target: nowhere.x\n    value: 1\n", rules.ErrInvalidTarget},

		// unknown discriminators stay inert (forward-compat), never rejected
		{"unknown action loads clean", "  - type: futureAction\n    whatever: 1\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rc rules.RuleContract
			if err := yaml.Unmarshal([]byte(ruleWith(tt.action)), &rc); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			err := rc.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() = %v, want errors.Is %v", err, tt.wantErr)
			}
			// The wrapped message must name the action so the loader's
			// error points at the offending rule entry.
			if err.Error() == tt.wantErr.Error() {
				t.Fatalf("error %q is unwrapped; want action type + field context", err)
			}
		})
	}
}

// TestActionValidate_ErrorNamesActionAndField checks the wrapping shape a
// startup log or 422 body will carry.
func TestActionValidate_ErrorNamesActionAndField(t *testing.T) {
	t.Parallel()
	var rc rules.RuleContract
	if err := yaml.Unmarshal([]byte(ruleWith("  - type: addTag\n    tag: \"\"\n")), &rc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	err := rc.Validate()
	if err == nil {
		t.Fatal("want error")
	}
	want := `rule "t": actions[0]: addTag: tag: rules: required action field is empty`
	if err.Error() != want {
		t.Fatalf("error = %q\nwant  %q", err.Error(), want)
	}
}
