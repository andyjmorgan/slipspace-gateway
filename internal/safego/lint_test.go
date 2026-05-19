package safego_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoUnsafeGoroutineLaunches walks the production source tree and
// fails if any `.go` file (excluding tests, the safego package itself,
// the e2e harness, and vendored code) launches a goroutine via a bare
// `go func()` or `go someFunc()` expression instead of going through
// `safego.Go` / `safego.Run`.
//
// The constraint is CLAUDE.md load-bearing invariant #11 / ADR-002:
// "any exception must be wrapped and if it's unrecoverable we 500,
// service stays up." A background goroutine without `defer recover()`
// crashes the entire process on panic, violating that invariant.
//
// If this test fails, the offending file + line is reported. The fix
// is either to wrap the launch with `safego.Go(name, log, meters, fn)`
// or — if the launch is in a test helper, the safego package's own
// implementation, or a third-party file — to add it to allowedFiles
// below with a justification comment.
func TestNoUnsafeGoroutineLaunches(t *testing.T) {
	t.Parallel()

	// Source root resolved relative to this test file so the walk
	// captures every Go file in the module.
	root := repoRoot(t)

	// allowedFiles is the closed set of source files permitted to
	// launch a goroutine without going through safego. Every entry
	// must carry a justification in this comment.
	allowedFiles := map[string]string{
		// safego.go IS the wrapper — it's the one site that launches
		// the recovered goroutine. By definition cannot wrap itself.
		"internal/safego/safego.go": "the wrapper itself",

		// Test harness for e2e — testcontainers + lifecycle plumbing
		// that already errors loudly on harness failure. Not a
		// production code path.
		"test/e2e/harness/harness.go": "e2e test harness",

		// observability/snapshot.go lives upstream of safego (safego
		// imports observability.Meters). The snapshotter's runLoop
		// carries its own deferred recover() that logs panics to the
		// configured slog.Logger — equivalent to what safego.Go would
		// provide.
		"internal/observability/snapshot.go": "upstream of safego, carries its own recover()",
	}

	// Skip these directory roots entirely.
	skipDirs := map[string]bool{
		".git":         true,
		".claude":      true,
		"vendor":       true,
		"test/smoke":   true, // Python
		"test/python":  true, // Python
		"node_modules": true,
	}

	// Match `go func(` or `go ident(` or `go pkg.Ident(` — anything
	// of shape `go <expression>(` at the start of an indented line.
	goLaunch := regexp.MustCompile(`(?m)^\s+go [A-Za-z_][\w\.]*\(`)

	violations := []string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip test files — they have controlled lifecycles and
		// often launch goroutines for assertion-time-only work.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := allowedFiles[rel]; ok {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // path comes from filepath.Walk under our own module root, not user input
		if err != nil {
			return err
		}
		// Scan with line numbers for reporting.
		for i, line := range strings.Split(string(data), "\n") {
			// Skip comments (gofmt keeps `//` at the same indent
			// as the code that follows; a leading `//` followed by
			// `go func` is documentation, not a launch).
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if goLaunch.MatchString(line) {
				violations = append(violations, formatViolation(rel, i+1, line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(violations) > 0 {
		t.Fatalf(
			"unwrapped goroutine launches found (%d). Wrap with safego.Go(name, log, meters, fn) "+
				"or add to allowedFiles in lint_test.go with a justification:\n%s",
			len(violations),
			strings.Join(violations, "\n"),
		)
	}
}

func formatViolation(file string, line int, src string) string {
	return strings.Join([]string{
		"  " + file + ":" + itoa(line),
		"    " + strings.TrimSpace(src),
	}, "\n")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// repoRoot walks up from the test file until it finds go.mod, then
// returns that directory. Stable across `go test ./...` and IDE
// runners — neither depends on the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from %s", dir)
		}
		dir = parent
	}
}
