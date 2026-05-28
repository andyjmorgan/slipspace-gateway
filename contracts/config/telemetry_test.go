package config_test

import (
	"testing"

	"gopkg.in/yaml.v3"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

func TestTelemetry_YAMLRoundTrip_AllFieldsPresent(t *testing.T) {
	body := `
content_capture:
  messages_max_bytes: 16384
  system_instructions_max_bytes: 8192
  tool_definitions_max_bytes: 131072
`
	var tel contractsconfig.Telemetry
	if err := yaml.Unmarshal([]byte(body), &tel); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	caps := tel.ContentCapture
	if caps.MessagesMaxBytes == nil || *caps.MessagesMaxBytes != 16384 {
		t.Errorf("MessagesMaxBytes = %v, want &16384", caps.MessagesMaxBytes)
	}
	if caps.SystemInstructionsMaxBytes == nil || *caps.SystemInstructionsMaxBytes != 8192 {
		t.Errorf("SystemInstructionsMaxBytes = %v, want &8192", caps.SystemInstructionsMaxBytes)
	}
	if caps.ToolDefinitionsMaxBytes == nil || *caps.ToolDefinitionsMaxBytes != 131072 {
		t.Errorf("ToolDefinitionsMaxBytes = %v, want &131072", caps.ToolDefinitionsMaxBytes)
	}
}

func TestTelemetry_YAMLRoundTrip_ExplicitZeroSurvives(t *testing.T) {
	body := `
content_capture:
  messages_max_bytes: 0
  system_instructions_max_bytes: 0
  tool_definitions_max_bytes: 0
`
	var tel contractsconfig.Telemetry
	if err := yaml.Unmarshal([]byte(body), &tel); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	caps := tel.ContentCapture
	if caps.MessagesMaxBytes == nil || *caps.MessagesMaxBytes != 0 {
		t.Errorf("MessagesMaxBytes = %v, want &0 (explicit zero must survive)", caps.MessagesMaxBytes)
	}
	if caps.SystemInstructionsMaxBytes == nil || *caps.SystemInstructionsMaxBytes != 0 {
		t.Errorf("SystemInstructionsMaxBytes = %v, want &0", caps.SystemInstructionsMaxBytes)
	}
	if caps.ToolDefinitionsMaxBytes == nil || *caps.ToolDefinitionsMaxBytes != 0 {
		t.Errorf("ToolDefinitionsMaxBytes = %v, want &0", caps.ToolDefinitionsMaxBytes)
	}
}

func TestTelemetry_YAMLRoundTrip_AbsentFields(t *testing.T) {
	body := `content_capture: {}`
	var tel contractsconfig.Telemetry
	if err := yaml.Unmarshal([]byte(body), &tel); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	caps := tel.ContentCapture
	if caps.MessagesMaxBytes != nil {
		t.Errorf("MessagesMaxBytes = %v, want nil when absent", caps.MessagesMaxBytes)
	}
	if caps.SystemInstructionsMaxBytes != nil {
		t.Errorf("SystemInstructionsMaxBytes = %v, want nil when absent", caps.SystemInstructionsMaxBytes)
	}
	if caps.ToolDefinitionsMaxBytes != nil {
		t.Errorf("ToolDefinitionsMaxBytes = %v, want nil when absent", caps.ToolDefinitionsMaxBytes)
	}
}

func TestContentCaptureCaps_Resolve_DefaultsWhenAbsent(t *testing.T) {
	got := contractsconfig.ContentCaptureCaps{}.Resolve()
	want := contractsconfig.ResolvedContentCaps{
		Messages:           contractsconfig.DefaultMessagesMaxBytes,
		SystemInstructions: contractsconfig.DefaultSystemInstructionsMaxBytes,
		ToolDefinitions:    contractsconfig.DefaultToolDefinitionsMaxBytes,
	}
	if got != want {
		t.Errorf("Resolve() = %+v, want %+v", got, want)
	}
}

func TestContentCaptureCaps_Resolve_ExplicitZeroIsUnbounded(t *testing.T) {
	zero := 0
	caps := contractsconfig.ContentCaptureCaps{
		MessagesMaxBytes:           &zero,
		SystemInstructionsMaxBytes: &zero,
		ToolDefinitionsMaxBytes:    &zero,
	}
	got := caps.Resolve()
	want := contractsconfig.ResolvedContentCaps{Messages: 0, SystemInstructions: 0, ToolDefinitions: 0}
	if got != want {
		t.Errorf("Resolve(&0,&0,&0) = %+v, want %+v", got, want)
	}
}

func TestContentCaptureCaps_Resolve_ExplicitValues(t *testing.T) {
	m, s, td := 1024, 2048, 4096
	caps := contractsconfig.ContentCaptureCaps{
		MessagesMaxBytes:           &m,
		SystemInstructionsMaxBytes: &s,
		ToolDefinitionsMaxBytes:    &td,
	}
	got := caps.Resolve()
	want := contractsconfig.ResolvedContentCaps{Messages: 1024, SystemInstructions: 2048, ToolDefinitions: 4096}
	if got != want {
		t.Errorf("Resolve() = %+v, want %+v", got, want)
	}
}

func TestContentCaptureCaps_Resolve_PartialOverrides(t *testing.T) {
	m := 999
	caps := contractsconfig.ContentCaptureCaps{MessagesMaxBytes: &m}
	got := caps.Resolve()
	want := contractsconfig.ResolvedContentCaps{
		Messages:           999,
		SystemInstructions: contractsconfig.DefaultSystemInstructionsMaxBytes,
		ToolDefinitions:    contractsconfig.DefaultToolDefinitionsMaxBytes,
	}
	if got != want {
		t.Errorf("Resolve(partial) = %+v, want %+v", got, want)
	}
}

// Negative pointer values are folded into the default rather than acted
// on. This guards against an operator typing -1 expecting "unbounded" —
// the YAML schema reserves explicit 0 for that and a negative value
// would otherwise sail through as an arbitrary cap (negative len()
// comparisons would never trigger the truncation branch, silently
// yielding "unbounded"). Folding into the default matches the absent
// case and keeps the reporter's contract simple.
func TestContentCaptureCaps_Resolve_NegativeFoldsToDefault(t *testing.T) {
	neg := -1
	caps := contractsconfig.ContentCaptureCaps{
		MessagesMaxBytes:           &neg,
		SystemInstructionsMaxBytes: &neg,
		ToolDefinitionsMaxBytes:    &neg,
	}
	got := caps.Resolve()
	want := contractsconfig.ResolvedContentCaps{
		Messages:           contractsconfig.DefaultMessagesMaxBytes,
		SystemInstructions: contractsconfig.DefaultSystemInstructionsMaxBytes,
		ToolDefinitions:    contractsconfig.DefaultToolDefinitionsMaxBytes,
	}
	if got != want {
		t.Errorf("Resolve(negative) = %+v, want %+v", got, want)
	}
}
