//go:build e2e

package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	// defaultAPIKey matches config-dev/api_keys.yaml; tests use it for the
	// managed-auth path. Not a credential — it's checked into the repo on
	// purpose so the E2E harness has a known-good handshake.
	defaultAPIKey = "sk_dev_local_development_only_not_for_production" //nolint:gosec // test fixture

	startupTimeout = 60 * time.Second
	healthInterval = 100 * time.Millisecond
	stopTimeout    = 10 * time.Second
)

// Harness owns the in-process webhook capture server, the mockllm
// process, and the gateway process for the lifetime of a single test.
// All processes are torn down via t.Cleanup.
//
// Architecture: the gateway is configured (via materializeConfig) with
// a webhook connector pointing at this harness's in-process
// httptest.Server. Every sealed-segment POST is decompressed,
// each cc.Record is translated into the legacy Envelope shape, and
// pushed to eventBuf — ExpectEvent reads from there. SLUICE_WEBHOOK_
// ALLOW_PRIVATE is set on the gateway process so the loopback target
// passes the runtime SSRF guard.
type Harness struct {
	T *testing.T

	GatewayURL string
	MockLLMURL string

	// AdminURL is the http://127.0.0.1:<port>/admin base of the
	// management console when Options.AdminEnabled is true.
	AdminURL string

	// AdminPassword is the credential the harness wrote into admin.yaml
	// for this run.
	AdminPassword string

	APIKey string

	HTTP *http.Client

	opts Options

	gatewayBindPort int
	promBindPort    int
	adminBindPort   int

	configDir string
	spoolRoot string

	// captureServer is the in-process httptest.Server that the
	// gateway's webhook connector POSTs sealed segments to.
	captureServer *httptest.Server
	captureURL    string
	captureSecret string

	eventBuf         chan Envelope
	pendingMu        sync.Mutex
	pendingEnvelopes []Envelope

	mockllmCmd  *exec.Cmd
	mockllmDone chan struct{}

	gatewayCmd  *exec.Cmd
	gatewayDone chan struct{}

	gatewayExitMu  sync.Mutex
	gatewayExitErr error

	stopped bool
}

// New brings up the in-process webhook capture server, the mockllm binary,
// and the gateway binary, waits for each to be reachable, and registers
// cleanups. It calls t.Fatalf on any startup failure.
func New(t *testing.T) *Harness {
	t.Helper()
	return NewWithOptions(t, Options{})
}

// NewWithOptions is New with non-default configuration overrides applied to
// the materialized gateway config. See [Options] for the supported knobs.
func NewWithOptions(t *testing.T, opts Options) *Harness {
	t.Helper()

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("harness: find repo root: %v", err)
	}

	h := &Harness{
		T:      t,
		APIKey: defaultAPIKey,
		HTTP:   &http.Client{Timeout: 30 * time.Second},
		opts:   opts,
	}

	h.captureSecret = randomID()
	if err := h.startCaptureServer(); err != nil {
		t.Fatalf("harness: start capture server: %v", err)
	}
	h.startMockLLM(t, repoRoot)
	h.startGateway(t, repoRoot)

	t.Cleanup(h.Stop)
	return h
}

// PromURL returns the base URL of the gateway's Prometheus scrape endpoint.
// Tests that assert metric labels survive end-to-end issue a GET against
// "<PromURL>/metrics" after a request and parse the resulting text exposition.
func (h *Harness) PromURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", h.promBindPort)
}

// SendGatewaySignal forwards sig to the gateway process group. Used by drain
// tests that need to send SIGTERM mid-request and verify in-flight completion
// before the process exits. Returns nil if the gateway process is gone.
func (h *Harness) SendGatewaySignal(sig syscall.Signal) error {
	return signalGroup(h.gatewayCmd, sig)
}

// WaitGatewayExit blocks until the gateway process exits or timeout elapses.
// Returns the gateway's exit error (nil on clean exit) or
// context.DeadlineExceeded if the process is still running when timeout
// elapses.
//
// Safe to call concurrently with Stop / from t.Cleanup: the underlying
// gatewayDone channel is closed by the harness watcher goroutine, so all
// observers see the same result.
func (h *Harness) WaitGatewayExit(timeout time.Duration) error {
	if h.gatewayDone == nil {
		return errors.New("gateway not started")
	}
	select {
	case <-h.gatewayDone:
		h.gatewayExitMu.Lock()
		defer h.gatewayExitMu.Unlock()
		return h.gatewayExitErr
	case <-time.After(timeout):
		return context.DeadlineExceeded
	}
}

// Stop tears the harness down. Safe to call more than once.
func (h *Harness) Stop() {
	if h.stopped {
		return
	}
	h.stopped = true

	stopProcess(h.gatewayCmd, h.gatewayDone)
	stopProcess(h.mockllmCmd, h.mockllmDone)

	h.shutdownCaptureServer()

	if h.configDir != "" {
		_ = os.RemoveAll(h.configDir)
	}
	if h.spoolRoot != "" {
		_ = os.RemoveAll(h.spoolRoot)
	}
}

func (h *Harness) startMockLLM(t *testing.T, repoRoot string) {
	t.Helper()

	// freePort uses listen-on-0 / close, which leaves a TOCTOU window
	// between us closing the listener and `go run ./cmd/mockllm` binding
	// it. Under parallel e2e packages that race fires often enough to be
	// a real flake — retry the whole start sequence if mockllm dies before
	// the readiness probe succeeds.
	const maxAttempts = 4
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := h.tryStartMockLLM(t, repoRoot); err == nil {
			return
		} else {
			lastErr = err
			t.Logf("harness: mockllm start attempt %d/%d failed: %v", attempt, maxAttempts, err)
		}
	}
	t.Fatalf("harness: mockllm did not start after %d attempts: %v", maxAttempts, lastErr)
}

func (h *Harness) tryStartMockLLM(t *testing.T, repoRoot string) error {
	t.Helper()

	port, err := freePort()
	if err != nil {
		return fmt.Errorf("free port for mockllm: %w", err)
	}

	bin, err := mockllmBinary(repoRoot)
	if err != nil {
		return fmt.Errorf("build mockllm: %w", err)
	}

	cmd := exec.Command(bin, "--port", strconv.Itoa(port)) //nolint:gosec // bin is a repo-built binary, fixed argv
	cmd.Dir = repoRoot
	cmd.Stdout = newTestLogWriter(t, "mockllm")
	cmd.Stderr = newTestLogWriter(t, "mockllm")
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start mockllm: %w", err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	// /control/state returns 200 with a JSON body; require == 200 so a
	// stray colliding server can't fool us into thinking mockllm is up.
	if err := waitForHTTP(h.HTTP, url+"/control/state", startupTimeout, done, exactly(200)); err != nil {
		stopProcess(cmd, done)
		return fmt.Errorf("mockllm did not become ready on port %d: %w", port, err)
	}

	// Same freePort impostor guard as the gateway: a concurrent harness on
	// the same picked port can answer /control/state with its own 200 while
	// our mockllm exits from the bind collision. A staged-then-fooled mockllm
	// is the worst case — the gateway forwards to the impostor, whose pool
	// lacks our canned responses, surfacing as "no canned response" 404s. If
	// our process exited just after readiness, re-pick a port (the caller's
	// retry loop handles the returned error).
	if subprocessExited(done, readinessConfirm) {
		stopProcess(cmd, done)
		return fmt.Errorf("mockllm exited just after readiness on port %d (impostor likely answered the probe)", port)
	}

	h.mockllmCmd = cmd
	h.mockllmDone = done
	h.MockLLMURL = url
	return nil
}

func (h *Harness) startGateway(t *testing.T, repoRoot string) {
	t.Helper()

	// Spool root + config dir are port-independent, so materialize them
	// once outside the retry loop.
	spoolRoot, err := os.MkdirTemp("", "sluice-e2e-spool-*")
	if err != nil {
		t.Fatalf("harness: tmp spool: %v", err)
	}
	h.spoolRoot = spoolRoot

	configDir, err := h.materializeConfig(repoRoot)
	if err != nil {
		t.Fatalf("harness: materialize config: %v", err)
	}
	h.configDir = configDir

	// freePort hands the gateway subprocess a port we listened-on-0 then
	// closed, leaving a TOCTOU window: a concurrently-running e2e package's
	// harness (its capture httptest.Server or its own gateway/admin/prom
	// listeners) can grab one of our three ports before the subprocess
	// binds it, and the gateway then dies with "address already in use".
	// startMockLLM already retries this exact race; the gateway path used
	// to t.Fatalf on the first collision (issue #135). Retry with fresh
	// ports on a bind collision, fail fast on any other startup error so a
	// genuine config/boot failure still surfaces immediately.
	const maxAttempts = 4
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		retryable, err := h.tryStartGateway(t, repoRoot, configDir)
		if err == nil {
			return
		}
		if !retryable || attempt == maxAttempts {
			t.Fatalf("harness: gateway did not become ready (attempt %d/%d): %v", attempt, maxAttempts, err)
		}
		t.Logf("harness: gateway start attempt %d/%d hit a port collision, retrying with fresh ports: %v", attempt, maxAttempts, err)
	}
}

// tryStartGateway allocates a fresh set of ports, starts the gateway
// subprocess, and blocks until it reports ready. It returns (retryable,
// err): retryable is true only when the subprocess died from a port-bind
// collision, which the caller resolves by re-picking ports. Any other
// failure (config error, boot panic) returns retryable=false so it surfaces
// without burning the retry budget.
func (h *Harness) tryStartGateway(t *testing.T, repoRoot, configDir string) (retryable bool, err error) {
	t.Helper()

	gwPort, err := freePort()
	if err != nil {
		return false, fmt.Errorf("alloc gateway port: %w", err)
	}
	promPort, err := freePort()
	if err != nil {
		return false, fmt.Errorf("alloc prometheus port: %w", err)
	}
	adminPort, err := freePort()
	if err != nil {
		return false, fmt.Errorf("alloc admin port: %w", err)
	}
	h.gatewayBindPort = gwPort
	h.promBindPort = promPort
	h.adminBindPort = adminPort
	if h.opts.AdminEnabled {
		password := h.opts.AdminPassword
		if password == "" {
			password = "test-password"
		}
		h.AdminPassword = password
		h.AdminURL = fmt.Sprintf("http://127.0.0.1:%d/admin", adminPort)
	}

	// admin.yaml bakes in the admin bind port (gateway + prometheus ports
	// go via env), so it must be re-written with this attempt's freshly
	// allocated port. materializeConfig wrote it once before any port was
	// picked; re-emit it here so a retry's new admin port actually lands in
	// the config the subprocess reads.
	if err := h.writeAdminYAML(configDir); err != nil {
		return false, fmt.Errorf("write admin.yaml: %w", err)
	}

	// cmd.Wait's error is just "exit status 1"; the actual "address already
	// in use" line goes to the subprocess's stderr. Watch both streams for
	// that signature so the caller can tell a port collision (retry with
	// fresh ports) from a genuine boot failure (fail fast). One shared flag,
	// two watchers — keeping a testLogWriter per stream avoids racing their
	// line buffers across stdout/stderr.
	var collided atomic.Bool
	bin, err := gatewayBinary(repoRoot)
	if err != nil {
		return false, fmt.Errorf("build gateway: %w", err)
	}
	cmd := exec.Command(bin) //nolint:gosec // bin is a repo-built binary, no args
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), h.gatewayEnv(configDir)...)
	cmd.Stdout = &portCollisionWatcher{w: newTestLogWriter(t, "gateway"), flag: &collided}
	cmd.Stderr = &portCollisionWatcher{w: newTestLogWriter(t, "gateway"), flag: &collided}
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("start gateway: %w", err)
	}

	done := make(chan struct{})
	go func() {
		err := cmd.Wait()
		h.gatewayExitMu.Lock()
		h.gatewayExitErr = err
		h.gatewayExitMu.Unlock()
		close(done)
	}()

	h.gatewayCmd = cmd
	h.gatewayDone = done
	h.GatewayURL = fmt.Sprintf("http://127.0.0.1:%d", gwPort)

	// /healthz returns exactly 200 OK when ready; tighter than the
	// default <500 so a colliding capture server (202) can't fool us.
	if err := waitForHTTP(h.HTTP, h.GatewayURL+"/healthz", startupTimeout, done, exactly(200)); err != nil {
		stopProcess(cmd, done)
		return collided.Load(), fmt.Errorf("gateway did not become ready: %w", err)
	}

	if h.opts.AdminEnabled {
		// /api/v1/auth/me requires Basic auth — accept 200 (authenticated
		// somehow) or 401 (the live admin demanding credentials), but
		// reject anything else; a 202 would mean a port collision.
		if err := waitForHTTP(h.HTTP, h.AdminURL+"/api/v1/auth/me", startupTimeout, done, oneOf(200, 401)); err != nil {
			stopProcess(cmd, done)
			return collided.Load(), fmt.Errorf("admin listener did not become ready: %w", err)
		}
	}

	// Guard the freePort TOCTOU across all three gateway listeners. Two
	// distinct failure shapes, both invisible to the readiness probes:
	//
	//   - Main-port impostor: a concurrent harness holding our picked gateway
	//     port answers /healthz with its own 200, masking that OUR gateway
	//     lost the bind race and exited. Admin-enabled tests catch this (their
	//     admin probe hits our own, uncollided admin port and fails), but
	//     admin-disabled tests only probe /healthz and would then talk to the
	//     impostor — which forwards to the impostor's mockllm and surfaces as
	//     "no canned response" 404s.
	//   - Prometheus/admin collision: those listeners start in goroutines just
	//     before the main server, and a prometheus bind failure is non-fatal —
	//     the gateway stays up and serves /healthz, so the only evidence is the
	//     "address already in use" line the watcher captured. Metrics-scraping
	//     tests then fail because nothing is on the prometheus port.
	//
	// Give the listeners a brief window to surface a late bind failure, then
	// retry with fresh ports if any listener collided (collided is reliable:
	// the goroutines log before/around the window, and once done closes
	// cmd.Wait has drained the watchers) or if our process exited outright.
	exited := subprocessExited(done, readinessConfirm)
	switch {
	case collided.Load():
		stopProcess(cmd, done)
		return true, errors.New("gateway hit a port-bind collision on a listener (gateway/prometheus/admin)")
	case exited:
		stopProcess(cmd, done)
		return false, fmt.Errorf("gateway exited just after readiness: %w", h.gatewayExit())
	}
	return false, nil
}

// readinessConfirm is how long tryStartGateway/tryStartMockLLM wait after a
// readiness probe passes to let the subprocess surface a late bind failure
// before trusting the probe. Bounds the freePort impostor window; small
// relative to startupTimeout so the happy-path tax is negligible.
const readinessConfirm = 250 * time.Millisecond

// subprocessExited reports whether done has closed within grace. Used to
// confirm a just-probed subprocess is actually ours and still running.
func subprocessExited(done <-chan struct{}, grace time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(grace):
		return false
	}
}

// gatewayExit reads the captured gateway exit error under its mutex.
func (h *Harness) gatewayExit() error {
	h.gatewayExitMu.Lock()
	defer h.gatewayExitMu.Unlock()
	return h.gatewayExitErr
}

// portCollisionWatcher tees a subprocess output stream to its wrapped
// writer while flagging whether the stream ever carried an "address already
// in use" bind error — the signature of the freePort TOCTOU race (issue
// #135). flag is shared across the stdout and stderr watchers of one
// subprocess; read it only after the subprocess has exited.
type portCollisionWatcher struct {
	w    io.Writer
	flag *atomic.Bool
}

func (w *portCollisionWatcher) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "address already in use") {
		w.flag.Store(true)
	}
	return w.w.Write(p)
}

// materializeConfig clones the policy + providers YAML from config-dev/ into
// a tmp dir and rewrites the in-tree mockllm placeholder to point at the
// harness's dynamically-assigned upstream. Server-level configuration is
// supplied to the gateway via SLUICE_* env vars (see gatewayEnv); the
// directory contains only policy.yaml + providers.yaml.
func (h *Harness) materializeConfig(repoRoot string) (string, error) {
	src := filepath.Join(repoRoot, "config-dev")
	dst, err := os.MkdirTemp("", "sluice-e2e-config-*")
	if err != nil {
		return "", fmt.Errorf("mkdir tmp config: %w", err)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return "", fmt.Errorf("read config-dev: %w", err)
	}

	mockHost := strings.TrimPrefix(h.MockLLMURL, "http://")

	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		// admin.yaml is harness-controlled per-test (free port, opt-in
		// enabled flag) so we never inherit the config-dev copy.
		if name == "admin.yaml" {
			continue
		}
		if name == "policy.yaml" && h.opts.PolicyYAML != "" {
			// Override: substitute the test-supplied policy verbatim.
			// Providers wiring still comes from config-dev/.
			content := strings.ReplaceAll(h.opts.PolicyYAML, "mockllm:5555", mockHost)
			if err := os.WriteFile(filepath.Join(dst, name), []byte(content), 0o600); err != nil { //nolint:gosec // dst is os.MkdirTemp output
				return "", fmt.Errorf("write override %s: %w", name, err)
			}
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, name)) //nolint:gosec // repo-controlled config-dev/* path
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		patched := strings.ReplaceAll(string(raw), "mockllm:5555", mockHost)
		dstFile := filepath.Join(dst, name)
		if err := os.WriteFile(dstFile, []byte(patched), 0o600); err != nil { //nolint:gosec // dst is os.MkdirTemp output
			return "", fmt.Errorf("write %s: %w", name, err)
		}
	}

	if err := h.writeAdminYAML(dst); err != nil {
		return "", err
	}
	if err := h.injectWebhookConnector(dst); err != nil {
		return "", err
	}
	return dst, nil
}

// injectWebhookConnector rewrites policy.yaml in dst to attach the
// harness's in-process webhook capture server to the `dev`
// configuration the e2e harness drives. Appends a `connectors:` block
// at EOF.
//
// We do this as a textual edit because the load path is strict about
// top-level keys but tolerates extra subkeys under each configuration;
// adding the binding via a string match keeps the harness independent
// of how config-dev/policy.yaml evolves field-by-field.
//
// The match target is the literal `tags:\n      tier: dev` block —
// the last meaningful line of the dev configuration in config-dev/.
// If that pattern changes in config-dev/, the inject silently
// no-ops and tests will time out on ExpectEvent; the failure is
// visible enough that "harness inject fell through" is the obvious
// next-thing-to-check.
func (h *Harness) injectWebhookConnector(dst string) error {
	// When the test explicitly disables reporting, skip the inject so
	// no connector binding fires and tests can assert "silence".
	if h.opts.ReportingEnabled != nil && !*h.opts.ReportingEnabled {
		return nil
	}

	policyPath := filepath.Join(dst, "policy.yaml")
	raw, err := os.ReadFile(policyPath) //nolint:gosec // dst is os.MkdirTemp output
	if err != nil {
		return fmt.Errorf("read policy.yaml: %w", err)
	}
	content := string(raw)

	// Inject the binding as the FIRST subkey under `  dev:\n` so the
	// match works against both config-dev/policy.yaml and any
	// test-supplied PolicyYAML that defines the same configuration.
	const devKey = "  dev:\n"
	const devBindingInsert = devKey + "    connector_bindings:\n      - connector: harness-webhook\n"
	content = strings.Replace(content, devKey, devBindingInsert, 1)

	// The webhook connector is a real-time, non-spooled pusher: it POSTs one
	// cc.Record JSON per request to the capture server (no rotation — that's a
	// spool concept). gateway_id rides the X-Sluice-Gateway-Id header.
	content += fmt.Sprintf(`
connectors:
  - name: harness-webhook
    type: webhook
    url: %s
    gateway_id: harness-gw
    secret_ref: env:HARNESS_WEBHOOK_SECRET
    timeout_ms: 5000
`, h.captureURL)

	return os.WriteFile(policyPath, []byte(content), 0o600) //nolint:gosec // dst is os.MkdirTemp output
}

// writeAdminYAML emits a per-test admin.yaml in dst. Disabled blocks
// still write the file so the loader's "admin.yaml exists" path is
// exercised on every run; enabled blocks bind to the harness-assigned
// free port so parallel tests don't collide.
func (h *Harness) writeAdminYAML(dst string) error {
	enabled := "false"
	if h.opts.AdminEnabled {
		enabled = "true"
	}
	body := fmt.Sprintf("admin:\n  enabled: %s\n  bind_addr: \"127.0.0.1:%d\"\n", enabled, h.adminBindPort)
	if h.opts.AdminEnabled {
		body += fmt.Sprintf("  password: %q\n", h.AdminPassword)
	}
	return os.WriteFile(filepath.Join(dst, "admin.yaml"), []byte(body), 0o600) //nolint:gosec // dst is os.MkdirTemp output
}

// gatewayEnv builds the SLUICE_* env block for the spawned gateway process.
// Options overrides (ReportingEnabled, StashThresholdBytes, DrainTimeoutSeconds)
// land here instead of in YAML mutation because the gateway sources these
// inputs from env vars after the three-plane refactor.
func (h *Harness) gatewayEnv(configDir string) []string {
	env := []string{
		"SLUICE_CONFIG_DIR=" + configDir,
		fmt.Sprintf("SLUICE_HTTP_BIND=127.0.0.1:%d", h.gatewayBindPort),
		fmt.Sprintf("SLUICE_PROMETHEUS_BIND=127.0.0.1:%d", h.promBindPort),
		"SLUICE_LOG_LEVEL=debug",
		"SLUICE_SPOOL_ROOT=" + h.spoolRoot,
		// The harness's capture httptest.Server binds to loopback;
		// flip the webhook connector's runtime SSRF guard so the
		// connector accepts a 127.0.0.1 destination.
		"SLUICE_WEBHOOK_ALLOW_PRIVATE=1",
		// HMAC key the gateway signs payloads with. Generated per
		// harness so concurrent test packages don't share state.
		"HARNESS_WEBHOOK_SECRET=" + h.captureSecret,
	}

	if h.opts.DrainTimeoutSeconds > 0 {
		env = append(env, fmt.Sprintf("SLUICE_SHUTDOWN_DRAIN_SECONDS=%d", h.opts.DrainTimeoutSeconds))
	}
	if h.opts.UpstreamResponseHeaderTimeoutSeconds > 0 {
		env = append(env, fmt.Sprintf("SLUICE_UPSTREAM_RESPONSE_HEADER_TIMEOUT_SECONDS=%d", h.opts.UpstreamResponseHeaderTimeoutSeconds))
	}
	// Tight snapshot interval so the admin dashboard reflects real
	// traffic within an e2e test's wall-clock budget. Production
	// default is 5 minutes (configured at the env var's default).
	if h.opts.AdminEnabled {
		env = append(env, "SLUICE_ADMIN_SNAPSHOT_INTERVAL_MS=200")
	}
	if h.opts.ExternalURL != "" {
		env = append(env, "SLUICE_EXTERNAL_URL="+h.opts.ExternalURL)
	}
	return env
}

// findRepoRoot walks up from this source file until it finds go.mod.
func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New("go.mod not found walking up from harness package")
}

// exactly returns a waitForHTTP accept predicate that matches a single
// status code; use it when the endpoint has a known good response and
// any other status implies we're talking to the wrong process.
func exactly(want int) func(int) bool {
	return func(s int) bool { return s == want }
}

// oneOf returns a waitForHTTP accept predicate that matches any of the
// supplied status codes.
func oneOf(want ...int) func(int) bool {
	return func(s int) bool {
		for _, w := range want {
			if s == w {
				return true
			}
		}
		return false
	}
}

// Pre-built subprocess binaries, compiled once per test binary (the Once
// is package-global, and each e2e package is its own `go test` process, so
// this builds gateway + mockllm exactly once per package rather than once
// per harness). Spawning `go run ./cmd/<x>` per test re-runs a compile+link
// cycle every time; across the full 14-package parallel e2e suite that is
// 100+ link steps competing for CPU, which is the dominant driver of the
// startup/readiness flake family (#101/#135/#152/#156). Building once and
// exec'ing the binary removes that contention.
var (
	gatewayBinOnce sync.Once
	gatewayBinPath string
	gatewayBinErr  error

	mockllmBinOnce sync.Once
	mockllmBinPath string
	mockllmBinErr  error
)

// gatewayBinary returns the path to the once-built gateway binary.
func gatewayBinary(repoRoot string) (string, error) {
	gatewayBinOnce.Do(func() {
		gatewayBinPath, gatewayBinErr = buildBinary(repoRoot, "gateway")
	})
	return gatewayBinPath, gatewayBinErr
}

// mockllmBinary returns the path to the once-built mockllm binary.
func mockllmBinary(repoRoot string) (string, error) {
	mockllmBinOnce.Do(func() {
		mockllmBinPath, mockllmBinErr = buildBinary(repoRoot, "mockllm")
	})
	return mockllmBinPath, mockllmBinErr
}

// buildBinary compiles ./cmd/<pkg> to a per-package temp dir without the
// race detector (matching the previous `go run` behaviour — the subprocess
// was never race-instrumented) and returns the binary path. The temp dir is
// unique per build so concurrent packages don't clobber a shared output.
func buildBinary(repoRoot, pkg string) (string, error) {
	dir, err := os.MkdirTemp("", "sluice-e2e-bin-*")
	if err != nil {
		return "", fmt.Errorf("mkdir tmp bin: %w", err)
	}
	out := filepath.Join(dir, pkg)
	cmd := exec.Command("go", "build", "-o", out, "./cmd/"+pkg) //nolint:gosec // fixed argv, repo-controlled
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build ./cmd/%s: %w: %s", pkg, err, stderr.String())
	}
	return out, nil
}

// claimedPorts records every port freePort has handed out in this test
// process so it never hands out the same one twice. The resilience package
// (and any package with many t.Parallel tests) allocates ports from many
// goroutines at once; without this, two concurrent freePort calls in the
// same process can read back the same kernel-recycled ephemeral port and
// collide. This kills the intra-process arm of the freePort TOCTOU
// (#135 family); the cross-process arm is handled by the readinessConfirm
// impostor guard in tryStartGateway/tryStartMockLLM.
var (
	claimedPortsMu sync.Mutex
	claimedPorts   = map[int]bool{}
)

func freePort() (int, error) {
	const maxTries = 50
	for try := 0; try < maxTries; try++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 0, err
		}
		port := l.Addr().(*net.TCPAddr).Port
		if err := l.Close(); err != nil {
			return 0, err
		}
		claimedPortsMu.Lock()
		seen := claimedPorts[port]
		if !seen {
			claimedPorts[port] = true
		}
		claimedPortsMu.Unlock()
		if !seen {
			return port, nil
		}
	}
	return 0, errors.New("freePort: exhausted tries finding an unclaimed port")
}

// waitForHTTP polls url until the server responds with a status the
// caller is willing to accept, the subprocess we're probing exits, or
// timeout elapses.
//
// `procDone`, when non-nil, is the close-on-exit channel of the
// subprocess we're waiting on. If it closes before the probe succeeds
// we abort — the URL we're hitting is no longer "our" process. This
// matters because freePort() has a TOCTOU window: a concurrent
// e2e harness's httptest.Server can grab the port between our
// listen-on-0/close and the subprocess's bind(), at which point the
// subprocess exits but the colliding listener (e.g. the capture
// server's catch-all that returns 202) happily answers our probe and
// fools us into thinking the gateway is ready.
//
// `accept` predicates the status code; pass nil to accept any status
// strictly under 500 (the old loose behaviour, kept for callers
// where the response shape is genuinely tolerant — e.g. mockllm's
// /control/state pre-startup may transiently 4xx).
func waitForHTTP(client *http.Client, url string, timeout time.Duration, procDone <-chan struct{}, accept func(int) bool) error {
	if accept == nil {
		accept = func(s int) bool { return s < 500 }
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		select {
		case <-procDone:
			if lastErr == nil {
				lastErr = errors.New("subprocess exited before becoming ready")
			} else {
				lastErr = fmt.Errorf("subprocess exited before becoming ready (last probe err: %w)", lastErr)
			}
			return fmt.Errorf("probe %s: %w", url, lastErr)
		default:
		}
		req, err := http.NewRequest(http.MethodGet, url, http.NoBody)
		if err != nil {
			return fmt.Errorf("build probe request: %w", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if accept(resp.StatusCode) {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(healthInterval)
	}
	if lastErr == nil {
		lastErr = errors.New("timeout with no recorded error")
	}
	return fmt.Errorf("probe %s: %w", url, lastErr)
}

// stopProcess sends SIGTERM, then SIGKILL if the process does not exit within
// stopTimeout. Safe with nil cmd. done must be closed (not just signalled)
// when the underlying process has exited.
func stopProcess(cmd *exec.Cmd, done <-chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	_ = signalGroup(cmd, syscall.SIGTERM)

	select {
	case <-done:
		return
	case <-time.After(stopTimeout):
	}

	_ = signalGroup(cmd, syscall.SIGKILL)
	select {
	case <-done:
	case <-time.After(stopTimeout):
	}
}

// testLogWriter prefixes each line with the subsystem name and forwards to t.Log.
// Tests get the upstream binary's stdout/stderr inline with their own output,
// only when the test fails (testing.T buffers normally).
type testLogWriter struct {
	t      *testing.T
	prefix string
	buf    []byte
}

func newTestLogWriter(t *testing.T, prefix string) *testLogWriter {
	return &testLogWriter{t: t, prefix: prefix}
}

func (w *testLogWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := -1
		for i, b := range w.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		w.t.Logf("[%s] %s", w.prefix, line)
	}
	return len(p), nil
}
