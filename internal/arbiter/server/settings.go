package server

import (
	"net/http"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/slipspace-gateway/contracts/admin"
)

// handleSettings serves the running service's applied config, read-only and with
// secrets redacted, so an operator can confirm what is in effect (listeners,
// content caps, the scanner block, the gateway registry) without seeing a
// credential. The redaction happens once at construction (config.Config.Redacted
// → WithAppliedConfig); this handler only re-shapes the snapshot into JSON.
//
// The Config type carries yaml tags, not json tags, so it is round-tripped
// through YAML into a generic tree first: the response keys then match the
// snake_case YAML the operator wrote, and the console can offer a faithful
// JSON⇄YAML toggle over the same shape.
func (s *Server) handleSettings(w http.ResponseWriter, _ *http.Request) {
	raw, err := yaml.Marshal(s.appliedConfig)
	if err != nil {
		s.queryError(w, "marshal settings", err)
		return
	}
	var tree map[string]any
	if err := yaml.Unmarshal(raw, &tree); err != nil {
		s.queryError(w, "decode settings", err)
		return
	}
	writeJSON(w, http.StatusOK, admin.SettingsResponse{Config: tree})
}
