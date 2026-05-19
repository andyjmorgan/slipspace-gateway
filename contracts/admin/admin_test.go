package admin_test

import (
	"errors"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/contracts/admin"
)

func TestConfig_EffectiveBindAddr(t *testing.T) {
	cases := []struct {
		name string
		set  string
		want string
	}{
		{"empty falls back to default", "", admin.DefaultBindAddr},
		{"whitespace-only falls back", "   ", admin.DefaultBindAddr},
		{"set value passes through", "127.0.0.1:9000", "127.0.0.1:9000"},
		{"shorthand bind passes through", ":7777", ":7777"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &admin.Config{BindAddr: tc.set}
			if got := c.EffectiveBindAddr(); got != tc.want {
				t.Fatalf("EffectiveBindAddr() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConfig_ResolvePassword(t *testing.T) {
	cases := []struct {
		name string
		env  string
		yaml string
		want string
	}{
		{"env only", "from-env", "", "from-env"},
		{"yaml only", "", "from-yaml", "from-yaml"},
		{"env wins over yaml", "from-env", "from-yaml", "from-env"},
		{"whitespace env is ignored", "   ", "from-yaml", "from-yaml"},
		{"both empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(admin.EnvPassword, tc.env)
			c := &admin.Config{Password: tc.yaml}
			if got := c.ResolvePassword(); got != tc.want {
				t.Fatalf("ResolvePassword() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		cfg     admin.Config
		wantErr error
	}{
		{
			name: "disabled passes with everything empty",
			cfg:  admin.Config{Enabled: false},
		},
		{
			name: "disabled passes even with bad bind",
			cfg:  admin.Config{Enabled: false, BindAddr: "not-a-bind"},
		},
		{
			name:    "enabled without password fails",
			cfg:     admin.Config{Enabled: true},
			wantErr: admin.ErrPasswordRequired,
		},
		{
			name: "enabled with yaml password passes",
			cfg:  admin.Config{Enabled: true, Password: "p"},
		},
		{
			name: "enabled with env password passes (yaml empty)",
			env:  "from-env",
			cfg:  admin.Config{Enabled: true},
		},
		{
			name:    "enabled with bad bind fails",
			cfg:     admin.Config{Enabled: true, Password: "p", BindAddr: "garbage"},
			wantErr: admin.ErrInvalidBindAddr,
		},
		{
			name:    "enabled with non-numeric port fails",
			cfg:     admin.Config{Enabled: true, Password: "p", BindAddr: "host:notaport"},
			wantErr: admin.ErrInvalidBindAddr,
		},
		{
			name: "enabled with default bind passes",
			cfg:  admin.Config{Enabled: true, Password: "p"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(admin.EnvPassword, tc.env)
			err := tc.cfg.Validate()
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate() = %v, want errors.Is(_, %v)", err, tc.wantErr)
			}
		})
	}
}
