package main

import (
	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
)

// testDefaultCaps returns the resolved content-capture caps that mirror
// the in-tree YAML defaults. Used by reporter tests that exercise the
// real cap behaviour without re-encoding the constants at every call
// site. Tests that want unbounded fields construct
// contractsconfig.ResolvedContentCaps{} directly.
func testDefaultCaps() contractsconfig.ResolvedContentCaps {
	return contractsconfig.ContentCaptureCaps{}.Resolve()
}
