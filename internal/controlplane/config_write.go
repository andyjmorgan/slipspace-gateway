package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/andyjmorgan/sluice-gateway/internal/config"
	"github.com/andyjmorgan/sluice-gateway/internal/controlplane/configdb"
)

// maxEntityBodyBytes bounds a single entity PUT body.
const maxEntityBodyBytes = 1 << 20 // 1 MiB

// configStore is the configdb subset the admin API uses. An interface so the
// handlers are unit-testable with a hand-rolled fake; configdb.DB satisfies it.
type configStore interface {
	UpsertEntity(ctx context.Context, kind, name string, body []byte, by string) error
	GetEntity(ctx context.Context, kind, name string) (configdb.Entity, error)
	ListEntities(ctx context.Context) ([]configdb.Entity, error)
	DeleteEntity(ctx context.Context, kind, name, by string) error
	ListChanges(ctx context.Context, limit int) ([]configdb.Change, error)
	Publish(ctx context.Context, body []byte, by string) (configdb.Version, error)
	ActiveVersion(ctx context.Context) (configdb.Version, error)
	ActivateVersion(ctx context.Context, id string) error
	ListVersions(ctx context.Context) ([]configdb.VersionMeta, error)
}

// ConfigAdminHandler serves the control plane's config write API: entity CRUD,
// publish (compose + validate the whole config, then activate), version history
// + rollback, and the change audit log. Editing entities is permissive — the
// working state may be temporarily inconsistent — and publish is the gate that
// validates the composed config before it becomes the active served version.
//
// On a successful publish or activate, the served config is live-reloaded into
// liveStore so gateways pick it up on their next fetch.
//
// NOTE: this API is unauthenticated, matching the rest of the CP HTTP surface;
// it is intended for a trusted cluster network (ClusterIP, no public ingress).
// Add auth before exposing it.
type ConfigAdminHandler struct {
	store     configStore
	liveStore *config.Store
	logger    *slog.Logger
	mux       *http.ServeMux
}

// NewConfigAdminHandler builds the handler. liveStore is the config.Store the
// CP serves from (Replaced on publish/activate); may be nil to disable
// live-reload (the version is still durably published).
func NewConfigAdminHandler(store configStore, liveStore *config.Store, logger *slog.Logger) *ConfigAdminHandler {
	h := &ConfigAdminHandler{store: store, liveStore: liveStore, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/config/entities", h.listEntities)
	mux.HandleFunc("GET /api/v1/config/entities/{kind}/{name}", h.getEntity)
	mux.HandleFunc("PUT /api/v1/config/entities/{kind}/{name}", h.putEntity)
	mux.HandleFunc("DELETE /api/v1/config/entities/{kind}/{name}", h.deleteEntity)
	mux.HandleFunc("GET /api/v1/config/changes", h.listChanges)
	mux.HandleFunc("GET /api/v1/config/versions", h.listVersions)
	mux.HandleFunc("POST /api/v1/config/publish", h.publish)
	mux.HandleFunc("POST /api/v1/config/versions/{id}/activate", h.activate)
	h.mux = mux
	return h
}

func (h *ConfigAdminHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

type entityView struct {
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Body      json.RawMessage `json:"body"`
	UpdatedAt time.Time       `json:"updated_at"`
	UpdatedBy string          `json:"updated_by"`
}

func toEntityView(e configdb.Entity) entityView {
	return entityView{
		Kind:      e.Kind,
		Name:      e.Name,
		Body:      json.RawMessage(e.Body),
		UpdatedAt: e.UpdatedAt,
		UpdatedBy: e.UpdatedBy,
	}
}

func (h *ConfigAdminHandler) listEntities(w http.ResponseWriter, r *http.Request) {
	entities, err := h.store.ListEntities(r.Context())
	if err != nil {
		h.serverError(w, "list entities", err)
		return
	}
	out := make([]entityView, 0, len(entities))
	for _, e := range entities {
		out = append(out, toEntityView(e))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *ConfigAdminHandler) getEntity(w http.ResponseWriter, r *http.Request) {
	e, err := h.store.GetEntity(r.Context(), r.PathValue("kind"), r.PathValue("name"))
	if errors.Is(err, configdb.ErrEntityNotFound) {
		writeError(w, http.StatusNotFound, "entity not found")
		return
	}
	if err != nil {
		h.serverError(w, "get entity", err)
		return
	}
	writeJSON(w, http.StatusOK, toEntityView(e))
}

func (h *ConfigAdminHandler) putEntity(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	name := r.PathValue("name")

	body, err := io.ReadAll(io.LimitReader(r.Body, maxEntityBodyBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body")
		return
	}
	if len(body) > maxEntityBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "entity body too large")
		return
	}
	// Validate the body decodes into the contract type for this kind. The whole
	// composed config is validated separately at publish.
	if _, err := EntitiesToConfig([]configdb.Entity{{Kind: kind, Name: name, Body: body}}); err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+kind+" body: "+err.Error())
		return
	}
	if err := h.store.UpsertEntity(r.Context(), kind, name, body, actor(r)); err != nil {
		h.serverError(w, "upsert entity", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"kind": kind, "name": name, "status": "saved"})
}

func (h *ConfigAdminHandler) deleteEntity(w http.ResponseWriter, r *http.Request) {
	err := h.store.DeleteEntity(r.Context(), r.PathValue("kind"), r.PathValue("name"), actor(r))
	if errors.Is(err, configdb.ErrEntityNotFound) {
		writeError(w, http.StatusNotFound, "entity not found")
		return
	}
	if err != nil {
		h.serverError(w, "delete entity", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ConfigAdminHandler) listChanges(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	changes, err := h.store.ListChanges(r.Context(), limit)
	if err != nil {
		h.serverError(w, "list changes", err)
		return
	}
	writeJSON(w, http.StatusOK, changesView(changes))
}

func (h *ConfigAdminHandler) listVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := h.store.ListVersions(r.Context())
	if err != nil {
		h.serverError(w, "list versions", err)
		return
	}
	writeJSON(w, http.StatusOK, versionsView(versions))
}

func (h *ConfigAdminHandler) publish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	entities, err := h.store.ListEntities(ctx)
	if err != nil {
		h.serverError(w, "publish: list entities", err)
		return
	}
	resolved, err := EntitiesToConfig(entities)
	if err != nil {
		writeError(w, http.StatusBadRequest, "compose config: "+err.Error())
		return
	}
	body, err := config.MarshalConfig(resolved)
	if err != nil {
		h.serverError(w, "publish: marshal config", err)
		return
	}
	// The gate: the whole composed config must validate before it can activate.
	if _, err := config.ResolveClosure(body); err != nil {
		writeError(w, http.StatusBadRequest, "composed config is invalid: "+err.Error())
		return
	}
	v, err := h.store.Publish(ctx, body, actor(r))
	if err != nil {
		h.serverError(w, "publish", err)
		return
	}
	h.reload(ctx)
	writeJSON(w, http.StatusOK, map[string]string{"version": v.ID, "hash": v.Hash, "status": "published"})
}

func (h *ConfigAdminHandler) activate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := h.store.ActivateVersion(r.Context(), id)
	if errors.Is(err, configdb.ErrVersionNotFound) {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}
	if err != nil {
		h.serverError(w, "activate version", err)
		return
	}
	h.reload(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"version": id, "status": "active"})
}

// reload re-loads the active published version into the served store. A failure
// is logged but not fatal — the version is durably published; the CP picks it
// up on its next reload/restart.
func (h *ConfigAdminHandler) reload(ctx context.Context) {
	if h.liveStore == nil {
		return
	}
	active, err := h.store.ActiveVersion(ctx)
	if err != nil {
		h.logWarn("reload: read active version", err)
		return
	}
	resolved, err := config.ResolveClosure(active.Body)
	if err != nil {
		h.logWarn("reload: resolve active version", err)
		return
	}
	h.liveStore.Replace(resolved)
}

func (h *ConfigAdminHandler) serverError(w http.ResponseWriter, what string, err error) {
	h.logWarn(what, err)
	writeError(w, http.StatusInternalServerError, what)
}

func (h *ConfigAdminHandler) logWarn(msg string, err error) {
	if h.logger != nil {
		h.logger.Warn("config admin: "+msg, "err", err.Error())
	}
}

func actor(r *http.Request) string {
	if a := r.Header.Get("X-Sluice-Actor"); a != "" {
		return a
	}
	return "api"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

type versionView struct {
	ID          string    `json:"id"`
	Hash        string    `json:"hash"`
	PublishedAt time.Time `json:"published_at"`
	PublishedBy string    `json:"published_by"`
	Active      bool      `json:"active"`
}

func versionsView(versions []configdb.VersionMeta) []versionView {
	out := make([]versionView, 0, len(versions))
	for _, v := range versions {
		out = append(out, versionView{
			ID:          v.ID,
			Hash:        v.Hash,
			PublishedAt: v.PublishedAt,
			PublishedBy: v.PublishedBy,
			Active:      v.Active,
		})
	}
	return out
}

type changeView struct {
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Op        string          `json:"op"`
	OldBody   json.RawMessage `json:"old_body,omitempty"`
	NewBody   json.RawMessage `json:"new_body,omitempty"`
	ChangedBy string          `json:"changed_by"`
	ChangedAt time.Time       `json:"changed_at"`
}

func changesView(changes []configdb.Change) []changeView {
	out := make([]changeView, 0, len(changes))
	for _, c := range changes {
		out = append(out, changeView{
			Kind:      c.Kind,
			Name:      c.Name,
			Op:        c.Op,
			OldBody:   json.RawMessage(c.OldBody),
			NewBody:   json.RawMessage(c.NewBody),
			ChangedBy: c.ChangedBy,
			ChangedAt: c.ChangedAt,
		})
	}
	return out
}
