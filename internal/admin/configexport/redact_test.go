package configexport_test

import (
	"strings"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/admin/configexport"
)

func TestRedact_APIKeysSecretsReplacedWithPlaceholder(t *testing.T) {
	in := []byte(`api_keys:
  - name: prod
    secret: sk_live_aaaabbbbccccdddd
    configuration: production
  - name: dev
    secret: sk_live_eeeeffffgggghhhh
    configuration: internal-dev
`)
	out, err := configexport.Redact(in)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "sk_live_") {
		t.Fatalf("redacted output still contains sk_live_:\n%s", got)
	}
	if strings.Count(got, "***") != 2 {
		t.Fatalf("expected 2 placeholders, got %d:\n%s", strings.Count(got, "***"), got)
	}
	if !strings.Contains(got, "name: prod") || !strings.Contains(got, "configuration: production") {
		t.Fatalf("non-secret fields lost:\n%s", got)
	}
}

func TestRedact_UpstreamCredentialsValuesReplaced(t *testing.T) {
	in := []byte(`configurations:
  production:
    upstream_credentials:
      openai: sk-openai-aaaabbbb
      anthropic: sk-ant-ccccdddd
      gemini: AIzaeeeeffff
    rule_names: [route_claude]
  internal-dev:
    upstream_credentials:
      openai: sk-openai-gggghhhh
`)
	out, err := configexport.Redact(in)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	got := string(out)
	for _, plaintext := range []string{"sk-openai-aaaabbbb", "sk-ant-ccccdddd", "AIzaeeeeffff", "sk-openai-gggghhhh"} {
		if strings.Contains(got, plaintext) {
			t.Fatalf("plaintext credential %q survived redaction:\n%s", plaintext, got)
		}
	}
	// Provider keys are operator-defined names, not secrets — they must survive.
	for _, providerKey := range []string{"openai:", "anthropic:", "gemini:"} {
		if !strings.Contains(got, providerKey) {
			t.Fatalf("provider key %q lost:\n%s", providerKey, got)
		}
	}
	if !strings.Contains(got, "route_claude") {
		t.Fatalf("rule_names list lost:\n%s", got)
	}
}

func TestRedact_AdminPasswordReplaced(t *testing.T) {
	in := []byte(`admin:
  bind_addr: 0.0.0.0:8081
  enabled: true
  password: hunter2-prod-secret
`)
	out, err := configexport.Redact(in)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "hunter2") {
		t.Fatalf("admin password survived:\n%s", got)
	}
	if !strings.Contains(got, "bind_addr: 0.0.0.0:8081") || !strings.Contains(got, "enabled: true") {
		t.Fatalf("non-secret admin fields lost:\n%s", got)
	}
}

func TestRedact_ProvidersFileUntouched(t *testing.T) {
	// providers.yaml carries no declared secrets. Verifies the redactor passes
	// it through structurally intact (modulo yaml.v3 normalisation).
	in := []byte(`providers:
  openai:
    base_url: https://api.openai.com
    endpoints:
      chat_completions:
        accepted_paths: [/v1/chat/completions]
`)
	out, err := configexport.Redact(in)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	got := string(out)
	for _, want := range []string{"openai:", "base_url: https://api.openai.com", "/v1/chat/completions"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in passthrough output:\n%s", want, got)
		}
	}
	if strings.Contains(got, "***") {
		t.Fatalf("unexpected placeholder in providers-only doc:\n%s", got)
	}
}

func TestRedact_MissingFieldsTolerated(t *testing.T) {
	// An api_keys entry without a secret field, a configuration without
	// upstream_credentials, an admin block without a password — each must not
	// crash and must leave the rest of the document intact.
	in := []byte(`api_keys:
  - name: incomplete
    configuration: production
configurations:
  bare:
    rule_names: []
admin:
  enabled: false
`)
	out, err := configexport.Redact(in)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "***") {
		t.Fatalf("unexpected redaction in document with no secret fields:\n%s", got)
	}
	if !strings.Contains(got, "name: incomplete") || !strings.Contains(got, "enabled: false") {
		t.Fatalf("non-secret content disturbed:\n%s", got)
	}
}

func TestRedact_EmptyContentPassesThrough(t *testing.T) {
	out, err := configexport.Redact(nil)
	if err != nil {
		t.Fatalf("Redact(nil): %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty output, got %q", out)
	}
}

func TestRedact_WhitespaceOnlyDocumentPassesThrough(t *testing.T) {
	// yaml.Unmarshal of whitespace/comment-only content returns a node with
	// Kind=0; the redactor short-circuits on that and returns the input
	// rather than trying to walk a non-existent tree.
	cases := [][]byte{
		[]byte("\n"),
		[]byte("# nothing but a comment\n"),
		[]byte("   \n  \n"),
	}
	for _, in := range cases {
		out, err := configexport.Redact(in)
		if err != nil {
			t.Fatalf("Redact(%q): %v", in, err)
		}
		if string(out) != string(in) {
			t.Errorf("expected passthrough for %q, got %q", in, out)
		}
	}
}

func TestRedact_MalformedYAMLReturnsError(t *testing.T) {
	in := []byte("api_keys: [\n  - name: broken")
	_, err := configexport.Redact(in)
	if err == nil {
		t.Fatal("expected parse error for malformed YAML, got nil")
	}
}

func TestRedact_AllDeclaredSecretFieldsRedactedInCombinedDoc(t *testing.T) {
	// This test pins the deny-by-default invariant: a combined document
	// exercising every declared secret-bearing field must have every secret
	// path replaced. If a new secret-bearing field is added to the contract
	// shape without updating Redact, this test continues to pass for the old
	// paths but a new test case must be added — that is the human gate.
	in := []byte(`api_keys:
  - name: a
    secret: SECRET_A
    configuration: production
configurations:
  production:
    upstream_credentials:
      openai: PROVIDER_KEY_A
      anthropic: PROVIDER_KEY_B
admin:
  password: ADMIN_PW
`)
	out, err := configexport.Redact(in)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	got := string(out)
	for _, plaintext := range []string{"SECRET_A", "PROVIDER_KEY_A", "PROVIDER_KEY_B", "ADMIN_PW"} {
		if strings.Contains(got, plaintext) {
			t.Fatalf("declared secret %q survived redaction:\n%s", plaintext, got)
		}
	}
	// Four secrets, each redacted exactly once.
	if got, want := strings.Count(got, "***"), 4; got != want {
		t.Fatalf("placeholder count = %d, want %d", got, want)
	}
}

func TestRedact_NonMappingRootPassesThrough(t *testing.T) {
	// A YAML document whose root is a sequence (not a mapping) has no
	// top-level keys the redactor can dispatch on. Pass through unchanged
	// rather than crashing.
	in := []byte("- one\n- two\n")
	out, err := configexport.Redact(in)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if !strings.Contains(string(out), "- one") {
		t.Fatalf("sequence root lost in passthrough:\n%s", out)
	}
}

func TestRedact_DefensiveGuardsToleratNonStandardShapes(t *testing.T) {
	// The per-section walkers all start with a kind guard so a future
	// schema change that swaps a sequence for a mapping (or vice-versa)
	// doesn't crash redaction. These cases pin every guard so the
	// "deny-by-default + pass-through" failure mode stays load-bearing.
	cases := []struct {
		name string
		in   string
	}{
		{"api_keys as scalar", "api_keys: not-a-list\n"},
		{"api_keys with scalar entry", "api_keys:\n  - just-a-scalar\n"},
		{"configurations as sequence", "configurations:\n  - bare\n"},
		{"configurations entry as scalar", "configurations:\n  bad: scalar-value\n"},
		{"upstream_credentials as scalar", "configurations:\n  c1:\n    upstream_credentials: not-a-map\n"},
		{"admin as scalar", "admin: just-a-string\n"},
		{"admin as sequence", "admin:\n  - one\n  - two\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := configexport.Redact([]byte(c.in))
			if err != nil {
				t.Fatalf("Redact: %v", err)
			}
			if strings.Contains(string(out), "***") {
				t.Errorf("unexpected redaction for non-standard shape:\n%s", out)
			}
		})
	}
}

func TestRedact_TopLevelKeyWithoutSecretsPassesThrough(t *testing.T) {
	// rules and resilience_policies are top-level keys the redactor does
	// not dispatch on — they carry operator policy data, no secrets.
	// Document survives untouched (modulo yaml.v3 normalisation).
	in := []byte(`rules:
  - name: route_claude
    conditions: []
    actions: []
resilience_policies:
  - name: openai-failover
    targets: []
`)
	out, err := configexport.Redact(in)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if strings.Contains(string(out), "***") {
		t.Errorf("unexpected redaction in rules/resilience block:\n%s", out)
	}
	for _, want := range []string{"route_claude", "openai-failover"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("expected %q in passthrough output:\n%s", want, out)
		}
	}
}
