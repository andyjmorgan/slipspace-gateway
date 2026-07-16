package config

import (
	"context"
	"errors"
	"strings"
	"testing"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
)

// fxAdvisorsPolicy is a policy file carrying an advisors block plus a
// configuration whose agent_routing references it.
const fxAdvisorsPolicy = `advisors:
  arb:
    endpoint: https://arbiter.example.com/api/v1/advise/route
    hmac_secret_file: /etc/slipspace/advisor.secret
    gateway_id: gw-office
configurations:
  prod:
    credentials:
      openai: sk-test
    bindings:
      - protocol: chat
        models: ["gpt-*"]
        provider: openai
    agent_routing:
      advisor: arb
      allow_models: ["cheap-candidate-a", "cheap-candidate-b"]
api_keys:
  - secret: sk_live_x
    name: k1
    configuration: prod
    enabled: true
`

// writeAdvisorsDir lays down a loadable dir with the given policy body.
func writeAdvisorsDir(t *testing.T, policy string) string {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"providers.yaml": fxProviders,
		"policy.yaml":    policy,
	})
	return dir
}

func TestLoad_AdvisorsAndAgentRouting(t *testing.T) {
	dir := writeAdvisorsDir(t, fxAdvisorsPolicy)
	rc := loadDir(t, dir)

	adv, ok := rc.Advisors["arb"]
	if !ok {
		t.Fatalf("advisor arb not loaded: %+v", rc.Advisors)
	}
	if adv.Endpoint != "https://arbiter.example.com/api/v1/advise/route" {
		t.Errorf("endpoint = %q", adv.Endpoint)
	}
	if adv.HMACSecretFile != "/etc/slipspace/advisor.secret" {
		t.Errorf("hmac_secret_file = %q", adv.HMACSecretFile)
	}
	if adv.GatewayID != "gw-office" {
		t.Errorf("gateway_id = %q", adv.GatewayID)
	}

	ar := rc.Configurations["prod"].AgentRouting
	if ar == nil {
		t.Fatal("agent_routing not loaded")
	}
	if ar.Advisor != "arb" {
		t.Errorf("agent_routing advisor = %q, want arb", ar.Advisor)
	}
	if len(ar.AllowModels) != 2 || ar.AllowModels[0] != "cheap-candidate-a" {
		t.Errorf("allow_models = %v", ar.AllowModels)
	}
	if got := rc.SourceFiles["advisors"]; got != "policy.yaml" {
		t.Errorf("SourceFiles[advisors] = %q, want policy.yaml", got)
	}
}

func TestLoad_AgentRoutingUnknownAdvisor(t *testing.T) {
	policy := strings.Replace(fxAdvisorsPolicy, "advisor: arb", "advisor: ghost", 1)
	dir := writeAdvisorsDir(t, policy)
	_, err := Load(context.Background(), dir)
	if err == nil {
		t.Fatal("Load with unknown advisor reference: want error")
	}
	if !strings.Contains(err.Error(), "advisor") {
		t.Errorf("err = %v, want mention of advisor", err)
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("err = %v, want ErrValidation", err)
	}
}

func TestLoad_AgentRoutingEmptyAllowModels(t *testing.T) {
	policy := strings.Replace(fxAdvisorsPolicy,
		`allow_models: ["cheap-candidate-a", "cheap-candidate-b"]`, "allow_models: []", 1)
	dir := writeAdvisorsDir(t, policy)
	_, err := Load(context.Background(), dir)
	if err == nil {
		t.Fatal("Load with empty allow_models: want error")
	}
	if !strings.Contains(err.Error(), "allow_models") {
		t.Errorf("err = %v, want mention of allow_models", err)
	}
}

func TestLoad_AdvisorInvalid(t *testing.T) {
	cases := []struct {
		name    string
		replace [2]string // old, new applied to fxAdvisorsPolicy
		wantErr string
	}{
		{
			name:    "missing endpoint",
			replace: [2]string{"    endpoint: https://arbiter.example.com/api/v1/advise/route\n", ""},
			wantErr: "endpoint",
		},
		{
			name:    "unparseable endpoint",
			replace: [2]string{"https://arbiter.example.com/api/v1/advise/route", "not-a-url"},
			wantErr: "endpoint",
		},
		{
			name:    "missing hmac_secret_file",
			replace: [2]string{"    hmac_secret_file: /etc/slipspace/advisor.secret\n", ""},
			wantErr: "hmac_secret_file",
		},
		{
			name:    "missing gateway_id",
			replace: [2]string{"    gateway_id: gw-office\n", ""},
			wantErr: "gateway_id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := strings.Replace(fxAdvisorsPolicy, tc.replace[0], tc.replace[1], 1)
			if policy == fxAdvisorsPolicy {
				t.Fatalf("fixture replace %q had no effect", tc.replace[0])
			}
			dir := writeAdvisorsDir(t, policy)
			_, err := Load(context.Background(), dir)
			if err == nil {
				t.Fatal("want error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want contains %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoad_DuplicateAdvisorsBlock(t *testing.T) {
	dir := writeAdvisorsDir(t, fxAdvisorsPolicy)
	writeFiles(t, dir, map[string]string{
		"extra.yaml": `advisors:
  other:
    endpoint: https://other.example.com/advise
    hmac_secret_file: /etc/slipspace/other.secret
    gateway_id: gw-other
`,
	})
	_, err := Load(context.Background(), dir)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("err = %v, want ErrDuplicateKey", err)
	}
}

func TestClone_DeepCopiesAdvisorsAndAgentRouting(t *testing.T) {
	dir := writeAdvisorsDir(t, fxAdvisorsPolicy)
	rc := loadDir(t, dir)
	clone := rc.Clone()

	// Mutate the clone's advisors map: edit one entry, add another.
	adv := clone.Advisors["arb"]
	adv.Endpoint = "https://mutated.example.com/advise"
	clone.Advisors["arb"] = adv
	clone.Advisors["extra"] = contractsconfig.Advisor{ //nolint:gosec // test fixture path, not a real credential
		Endpoint:       "https://extra.example.com/advise",
		HMACSecretFile: "/etc/slipspace/extra.secret",
		GatewayID:      "gw-extra",
	}

	// Mutate the clone's agent_routing in place (pointer + slice).
	car := clone.Configurations["prod"].AgentRouting
	car.Advisor = "extra"
	car.AllowModels[0] = "mutated-model-internal"

	if got := rc.Advisors["arb"].Endpoint; got != "https://arbiter.example.com/api/v1/advise/route" {
		t.Errorf("original advisor endpoint mutated: %q", got)
	}
	if _, ok := rc.Advisors["extra"]; ok {
		t.Error("advisor added to clone leaked into original")
	}
	oar := rc.Configurations["prod"].AgentRouting
	if oar.Advisor != "arb" {
		t.Errorf("original agent_routing advisor mutated: %q", oar.Advisor)
	}
	if oar.AllowModels[0] != "cheap-candidate-a" {
		t.Errorf("original allow_models mutated: %v", oar.AllowModels)
	}
}

func TestWriteConfig_AdvisorsRoundTrip(t *testing.T) {
	dir := writeAdvisorsDir(t, fxAdvisorsPolicy)
	rc := loadDir(t, dir)

	if err := WriteConfig(dir, rc); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	// The advisors block re-emits into its source file, not dropped.
	policyBytes := string(readFile(t, dir, "policy.yaml"))
	if !strings.Contains(policyBytes, "advisors:") || !strings.Contains(policyBytes, "arb") {
		t.Errorf("advisors block missing from rewritten policy.yaml:\n%s", policyBytes)
	}
	providersBytes := string(readFile(t, dir, "providers.yaml"))
	if strings.Contains(providersBytes, "advisors:") {
		t.Errorf("advisors block leaked into providers.yaml")
	}

	got := loadDir(t, dir)
	adv, ok := got.Advisors["arb"]
	if !ok {
		t.Fatalf("advisors lost on round-trip: %+v", got.Advisors)
	}
	if adv.Endpoint != "https://arbiter.example.com/api/v1/advise/route" ||
		adv.HMACSecretFile != "/etc/slipspace/advisor.secret" ||
		adv.GatewayID != "gw-office" {
		t.Errorf("advisor fields lost on round-trip: %+v", adv)
	}
	ar := got.Configurations["prod"].AgentRouting
	if ar == nil || ar.Advisor != "arb" || len(ar.AllowModels) != 2 {
		t.Errorf("agent_routing lost on round-trip: %+v", ar)
	}
}

func TestWriteConfig_AdvisorEditLandsInSourceFile(t *testing.T) {
	dir := writeAdvisorsDir(t, fxAdvisorsPolicy)
	rc := loadDir(t, dir)

	clone := rc.Clone()
	adv := clone.Advisors["arb"]
	adv.Endpoint = "https://moved-advisor.example.com/advise"
	clone.Advisors["arb"] = adv
	if err := clone.RevalidateAndIndex(); err != nil {
		t.Fatalf("revalidate: %v", err)
	}
	if err := WriteConfig(dir, clone); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	policyBytes := string(readFile(t, dir, "policy.yaml"))
	if !strings.Contains(policyBytes, "moved-advisor.example.com") {
		t.Errorf("edited advisor did not land in policy.yaml")
	}
	if got := loadDir(t, dir).Advisors["arb"].Endpoint; got != "https://moved-advisor.example.com/advise" {
		t.Errorf("reloaded advisor endpoint = %q, want moved", got)
	}
}
