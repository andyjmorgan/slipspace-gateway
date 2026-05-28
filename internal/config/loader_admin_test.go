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

const sampleAdminWithTelemetryYaml = `admin:
  enabled: false
telemetry:
  content_capture:
    messages_max_bytes: 4096
    system_instructions_max_bytes: 0
    tool_definitions_max_bytes: 8192
`

func TestLoad_TelemetryBlock_Parses(t *testing.T) {
	dir := makeFullDirWithAdmin(t, sampleAdminWithTelemetryYaml)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	caps := resolved.Telemetry.ContentCapture
	if caps.MessagesMaxBytes == nil || *caps.MessagesMaxBytes != 4096 {
		t.Errorf("MessagesMaxBytes = %v, want &4096", caps.MessagesMaxBytes)
	}
	if caps.SystemInstructionsMaxBytes == nil || *caps.SystemInstructionsMaxBytes != 0 {
		t.Errorf("SystemInstructionsMaxBytes = %v, want &0 (explicit zero)", caps.SystemInstructionsMaxBytes)
	}
	if caps.ToolDefinitionsMaxBytes == nil || *caps.ToolDefinitionsMaxBytes != 8192 {
		t.Errorf("ToolDefinitionsMaxBytes = %v, want &8192", caps.ToolDefinitionsMaxBytes)
	}

	resolvedCaps := caps.Resolve()
	if resolvedCaps.Messages != 4096 {
		t.Errorf("Resolve().Messages = %d, want 4096", resolvedCaps.Messages)
	}
	if resolvedCaps.SystemInstructions != 0 {
		t.Errorf("Resolve().SystemInstructions = %d, want 0 (unbounded)", resolvedCaps.SystemInstructions)
	}
	if resolvedCaps.ToolDefinitions != 8192 {
		t.Errorf("Resolve().ToolDefinitions = %d, want 8192", resolvedCaps.ToolDefinitions)
	}
}

func TestLoad_TelemetryBlock_AbsentResolvesToDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", samplePolicy)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	caps := resolved.Telemetry.ContentCapture.Resolve()
	if caps.Messages == 0 || caps.SystemInstructions == 0 || caps.ToolDefinitions == 0 {
		t.Errorf("Resolve() = %+v, want non-zero defaults across the board when telemetry: is absent", caps)
	}
}

func TestLoad_TelemetryBlock_WithoutAdmin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	writeFile(t, dir, "policy.yaml", samplePolicy)
	writeFile(t, dir, "admin.yaml", `telemetry:
  content_capture:
    messages_max_bytes: 2048
`)
	resolved, err := config.Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if resolved.Admin != nil {
		t.Errorf("Admin = %+v, want nil when only telemetry: is set", resolved.Admin)
	}
	if got := resolved.Telemetry.ContentCapture.MessagesMaxBytes; got == nil || *got != 2048 {
		t.Errorf("MessagesMaxBytes = %v, want &2048", got)
	}
}

func TestLoad_TelemetryBlock_WrongFileForKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "providers.yaml", sampleProviders)
	// telemetry block placed in policy.yaml — should be rejected.
	writeFile(t, dir, "policy.yaml", samplePolicy+"\ntelemetry:\n  content_capture:\n    messages_max_bytes: 1024\n")
	_, err := config.Load(context.Background(), dir)
	if err == nil {
		t.Fatal("Load: want error placing telemetry in policy.yaml, got nil")
	}
}
