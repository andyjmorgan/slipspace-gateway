package controlplane

import (
	"encoding/json"
	"net/http"
	"time"
)

// Liveness status values derived from a gateway's last-seen age.
const (
	StatusOnline  = "online"
	StatusStale   = "stale"
	StatusOffline = "offline"
)

// GatewayView is the JSON shape the console reads from GET /api/v1/fleet.
type GatewayView struct {
	ID string `json:"id"`

	Version string `json:"version"`

	Labels map[string]string `json:"labels,omitempty"`

	CachedConfigHashes []string `json:"cached_config_hashes,omitempty"`

	RegisteredAt string `json:"registered_at"`

	LastSeen string `json:"last_seen"`

	// Status is online | stale | offline, derived from LastSeen age against
	// the handler's thresholds at request time.
	Status string `json:"status"`
}

// FleetHTTPHandler serves the read-only fleet registry for the console:
// GET /api/v1/fleet -> [GatewayView]. Liveness is computed from LastSeen
// against staleAfter / offlineAfter at request time, so a gateway that stops
// heartbeating ages through online -> stale -> offline without any writer.
type FleetHTTPHandler struct {
	reg          Registry
	now          func() time.Time
	staleAfter   time.Duration
	offlineAfter time.Duration
}

// NewFleetHTTPHandler builds the read API. staleAfter/offlineAfter are the age
// thresholds for the derived status (offlineAfter should exceed staleAfter).
func NewFleetHTTPHandler(reg Registry, staleAfter, offlineAfter time.Duration) *FleetHTTPHandler {
	return &FleetHTTPHandler{
		reg:          reg,
		now:          func() time.Time { return time.Now().UTC() },
		staleAfter:   staleAfter,
		offlineAfter: offlineAfter,
	}
}

func (h *FleetHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	gws, err := h.reg.List(r.Context())
	if err != nil {
		http.Error(w, "registry error", http.StatusInternalServerError)
		return
	}
	now := h.now()
	out := make([]GatewayView, 0, len(gws))
	for _, g := range gws {
		out = append(out, GatewayView{
			ID:                 g.ID,
			Version:            g.Version,
			Labels:             g.Labels,
			CachedConfigHashes: g.CachedConfigHashes,
			RegisteredAt:       g.RegisteredAt.Format(time.RFC3339),
			LastSeen:           g.LastSeen.Format(time.RFC3339),
			Status:             livenessStatus(now.Sub(g.LastSeen), h.staleAfter, h.offlineAfter),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func livenessStatus(age, staleAfter, offlineAfter time.Duration) string {
	switch {
	case age >= offlineAfter:
		return StatusOffline
	case age >= staleAfter:
		return StatusStale
	default:
		return StatusOnline
	}
}
