package config_test

import (
	"errors"
	"testing"
	"time"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
)

// validAdvisor returns an advisor definition passing Validate.
func validAdvisor() contractsconfig.Advisor {
	//nolint:gosec // test fixture paths, not real credentials
	return contractsconfig.Advisor{
		Endpoint:       "https://arbiter.example.com/api/v1/advise/route",
		HMACSecretFile: "/etc/slipspace/advisor.secret",
		GatewayID:      "gw-office",
	}
}

func TestAdvisor_Validate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*contractsconfig.Advisor)
		wantErr error
	}{
		{
			name:   "valid",
			mutate: func(*contractsconfig.Advisor) {},
		},
		{
			name:    "missing endpoint",
			mutate:  func(a *contractsconfig.Advisor) { a.Endpoint = "" },
			wantErr: contractsconfig.ErrAdvisorEndpoint,
		},
		{
			name:    "endpoint without scheme",
			mutate:  func(a *contractsconfig.Advisor) { a.Endpoint = "arbiter.example.com/advise" },
			wantErr: contractsconfig.ErrAdvisorEndpoint,
		},
		{
			name:    "endpoint without host",
			mutate:  func(a *contractsconfig.Advisor) { a.Endpoint = "https://" },
			wantErr: contractsconfig.ErrAdvisorEndpoint,
		},
		{
			name:    "unparseable endpoint",
			mutate:  func(a *contractsconfig.Advisor) { a.Endpoint = "://missing-scheme" },
			wantErr: contractsconfig.ErrAdvisorEndpoint,
		},
		{
			name:    "missing hmac_secret_file",
			mutate:  func(a *contractsconfig.Advisor) { a.HMACSecretFile = "" },
			wantErr: contractsconfig.ErrAdvisorSecretFile,
		},
		{
			name:    "missing gateway_id",
			mutate:  func(a *contractsconfig.Advisor) { a.GatewayID = "" },
			wantErr: contractsconfig.ErrAdvisorGatewayID,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := validAdvisor()
			tc.mutate(&a)
			err := a.Validate("arb")
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestAdvisor_Timeout(t *testing.T) {
	cases := []struct {
		name      string
		timeoutMs int
		want      time.Duration
	}{
		{"zero takes default", 0, contractsconfig.DefaultAdvisorTimeoutMs * time.Millisecond},
		{"negative takes default", -100, contractsconfig.DefaultAdvisorTimeoutMs * time.Millisecond},
		{"explicit passes through", 250, 250 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := contractsconfig.Advisor{TimeoutMs: tc.timeoutMs}
			if got := a.Timeout(); got != tc.want {
				t.Errorf("Timeout() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAgentRouting_PinTTL(t *testing.T) {
	cases := []struct {
		name string
		ar   *contractsconfig.AgentRouting
		want time.Duration
	}{
		{"nil receiver takes default", nil, contractsconfig.DefaultPinTTLSeconds * time.Second},
		{"zero takes default", &contractsconfig.AgentRouting{}, contractsconfig.DefaultPinTTLSeconds * time.Second},
		{"negative takes default", &contractsconfig.AgentRouting{PinTTLSeconds: -1}, contractsconfig.DefaultPinTTLSeconds * time.Second},
		{"explicit passes through", &contractsconfig.AgentRouting{PinTTLSeconds: 60}, 60 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ar.PinTTL(); got != tc.want {
				t.Errorf("PinTTL() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAgentRouting_ApplyWindow(t *testing.T) {
	cases := []struct {
		name string
		ar   *contractsconfig.AgentRouting
		want int
	}{
		{"nil receiver takes default", nil, contractsconfig.DefaultApplyWindowRequests},
		{"zero takes default", &contractsconfig.AgentRouting{}, contractsconfig.DefaultApplyWindowRequests},
		{"negative takes default", &contractsconfig.AgentRouting{ApplyWindowRequests: -3}, contractsconfig.DefaultApplyWindowRequests},
		{"explicit passes through", &contractsconfig.AgentRouting{ApplyWindowRequests: 7}, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ar.ApplyWindow(); got != tc.want {
				t.Errorf("ApplyWindow() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAgentRouting_ModelAllowed(t *testing.T) {
	allow := &contractsconfig.AgentRouting{AllowModels: []string{"cheap-candidate-a", "cheap-candidate-b"}}
	cases := []struct {
		name  string
		ar    *contractsconfig.AgentRouting
		model string
		want  bool
	}{
		{"nil receiver denies", nil, "cheap-candidate-a", false},
		{"listed model allowed", allow, "cheap-candidate-a", true},
		{"unlisted model denied", allow, "nomatch-internal", false},
		{"empty list denies", &contractsconfig.AgentRouting{}, "cheap-candidate-a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ar.ModelAllowed(tc.model); got != tc.want {
				t.Errorf("ModelAllowed(%q) = %t, want %t", tc.model, got, tc.want)
			}
		})
	}
}
