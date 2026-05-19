package config_test

import (
	"context"
	"errors"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/contracts/admin"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

const sampleAdminEnabledYaml = `admin:
  enabled: true
  bind_addr: 127.0.0.1:8081
  password: dev-only
`

const sampleAdminDisabledYaml = `admin:
  enabled: false
`

const sampleAdminEnvOnlyYaml = `admin:
  enabled: true
  bind_addr: 0.0.0.0:8081
`

func makeFullDirWithAdmin(t *testing.T, adminBody string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", samplePolicy)
	writeFile(t, dir, "admin.yaml", adminBody)
	return dir
}

func TestLoad_AdminBlock_Enabled(t *testing.T) {
	dir := makeFullDirWithAdmin(t, sampleAdminEnabledYaml)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if resolved.Admin == nil {
		t.Fatal("Admin block not loaded")
	}
	if !resolved.Admin.Enabled {
		t.Error("Admin.Enabled = false, want true")
	}
	if got := resolved.Admin.EffectiveBindAddr(); got != "127.0.0.1:8081" {
		t.Errorf("EffectiveBindAddr() = %q", got)
	}
	if got := resolved.Admin.ResolvePassword(); got != "dev-only" {
		t.Errorf("ResolvePassword() = %q", got)
	}
}

func TestLoad_AdminBlock_Disabled(t *testing.T) {
	dir := makeFullDirWithAdmin(t, sampleAdminDisabledYaml)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if resolved.Admin == nil {
		t.Fatal("Admin block not loaded (even when disabled, the block was present)")
	}
	if resolved.Admin.Enabled {
		t.Error("Admin.Enabled = true, want false")
	}
}

func TestLoad_AdminBlock_Absent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", samplePolicy)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if resolved.Admin != nil {
		t.Errorf("Admin = %+v, want nil when admin.yaml is absent", resolved.Admin)
	}
}

func TestLoad_AdminBlock_EnabledNoPasswordFails(t *testing.T) {
	t.Setenv(admin.EnvPassword, "")
	dir := makeFullDirWithAdmin(t, sampleAdminEnvOnlyYaml)
	_, err := config.Load(context.Background(), dir)
	if err == nil {
		t.Fatal("Load: want error, got nil")
	}
	if !errors.Is(err, admin.ErrPasswordRequired) {
		t.Fatalf("Load: want errors.Is(_, ErrPasswordRequired), got %v", err)
	}
}

func TestLoad_AdminBlock_EnvPasswordSatisfies(t *testing.T) {
	t.Setenv(admin.EnvPassword, "from-env")
	dir := makeFullDirWithAdmin(t, sampleAdminEnvOnlyYaml)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := resolved.Admin.ResolvePassword(); got != "from-env" {
		t.Errorf("ResolvePassword() = %q, want from-env", got)
	}
}

func TestLoad_AdminBlock_WrongFileForKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	// admin block placed in policy.yaml — should be rejected.
	writeFile(t, dir, "policy.yaml", samplePolicy+"\nadmin:\n  enabled: false\n")
	_, err := config.Load(context.Background(), dir)
	if err == nil {
		t.Fatal("Load: want error placing admin in policy.yaml, got nil")
	}
}
