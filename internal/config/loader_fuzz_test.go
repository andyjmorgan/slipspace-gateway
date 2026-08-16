package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoad fuzzes the YAML config loader, one of the two legs CLAUDE.md
// names explicitly ("fuzz every UnmarshalJSON + the YAML loader + route
// detection").
//
// Config files are operator-trusted for *content* — they arrive from a
// k8s Secret or a permissioned filesystem, which is why there is no
// ${VAR} expansion — but they are not trusted to be well-formed. A
// hand-edited or half-templated file is the normal failure mode, and the
// loader runs at boot before anything else, so a panic here is a crash
// loop with no config to roll back to.
//
// The property asserted is total: for arbitrary bytes, Load either
// returns a usable *ResolvedConfig or a plain error — never a panic,
// never (nil, nil), and never a non-nil config alongside an error, since
// callers switch on err and would dereference a half-built config.
func FuzzLoad(f *testing.F) {
	seeds := []string{
		"",
		"{}",
		"providers:",
		"providers: {{{not yaml",
		v2Providers,
		v2Policy,
		v2Providers + v2Policy,
		// Legacy key that must be rejected rather than ignored.
		"backends:\n  openai: { base_url: https://api.openai.com, protocols: { chat: {} } }\n",
		// Shapes that exercise the merge + index paths.
		"configurations:\n  p:\n    bindings:\n      - { protocol: chat, provider: ghost }\n",
		"api_keys:\n  - { secret: s, name: n, configuration: missing, enabled: true }\n",
		// Type confusion: right keys, wrong node kinds.
		"providers: []\n",
		"providers:\n  openai: \"a string, not a map\"\n",
		"api_keys: {}\n",
		// Deep nesting and anchors — yaml.v3 features the loader inherits.
		"a: &x\n  b: *x\n",
		"providers:\n  openai:\n    base_url: &b https://api.openai.com\n    protocols: { chat: { path: *b } }\n",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Oversized inputs only slow the fuzzer down; the loader has no
		// size-dependent behaviour worth exploring past a few KiB.
		if len(data) > 64*1024 {
			t.Skip()
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "fuzz.yaml"), data, 0o600); err != nil {
			t.Fatalf("write fuzz input: %v", err)
		}

		rc, err := Load(context.Background(), dir)
		if err != nil {
			// An error must not come with a config attached — callers
			// branch on err and would otherwise read a half-built value.
			if rc != nil {
				t.Fatalf("Load returned both a config and an error: %v", err)
			}
			return
		}
		if rc == nil {
			t.Fatal("Load returned (nil, nil)")
		}
		// A successfully loaded config is indexed and self-consistent:
		// every api-key secret resolves to a configuration that exists.
		for secret, key := range rc.SecretIndex {
			if key == nil {
				t.Fatalf("SecretIndex[%q] is nil", secret)
			}
			if _, exists := rc.Configurations[key.Configuration]; !exists {
				t.Fatalf("SecretIndex[%q] points at configuration %q, which is not in Configurations",
					secret, key.Configuration)
			}
		}
	})
}
