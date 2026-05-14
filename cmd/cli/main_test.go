package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

func runCLI(t *testing.T, args ...string) (stdout, stderr *bytes.Buffer, code int) {
	t.Helper()
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	code = run(context.Background(), args, stdout, stderr)
	return stdout, stderr, code
}

func TestRun_NoArgs_ReturnsUsage(t *testing.T) {
	_, stderr, code := runCLI(t)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr missing usage; got %q", stderr.String())
	}
}

func TestRun_UnknownTopLevel_ReturnsUsage(t *testing.T) {
	_, stderr, code := runCLI(t, "nope")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr missing usage; got %q", stderr.String())
	}
}

func TestRun_Version_PrintsVersion(t *testing.T) {
	stdout, _, code := runCLI(t, "--version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.HasPrefix(stdout.String(), binaryName+" ") {
		t.Fatalf("stdout = %q, want prefix %q", stdout.String(), binaryName+" ")
	}
}

func TestRun_KeyWithoutSubcommand_ReturnsUsage(t *testing.T) {
	_, _, code := runCLI(t, "key")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestRun_KeyUnknownSubcommand_ReturnsUsage(t *testing.T) {
	_, _, code := runCLI(t, "key", "rotate")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestRun_ConfigWithoutSubcommand_ReturnsUsage(t *testing.T) {
	_, _, code := runCLI(t, "config")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestRun_ConfigUnknownSubcommand_ReturnsUsage(t *testing.T) {
	_, _, code := runCLI(t, "config", "wat")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestKeyNew_DefaultPrefixAndShape(t *testing.T) {
	stdout, _, code := runCLI(t, "key", "new")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) < 1 {
		t.Fatalf("no output")
	}
	key := lines[0]
	if !strings.HasPrefix(key, defaultKeyPrefix) {
		t.Fatalf("key %q missing prefix %q", key, defaultKeyPrefix)
	}
	hexPart := strings.TrimPrefix(key, defaultKeyPrefix)
	if len(hexPart) != keyRandomBytes*2 {
		t.Fatalf("hex part length = %d, want %d", len(hexPart), keyRandomBytes*2)
	}
	if !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(hexPart) {
		t.Fatalf("hex part %q contains non-hex characters", hexPart)
	}
}

func TestKeyNew_TwoCallsDiffer(t *testing.T) {
	stdout1, _, code1 := runCLI(t, "key", "new")
	stdout2, _, code2 := runCLI(t, "key", "new")
	if code1 != 0 || code2 != 0 {
		t.Fatalf("codes = %d, %d", code1, code2)
	}
	k1 := strings.Split(stdout1.String(), "\n")[0]
	k2 := strings.Split(stdout2.String(), "\n")[0]
	if k1 == k2 {
		t.Fatalf("two calls produced identical keys: %s", k1)
	}
}

func TestKeyNew_CustomPrefix(t *testing.T) {
	stdout, _, code := runCLI(t, "key", "new", "--prefix", "sk_dev_")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	key := strings.Split(stdout.String(), "\n")[0]
	if !strings.HasPrefix(key, "sk_dev_") {
		t.Fatalf("key %q missing prefix sk_dev_", key)
	}
}

func TestKeyNew_YAMLSnippet_WithLabel(t *testing.T) {
	stdout, _, code := runCLI(t, "key", "new", "--label", "my dev key", "--configuration", "dev")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	snippet := extractYAMLSnippet(t, stdout.String())
	var parsed struct {
		APIKeys []struct {
			Secret        string `yaml:"secret"`
			Name          string `yaml:"name"`
			Configuration string `yaml:"configuration"`
			Enabled       bool   `yaml:"enabled"`
		} `yaml:"api_keys"`
	}
	if err := yaml.Unmarshal([]byte(snippet), &parsed); err != nil {
		t.Fatalf("yaml unmarshal: %v\n--- snippet ---\n%s", err, snippet)
	}
	if len(parsed.APIKeys) != 1 {
		t.Fatalf("api_keys length = %d, want 1", len(parsed.APIKeys))
	}
	entry := parsed.APIKeys[0]
	if !strings.HasPrefix(entry.Secret, defaultKeyPrefix) {
		t.Fatalf("secret prefix wrong: %q", entry.Secret)
	}
	if entry.Name != "my dev key" {
		t.Fatalf("name = %q, want %q", entry.Name, "my dev key")
	}
	if entry.Configuration != "dev" {
		t.Fatalf("configuration = %q, want dev", entry.Configuration)
	}
	if !entry.Enabled {
		t.Fatalf("enabled = false, want true")
	}
}

func TestKeyNew_YAMLSnippet_WithoutLabel_OmitsNameLine(t *testing.T) {
	stdout, _, code := runCLI(t, "key", "new")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	snippet := extractYAMLSnippet(t, stdout.String())
	if strings.Contains(snippet, "name:") {
		t.Fatalf("snippet contained name: line when label omitted:\n%s", snippet)
	}

	var parsed struct {
		APIKeys []struct {
			Secret        string `yaml:"secret"`
			Configuration string `yaml:"configuration"`
			Enabled       bool   `yaml:"enabled"`
		} `yaml:"api_keys"`
	}
	if err := yaml.Unmarshal([]byte(snippet), &parsed); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if len(parsed.APIKeys) != 1 {
		t.Fatalf("api_keys length = %d, want 1", len(parsed.APIKeys))
	}
	if parsed.APIKeys[0].Configuration != defaultKeyConfiguration {
		t.Fatalf("configuration = %q, want %q", parsed.APIKeys[0].Configuration, defaultKeyConfiguration)
	}
}

func TestKeyNew_UnexpectedPositional_ReturnsUsage(t *testing.T) {
	_, _, code := runCLI(t, "key", "new", "extra")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestKeyNew_BadFlag_ReturnsUsage(t *testing.T) {
	_, _, code := runCLI(t, "key", "new", "--nope")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func extractYAMLSnippet(t *testing.T, output string) string {
	t.Helper()
	idx := strings.Index(output, "api_keys:")
	if idx < 0 {
		t.Fatalf("no api_keys: in output:\n%s", output)
	}
	return output[idx:]
}

func TestConfigValidate_HappyPath(t *testing.T) {
	repoRoot := repoRootFromCWD(t)
	dir := filepath.Join(repoRoot, "config-dev")
	stdout, _, code := runCLI(t, "config", "validate", "--dir", dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stdout=%q", code, stdout.String())
	}
	if !strings.HasPrefix(stdout.String(), "OK: ") {
		t.Fatalf("stdout missing OK prefix: %q", stdout.String())
	}
	expectedFragments := []string{
		"configuration(s)",
		"api_keys",
		"providers",
		"routes",
	}
	for _, f := range expectedFragments {
		if !strings.Contains(stdout.String(), f) {
			t.Fatalf("stdout missing %q: %s", f, stdout.String())
		}
	}
}

func TestConfigValidate_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := runCLI(t, "config", "validate", "--dir", dir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.HasPrefix(stdout.String(), "FAIL: empty_directory:") {
		t.Fatalf("stdout = %q, want FAIL: empty_directory prefix", stdout.String())
	}
}

func TestConfigValidate_DuplicateTopLevelKey(t *testing.T) {
	dir := t.TempDir()
	gatewayBlock := "gateway:\n  http:\n    bind: 0.0.0.0:8585\n"
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(gatewayBlock), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.yaml"), []byte(gatewayBlock), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := runCLI(t, "config", "validate", "--dir", dir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout=%q", code, stdout.String())
	}
	if !strings.HasPrefix(stdout.String(), "FAIL: duplicate_top_level_key:") {
		t.Fatalf("stdout = %q, want FAIL: duplicate_top_level_key prefix", stdout.String())
	}
}

func TestConfigValidate_ParseError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "api_keys.yaml"), []byte("api_keys:\n  - secret: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := runCLI(t, "config", "validate", "--dir", dir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout=%q", code, stdout.String())
	}
	if !strings.HasPrefix(stdout.String(), "FAIL: parse_error:") {
		t.Fatalf("stdout = %q, want FAIL: parse_error prefix", stdout.String())
	}
}

func TestConfigValidate_NoConfigurations(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gateway.yaml"), []byte("gateway:\n  http:\n    bind: 0.0.0.0:8585\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := runCLI(t, "config", "validate", "--dir", dir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.HasPrefix(stdout.String(), "FAIL: no_configurations:") {
		t.Fatalf("stdout = %q, want FAIL: no_configurations prefix", stdout.String())
	}
}

func TestConfigValidate_UnknownTopLevel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "weird.yaml"), []byte("nope: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := runCLI(t, "config", "validate", "--dir", dir)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.HasPrefix(stdout.String(), "FAIL: unknown_top_level_key:") {
		t.Fatalf("stdout = %q, want FAIL: unknown_top_level_key prefix", stdout.String())
	}
}

func TestConfigValidate_UnexpectedPositional_ReturnsUsage(t *testing.T) {
	_, _, code := runCLI(t, "config", "validate", "extra")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestConfigValidate_BadFlag_ReturnsUsage(t *testing.T) {
	_, _, code := runCLI(t, "config", "validate", "--nope")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestResolveConfigDir_FlagWins(t *testing.T) {
	t.Setenv(configDirEnv, "/env/path")
	got := resolveConfigDir("/flag/path")
	if got != "/flag/path" {
		t.Fatalf("got %q, want /flag/path", got)
	}
}

func TestResolveConfigDir_EnvFallback(t *testing.T) {
	t.Setenv(configDirEnv, "/env/path")
	got := resolveConfigDir("")
	if got != "/env/path" {
		t.Fatalf("got %q, want /env/path", got)
	}
}

func TestResolveConfigDir_Default(t *testing.T) {
	t.Setenv(configDirEnv, "")
	got := resolveConfigDir("")
	if got != configDirDefault {
		t.Fatalf("got %q, want %q", got, configDirDefault)
	}
}

func TestClassifyConfigErr_AllCategories(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{config.ErrEmptyDirectory, "empty_directory"},
		{config.ErrDuplicateTopLevelKey, "duplicate_top_level_key"},
		{config.ErrUnknownTopLevelKey, "unknown_top_level_key"},
		{config.ErrNoConfigurations, "no_configurations"},
		{config.ErrUnknownConfiguration, "unknown_configuration"},
		{config.ErrEndpointNotInProvider, "endpoint_not_in_provider"},
		{config.ErrMalformedAllowedEndpoint, "malformed_allowed_endpoint"},
		{config.ErrPathCollision, "path_collision"},
		{config.ErrPrefixRequiredEmpty, "prefix_required_empty"},
		{config.ErrInvalidBind, "invalid_bind"},
		{config.ErrParse, "parse_error"},
		{errors.New("random"), "other"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			wrapped := fmt.Errorf("ctx: %w", tc.err)
			if got := classifyConfigErr(wrapped); got != tc.want {
				t.Fatalf("classifyConfigErr(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestNewLogger_LevelParsing(t *testing.T) {
	for _, level := range []string{"", "info", "DEBUG", "warn", "error", "trace"} {
		l := newLogger(&bytes.Buffer{}, level)
		if l == nil {
			t.Fatalf("nil logger for level %q", level)
		}
	}
}

func repoRootFromCWD(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found from %s", cwd)
		}
		dir = parent
	}
}
