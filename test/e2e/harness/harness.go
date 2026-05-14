//go:build e2e

package harness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
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

// Harness owns a NATS container, a mockllm process, and a gateway process for
// the lifetime of a single test. All processes and containers are torn down
// via t.Cleanup.
type Harness struct {
	T *testing.T

	GatewayURL string
	MockLLMURL string
	NATSURL    string

	APIKey string

	HTTP *http.Client
	NATS *nats.Conn

	opts Options

	gatewayBindPort int
	promBindPort    int

	configDir string

	natsContainer testcontainers.Container

	eventBuf chan *nats.Msg
	eventSub *nats.Subscription

	mockllmCmd  *exec.Cmd
	mockllmDone chan struct{}

	gatewayCmd  *exec.Cmd
	gatewayDone chan struct{}

	gatewayExitMu  sync.Mutex
	gatewayExitErr error

	stopped bool
}

// New brings up NATS (testcontainers, JetStream enabled), the mockllm binary,
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

	h.startNATS(t)
	if err := h.startEventTap(); err != nil {
		t.Fatalf("harness: event tap: %v", err)
	}
	h.startMockLLM(t, repoRoot)
	h.startGateway(t, repoRoot)

	t.Cleanup(h.Stop)
	return h
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

	if h.eventSub != nil {
		_ = h.eventSub.Unsubscribe()
	}
	if h.NATS != nil {
		h.NATS.Close()
	}

	stopProcess(h.gatewayCmd, h.gatewayDone)
	stopProcess(h.mockllmCmd, h.mockllmDone)

	if h.natsContainer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		if err := h.natsContainer.Terminate(ctx); err != nil && h.T != nil {
			h.T.Logf("harness: terminate nats container: %v", err)
		}
	}

	if h.configDir != "" {
		_ = os.RemoveAll(h.configDir)
	}
}

func (h *Harness) startNATS(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	req := testcontainers.ContainerRequest{
		Image:        "nats:2.10",
		Cmd:          []string{"-js"},
		ExposedPorts: []string{"4222/tcp"},
		WaitingFor: wait.ForLog("Server is ready").
			WithStartupTimeout(startupTimeout),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("harness: start nats: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("harness: nats host: %v", err)
	}
	port, err := container.MappedPort(ctx, "4222/tcp")
	if err != nil {
		t.Fatalf("harness: nats port: %v", err)
	}

	h.natsContainer = container
	h.NATSURL = fmt.Sprintf("nats://%s:%s", host, port.Port())

	conn, err := nats.Connect(h.NATSURL, nats.Timeout(5*time.Second), nats.MaxReconnects(3))
	if err != nil {
		t.Fatalf("harness: nats connect %s: %v", h.NATSURL, err)
	}
	h.NATS = conn
}

func (h *Harness) startMockLLM(t *testing.T, repoRoot string) {
	t.Helper()

	port, err := freePort()
	if err != nil {
		t.Fatalf("harness: free port for mockllm: %v", err)
	}

	cmd := exec.Command("go", "run", "./cmd/mockllm", "--port", strconv.Itoa(port)) //nolint:gosec // fixed argv, repo-controlled
	cmd.Dir = repoRoot
	cmd.Stdout = newTestLogWriter(t, "mockllm")
	cmd.Stderr = newTestLogWriter(t, "mockllm")
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("harness: start mockllm: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	h.mockllmCmd = cmd
	h.mockllmDone = done
	h.MockLLMURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	if err := waitForHTTP(h.HTTP, h.MockLLMURL+"/control/state", startupTimeout); err != nil {
		stopProcess(cmd, done)
		t.Fatalf("harness: mockllm did not become ready: %v", err)
	}
}

func (h *Harness) startGateway(t *testing.T, repoRoot string) {
	t.Helper()

	gwPort, err := freePort()
	if err != nil {
		t.Fatalf("harness: alloc gateway port: %v", err)
	}
	promPort, err := freePort()
	if err != nil {
		t.Fatalf("harness: alloc prometheus port: %v", err)
	}
	h.gatewayBindPort = gwPort
	h.promBindPort = promPort

	configDir, err := h.materializeConfig(repoRoot)
	if err != nil {
		t.Fatalf("harness: materialize config: %v", err)
	}
	h.configDir = configDir

	cmd := exec.Command("go", "run", "./cmd/gateway")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), h.gatewayEnv(configDir)...)
	cmd.Stdout = newTestLogWriter(t, "gateway")
	cmd.Stderr = newTestLogWriter(t, "gateway")
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("harness: start gateway: %v", err)
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

	if err := waitForHTTP(h.HTTP, h.GatewayURL+"/healthz", startupTimeout); err != nil {
		stopProcess(cmd, done)
		t.Fatalf("harness: gateway did not become ready: %v", err)
	}
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
	return dst, nil
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
	}

	reportingOn := true
	if h.opts.ReportingEnabled != nil {
		reportingOn = *h.opts.ReportingEnabled
	}
	if reportingOn {
		env = append(env, "SLUICE_NATS_URL="+h.NATSURL)
	}

	if h.opts.StashThresholdBytes > 0 {
		env = append(env, fmt.Sprintf("SLUICE_NATS_STASH_THRESHOLD_BYTES=%d", h.opts.StashThresholdBytes))
	}
	if h.opts.DrainTimeoutSeconds > 0 {
		env = append(env, fmt.Sprintf("SLUICE_SHUTDOWN_DRAIN_SECONDS=%d", h.opts.DrainTimeoutSeconds))
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

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		return 0, err
	}
	return port, nil
}

func waitForHTTP(client *http.Client, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, url, http.NoBody)
		if err != nil {
			return fmt.Errorf("build probe request: %w", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
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
