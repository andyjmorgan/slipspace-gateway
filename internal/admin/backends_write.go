package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// backendWriteBody is the POST/PUT decode target: the backend connection fields
// (flattened via the embedded contract type) plus the name. On POST the name is
// taken from the body; on PUT the URL name is authoritative and a mismatching
// body name is rejected (rename is not supported — a binding/group/credential
// references the backend by name).
type backendWriteBody struct {
	Name string `json:"name"`

	contractsconfig.Backend
}

// BackendsCreateHandler serves POST /api/v1/config/backends. Decodes a backend
// connection, clones the snapshot, inserts it, validates + persists + swaps via
// commitClone. With ?dry_run=true it validates against a clone and returns the
// PreviewResult without persisting.
//
//   - 201 Created on success, body = BackendDetail
//   - 200 OK on a dry-run, body = PreviewResult
//   - 400 on parse failure / empty name / empty body
//   - 409 Conflict when the backend name already exists
//   - 422 when the post-mutation Validate fails (body = RuleValidationError)
//   - 500 on disk write failure
//   - 503 when the admin write surface is disabled (no store / config dir)
func BackendsCreateHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		name, be, ok := decodeBackendBody(w, r.Body, "")
		if !ok {
			return
		}
		if name == "" {
			http.Error(w, "backend name is required", http.StatusBadRequest)
			return
		}

		snap := store.Snapshot()
		if _, exists := snap.Backends[name]; exists {
			writeJSONStatus(w, http.StatusConflict, ConflictError{
				Error: "backend already exists",
				Name:  name,
			})
			return
		}

		mutate := func(c *config.ResolvedConfigV2) {
			if c.Backends == nil {
				c.Backends = contractsconfig.BackendsConfig{}
			}
			c.Backends[name] = be
		}
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
		writeJSON(w, backendDetailFromContract(name, store.Snapshot().Backends[name]))
	})
}

// BackendsReplaceHandler serves PUT /api/v1/config/backends/{name}. The URL name
// is authoritative; a body name must match it (rename is rejected with 409).
//
//   - 200 OK on success, body = BackendDetail (or PreviewResult on dry-run)
//   - 400 on parse failure / empty path
//   - 404 when no backend with the URL name exists
//   - 409 on a rename attempt
//   - 422 / 500 / 503 as for create
func BackendsReplaceHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		urlName := backendNameFromPath(r)
		if urlName == "" {
			http.NotFound(w, r)
			return
		}
		name, be, ok := decodeBackendBody(w, r.Body, urlName)
		if !ok {
			return
		}
		if name != urlName {
			writeJSONStatus(w, http.StatusConflict, ConflictError{
				Error: "rename not supported; body.name must match URL name",
				Name:  name,
			})
			return
		}

		snap := store.Snapshot()
		if _, found := snap.Backends[urlName]; !found {
			http.NotFound(w, r)
			return
		}

		mutate := func(c *config.ResolvedConfigV2) { c.Backends[urlName] = be }
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
		writeJSON(w, backendDetailFromContract(urlName, store.Snapshot().Backends[urlName]))
	})
}

// BackendsDeleteHandler serves DELETE /api/v1/config/backends/{name}. Refuses
// with 409 when the backend is still referenced by a binding, group target, or
// configuration credential — the referrers are named so the operator knows what
// to unbind first, and the post-delete state is guaranteed to pass Validate.
//
//   - 204 No Content on success
//   - 404 when no backend with the URL name exists
//   - 409 Conflict (UsedBy populated) when the backend is still referenced
//   - 500 / 503 as above
func BackendsDeleteHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		name := backendNameFromPath(r)
		if name == "" {
			http.NotFound(w, r)
			return
		}

		snap := store.Snapshot()
		if _, found := snap.Backends[name]; !found {
			http.NotFound(w, r)
			return
		}

		if usedBy := referrersToBackend(snap, name); len(usedBy) > 0 {
			writeJSONStatus(w, http.StatusConflict, ConflictError{
				Error:  "backend is referenced by one or more bindings, groups, or credentials",
				Name:   name,
				UsedBy: usedBy,
			})
			return
		}

		clone := snap.Clone()
		delete(clone.Backends, name)
		if err := commitClone(clone, configDir, store); err != nil {
			writeMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// backendNameFromPath extracts the {name} segment from a backends path.
func backendNameFromPath(r *http.Request) string {
	name := strings.TrimPrefix(r.URL.Path, pathBackends)
	return strings.TrimSuffix(name, "/")
}

// decodeBackendBody reads a backendWriteBody from body. When urlName is set
// (PUT) and the body omits a name, the URL name is adopted so a body that
// carries only connection fields is accepted.
func decodeBackendBody(w http.ResponseWriter, body io.Reader, urlName string) (string, contractsconfig.Backend, bool) {
	limited := http.MaxBytesReader(w, io.NopCloser(body), maxConfigBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		http.Error(w, "request body too large or unreadable: "+err.Error(), http.StatusBadRequest)
		return "", contractsconfig.Backend{}, false
	}
	if len(raw) == 0 {
		http.Error(w, "request body is empty", http.StatusBadRequest)
		return "", contractsconfig.Backend{}, false
	}
	var wb backendWriteBody
	if err := json.Unmarshal(raw, &wb); err != nil {
		http.Error(w, "invalid backend JSON: "+err.Error(), http.StatusBadRequest)
		return "", contractsconfig.Backend{}, false
	}
	if wb.Name == "" {
		wb.Name = urlName
	}
	return wb.Name, wb.Backend, true
}
