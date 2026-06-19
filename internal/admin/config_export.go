package admin

import (
	"bytes"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/andyjmorgan/slipspace-gateway/internal/admin/configexport"
	"github.com/andyjmorgan/slipspace-gateway/internal/observability"
	"github.com/andyjmorgan/slipspace-gateway/internal/version"
)

// ConfigExportFile is the SPA-facing per-file payload returned by the
// tabbed inspector endpoint. Content carries the redacted YAML text the
// same bytes the ZIP download would include for that file.
type ConfigExportFile struct {
	Name string `json:"name"`

	Content string `json:"content"`

	SizeBytes int `json:"size_bytes"`
}

// ConfigExportFilesResponse wraps the file list so the SPA receives a
// stable JSON object even when the loader saw zero accepted files (the
// `files` field is then an empty array, never null).
type ConfigExportFilesResponse struct {
	Files []ConfigExportFile `json:"files"`
}

// ConfigExportFilesHandler returns the redacted text of every accepted YAML
// file under configDir. Backs the Settings page's tabbed inspector view.
//
// A nil/empty configDir produces 503 so the SPA can hide the section rather
// than render a partial UI; a load/redact failure surfaces as 500 with a
// short error body (full detail goes to logs, not the wire).
func ConfigExportFilesHandler(configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if configDir == "" {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		files, err := configexport.LoadRedacted(configDir)
		if err != nil {
			http.Error(w, "failed to load config export: "+err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]ConfigExportFile, 0, len(files))
		for _, f := range files {
			out = append(out, ConfigExportFile{
				Name:      f.Name,
				Content:   f.Content,
				SizeBytes: f.SizeBytes,
			})
		}
		writeJSON(w, ConfigExportFilesResponse{Files: out})
	})
}

// ConfigExportDownloadHandler streams a ZIP bundle of every accepted YAML
// file under configDir, secrets redacted, with a MANIFEST.txt header carrying
// the gateway version, hostname, configDir, generation timestamp, and a per-
// file listing. The download filename is timestamped so successive exports
// from the same operator land as distinct files in the browser's downloads.
//
// Bumps gateway.admin.config_exports.total on each call, labelled by the
// final HTTP status the response actually carried — separate from the
// InstrumentRoute counter so a regression in this surface surfaces without
// needing to filter the broader admin counter.
func ConfigExportDownloadHandler(configDir, hostname string, meters *observability.Meters) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		status := http.StatusOK
		defer func() {
			if meters != nil && meters.AdminConfigExportsTotal != nil {
				meters.AdminConfigExportsTotal.Add(ctx, 1, metric.WithAttributes(
					attribute.String("status", strconv.Itoa(status)),
				))
			}
		}()
		if configDir == "" {
			status = http.StatusServiceUnavailable
			http.Error(w, "config unavailable", status)
			return
		}
		files, err := configexport.LoadRedacted(configDir)
		if err != nil {
			status = http.StatusInternalServerError
			http.Error(w, "failed to load config: "+err.Error(), status)
			return
		}
		now := time.Now().UTC()
		info := configexport.ManifestInfo{
			Version:   version.Version,
			Hostname:  hostname,
			ConfigDir: configDir,
			Timestamp: now,
		}
		// Buffer the zip so a mid-write failure becomes a clean 500 rather
		// than a half-streamed response the browser will keep as a partial
		// file. The bundle is small (a few KB to a few hundred KB) — well
		// under the cost of a redacted snapshot.
		var buf bytes.Buffer
		if err := configexport.WriteZip(&buf, files, info); err != nil {
			status = http.StatusInternalServerError
			http.Error(w, "failed to build export: "+err.Error(), status)
			return
		}
		filename := "slipspace-config-" + now.Format("20060102T150405Z") + ".zip"
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
		_, _ = w.Write(buf.Bytes())
	})
}
