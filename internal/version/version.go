// Package version exposes the build-time version string embedded in Sluice
// binaries. Override at link time:
//
//	go build -ldflags "-X github.com/andyjmorgan/slipspace-gateway/internal/version.Version=v0.1.0"
package version

// Version is the build-time version string. Defaults to "dev" for source builds.
var Version = "dev"
