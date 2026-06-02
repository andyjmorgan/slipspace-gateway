package controlplane

import (
	"context"
	"errors"
	"net/http"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// Config-drift statuses for a fleet member.
const (
	DriftCurrent = "current" // serving a closure hash the CP currently serves
	DriftStale   = "drifted" // serving a closure hash no longer current
	DriftUnknown = "unknown" // hasn't reported a config hash yet
)

type fleetLister interface {
	List(ctx context.Context) ([]Gateway, error)
}

type activeConfigReader interface {
	ActiveVersion(ctx context.Context) (configdb.Version, error)
}

// DriftHandler reports which fleet members run the latest published config. It
// compares each gateway's reported closure hash against the set of closure
// hashes the CP currently serves (one per configuration in the active version),
// so an operator can see who has drifted after a publish.
type DriftHandler struct {
	fleet  fleetLister
	active activeConfigReader
	mux    *http.ServeMux
}

// NewDriftHandler builds the handler over the fleet registry and the active
// config source.
func NewDriftHandler(fleet fleetLister, active activeConfigReader) *DriftHandler {
	h := &DriftHandler{fleet: fleet, active: active}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/fleet/drift", h.handle)
	h.mux = mux
	return h
}

func (h *DriftHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

type driftRow struct {
	GatewayID    string   `json:"gateway_id"`
	Version      string   `json:"version"`
	Status       string   `json:"status"`
	CachedHashes []string `json:"cached_hashes"`
}

func (h *DriftHandler) handle(w http.ResponseWriter, r *http.Request) {
	gateways, err := h.fleet.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list fleet")
		return
	}
	current, err := h.currentClosureHashes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve active config")
		return
	}
	rows := make([]driftRow, 0, len(gateways))
	for _, g := range gateways {
		rows = append(rows, driftRow{
			GatewayID:    g.ID,
			Version:      g.Version,
			Status:       driftStatus(g.CachedConfigHashes, current),
			CachedHashes: g.CachedConfigHashes,
		})
	}
	writeJSON(w, http.StatusOK, rows)
}

// currentClosureHashes is the set of closure hashes the CP currently serves —
// one per configuration in the active published version. Empty when nothing is
// published (every gateway then reads as drifted/unknown, which is correct).
func (h *DriftHandler) currentClosureHashes(ctx context.Context) (map[string]struct{}, error) {
	active, err := h.active.ActiveVersion(ctx)
	if errors.Is(err, configdb.ErrNoActiveConfig) {
		return map[string]struct{}{}, nil
	}
	if err != nil {
		return nil, err
	}
	resolved, err := config.ResolveClosure(active.Body)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(resolved.Configurations))
	for name := range resolved.Configurations {
		_, hash, err := config.MarshalClosure(resolved, name)
		if err != nil {
			return nil, err
		}
		out[hash] = struct{}{}
	}
	return out, nil
}

func driftStatus(cached []string, current map[string]struct{}) string {
	if len(cached) == 0 {
		return DriftUnknown
	}
	for _, h := range cached {
		if _, ok := current[h]; !ok {
			return DriftStale
		}
	}
	return DriftCurrent
}
