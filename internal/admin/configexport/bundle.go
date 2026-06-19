package configexport

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// File is one redacted YAML file ready for either the tabbed inspector view or
// the ZIP bundle. Content carries the post-redaction YAML bytes as a string
// (the SPA renders it inside a <pre>), SizeBytes is the byte length for the
// MANIFEST listing and the UI's per-file metadata.
type File struct {
	// Name is the basename of the file on disk (e.g. "policy.yaml").
	Name string

	// Content is the redacted YAML, ready to render.
	Content string

	// SizeBytes is len(Content). Carried alongside so the SPA need not
	// recompute it (and the manifest listing stays consistent with the
	// JSON payload).
	SizeBytes int
}

// ManifestInfo carries the gateway-side metadata embedded in the MANIFEST.txt
// header of every export bundle. Every field is informational — there is no
// validator at the read side, but a missing field shows as an empty value in
// the manifest, which is operator-visible and obvious.
type ManifestInfo struct {
	// Version is the gateway build version (internal/version.Version).
	Version string

	// Hostname is os.Hostname() at the export call site. Helps an operator
	// reconstruct which pod produced a given snapshot.
	Hostname string

	// ConfigDir is the SLIPSPACE_CONFIG_DIR the export read from. Useful when
	// multiple gateway pods serve different config trees.
	ConfigDir string

	// Timestamp is the wall-clock moment the export was generated.
	Timestamp time.Time
}

// LoadRedacted enumerates the accepted YAML files in dir, redacts each, and
// returns them sorted by filename. Backs both the per-file tabs view and the
// ZIP download, so the two surfaces never disagree about what an operator is
// looking at.
func LoadRedacted(dir string) ([]File, error) {
	paths, err := config.ListConfigFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("configexport: list config files: %w", err)
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]File, 0, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(paths[name])
		if err != nil {
			return nil, fmt.Errorf("configexport: read %s: %w", name, err)
		}
		redacted, err := Redact(raw)
		if err != nil {
			return nil, fmt.Errorf("configexport: redact %s: %w", name, err)
		}
		out = append(out, File{
			Name:      name,
			Content:   string(redacted),
			SizeBytes: len(redacted),
		})
	}
	return out, nil
}

// WriteZip writes files plus a synthesised MANIFEST.txt header into w as a ZIP
// archive. The MANIFEST always appears as the first entry so a consumer
// listing the archive sees it without scanning. Files are written in the
// order provided — callers should pass the slice from LoadRedacted to keep
// ordering stable across the tabbed inspector and the downloaded bundle.
//
// archive/zip buffers entry bytes through deflate + bufio internally, so for
// typical config files (a few KB) the entire archive stays in memory until
// zw.Close flushes everything to w. A failure in the underlying writer
// therefore surfaces at Close — that's why this function does not separately
// wrap mid-entry write errors; the Close path catches them with sufficient
// context.
func WriteZip(w io.Writer, files []File, info ManifestInfo) error {
	zw := zip.NewWriter(w)
	now := time.Now().UTC()

	entries := make([]File, 0, len(files)+1)
	manifest := buildManifest(info, files)
	entries = append(entries, File{Name: "MANIFEST.txt", Content: manifest, SizeBytes: len(manifest)})
	entries = append(entries, files...)

	for _, e := range entries {
		ew, err := zw.CreateHeader(&zip.FileHeader{
			Name:     e.Name,
			Method:   zip.Deflate,
			Modified: now,
		})
		if err != nil {
			return fmt.Errorf("configexport: zip create %s: %w", e.Name, err)
		}
		if _, err := ew.Write([]byte(e.Content)); err != nil {
			return fmt.Errorf("configexport: zip write %s: %w", e.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("configexport: zip close: %w", err)
	}
	return nil
}

// buildManifest renders the human-readable header file that sits at the root
// of every export bundle. Format is fixed: a short banner, key/value lines
// for the metadata, a blank line, then a "Files:" listing. Format changes
// here are visible to operators reading old exports — change with care.
func buildManifest(info ManifestInfo, files []File) string {
	var sb strings.Builder
	sb.WriteString("SlipSpace Gateway Configuration Export\n")
	ts := info.Timestamp.UTC().Format(time.RFC3339)
	fmt.Fprintf(&sb, "Generated:   %s\n", ts)
	fmt.Fprintf(&sb, "Version:     %s\n", info.Version)
	fmt.Fprintf(&sb, "Hostname:    %s\n", info.Hostname)
	fmt.Fprintf(&sb, "Config dir:  %s\n", info.ConfigDir)
	sb.WriteString("Secrets:     redacted (***)\n")
	sb.WriteString("\nFiles:\n")
	for _, f := range files {
		fmt.Fprintf(&sb, "  %-20s %d bytes\n", f.Name, f.SizeBytes)
	}
	return sb.String()
}
