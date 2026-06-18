package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// pathConnectors is the trailing-slash prefix for a single connector's routes.
const pathConnectors = "/api/v1/config/connectors/"

// Connectors carry no plaintext secrets: cloud + webhook credentials are
// secret_ref indirections (env:NAME / file:/path) resolved at runtime, so the
// contract type serialises directly on read and decodes directly on write —
// no mask/write-back is needed. ConnectorsConfig is a slice (each entry carries
// its own Name), unlike the map-keyed providers/groups.

// ConnectorsListHandler serves GET /api/v1/config/connectors in name order.
func ConnectorsListHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		out := make([]contractsconfig.Connector, len(resolved.Connectors))
		copy(out, resolved.Connectors)
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		writeJSON(w, out)
	})
}

// ConnectorDetailHandler serves GET /api/v1/config/connectors/{name}.
func ConnectorDetailHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		name := connectorNameFromPath(r)
		if name == "" {
			http.NotFound(w, r)
			return
		}
		idx, found := indexOfConnector(resolved.Connectors, name)
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, resolved.Connectors[idx])
	})
}

// ConnectorsCreateHandler serves POST /api/v1/config/connectors.
//
//   - 201 Created (body = Connector) / 200 dry-run
//   - 400 parse / empty name / empty body
//   - 409 when the connector name already exists
//   - 422 on validation failure (bad type, missing required field for the type)
//   - 500 / 503
func ConnectorsCreateHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		conn, ok := decodeConnectorBody(w, r.Body, "")
		if !ok {
			return
		}
		if conn.Name == "" {
			http.Error(w, "connector name is required", http.StatusBadRequest)
			return
		}

		snap := store.Snapshot()
		if _, exists := indexOfConnector(snap.Connectors, conn.Name); exists {
			writeJSONStatus(w, http.StatusConflict, ConflictError{Error: "connector already exists", Name: conn.Name})
			return
		}

		mutate := func(c *config.ResolvedConfig) { c.Connectors = append(c.Connectors, conn) }
		if isDryRun(r) {
			writeJSON(w, previewMutation(snap, mutate))
			return
		}

		clone := snap.Clone()
		mutate(clone)
		if err := commitClone(clone, configDir, store); err != nil {
			writeMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, conn)
	})
}

// ConnectorsReplaceHandler serves PUT /api/v1/config/connectors/{name}. Rename
// rejected (a configuration's connector_bindings reference it by name).
func ConnectorsReplaceHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		urlName := connectorNameFromPath(r)
		if urlName == "" {
			http.NotFound(w, r)
			return
		}
		conn, ok := decodeConnectorBody(w, r.Body, urlName)
		if !ok {
			return
		}
		if conn.Name != urlName {
			writeJSONStatus(w, http.StatusConflict, ConflictError{
				Error: "rename not supported; body.name must match URL name",
				Name:  conn.Name,
			})
			return
		}

		snap := store.Snapshot()
		idx, found := indexOfConnector(snap.Connectors, urlName)
		if !found {
			http.NotFound(w, r)
			return
		}

		mutate := func(c *config.ResolvedConfig) { c.Connectors[idx] = conn }
		if isDryRun(r) {
			writeJSON(w, previewMutation(snap, mutate))
			return
		}

		clone := snap.Clone()
		mutate(clone)
		if err := commitClone(clone, configDir, store); err != nil {
			writeMutationError(w, err)
			return
		}
		writeJSON(w, conn)
	})
}

// ConnectorsDeleteHandler serves DELETE /api/v1/config/connectors/{name}.
// Refuses with 409 when a configuration's connector_bindings reference it.
func ConnectorsDeleteHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		name := connectorNameFromPath(r)
		if name == "" {
			http.NotFound(w, r)
			return
		}

		snap := store.Snapshot()
		idx, found := indexOfConnector(snap.Connectors, name)
		if !found {
			http.NotFound(w, r)
			return
		}

		if usedBy := referrersToConnector(snap, name); len(usedBy) > 0 {
			writeJSONStatus(w, http.StatusConflict, ConflictError{
				Error:  "connector is referenced by one or more configurations",
				Name:   name,
				UsedBy: usedBy,
			})
			return
		}

		clone := snap.Clone()
		clone.Connectors = append(clone.Connectors[:idx], clone.Connectors[idx+1:]...)
		if err := commitClone(clone, configDir, store); err != nil {
			writeMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func connectorNameFromPath(r *http.Request) string {
	return strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, pathConnectors), "/")
}

func indexOfConnector(conns contractsconfig.ConnectorsConfig, name string) (int, bool) {
	for i := range conns {
		if conns[i].Name == name {
			return i, true
		}
	}
	return -1, false
}

func decodeConnectorBody(w http.ResponseWriter, body io.Reader, urlName string) (contractsconfig.Connector, bool) {
	limited := http.MaxBytesReader(w, io.NopCloser(body), maxConfigBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		http.Error(w, "request body too large or unreadable: "+err.Error(), http.StatusBadRequest)
		return contractsconfig.Connector{}, false
	}
	if len(raw) == 0 {
		http.Error(w, "request body is empty", http.StatusBadRequest)
		return contractsconfig.Connector{}, false
	}
	var conn contractsconfig.Connector
	if err := json.Unmarshal(raw, &conn); err != nil {
		http.Error(w, "invalid connector JSON: "+err.Error(), http.StatusBadRequest)
		return contractsconfig.Connector{}, false
	}
	if conn.Name == "" {
		conn.Name = urlName
	}
	return conn, true
}
