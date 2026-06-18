package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// providerWriteBody is the POST/PUT decode target: the provider connection fields
// (flattened via the embedded contract type) plus the name. On POST the name is
// taken from the body; on PUT the URL name is authoritative and a mismatching
// body name is rejected (rename is not supported — a binding/group/credential
// references the provider by name).
type providerWriteBody struct {
	Name string `json:"name"`

	contractsconfig.Provider
}

// ProvidersCreateHandler serves POST /api/v1/config/providers. Decodes a provider
// connection, clones the snapshot, inserts it, validates + persists + swaps via
// commitClone. With ?dry_run=true it validates against a clone and returns the
// PreviewResult without persisting.
//
//   - 201 Created on success, body = ProviderDetail
//   - 200 OK on a dry-run, body = PreviewResult
//   - 400 on parse failure / empty name / empty body
//   - 409 Conflict when the provider name already exists
//   - 422 when the post-mutation Validate fails (body = RuleValidationError)
//   - 500 on disk write failure
//   - 503 when the admin write surface is disabled (no store / config dir)
func ProvidersCreateHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		name, be, ok := decodeProviderBody(w, r.Body, "")
		if !ok {
			return
		}
		if name == "" {
			http.Error(w, "provider name is required", http.StatusBadRequest)
			return
		}

		snap := store.Snapshot()
		if _, exists := snap.Providers[name]; exists {
			writeJSONStatus(w, http.StatusConflict, ConflictError{
				Error: "provider already exists",
				Name:  name,
			})
			return
		}

		mutate := func(c *config.ResolvedConfig) {
			if c.Providers == nil {
				c.Providers = contractsconfig.ProvidersConfig{}
			}
			c.Providers[name] = be
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
		writeJSON(w, providerDetailFromContract(name, store.Snapshot().Providers[name]))
	})
}

// ProvidersReplaceHandler serves PUT /api/v1/config/providers/{name}. The URL name
// is authoritative; a body name must match it (rename is rejected with 409).
//
//   - 200 OK on success, body = ProviderDetail (or PreviewResult on dry-run)
//   - 400 on parse failure / empty path
//   - 404 when no provider with the URL name exists
//   - 409 on a rename attempt
//   - 422 / 500 / 503 as for create
func ProvidersReplaceHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		urlName := providerNameFromPath(r)
		if urlName == "" {
			http.NotFound(w, r)
			return
		}
		name, be, ok := decodeProviderBody(w, r.Body, urlName)
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
		if _, found := snap.Providers[urlName]; !found {
			http.NotFound(w, r)
			return
		}

		mutate := func(c *config.ResolvedConfig) { c.Providers[urlName] = be }
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
		writeJSON(w, providerDetailFromContract(urlName, store.Snapshot().Providers[urlName]))
	})
}

// ProvidersDeleteHandler serves DELETE /api/v1/config/providers/{name}. Refuses
// with 409 when the provider is still referenced by a binding, group target, or
// configuration credential — the referrers are named so the operator knows what
// to unbind first, and the post-delete state is guaranteed to pass Validate.
//
//   - 204 No Content on success
//   - 404 when no provider with the URL name exists
//   - 409 Conflict (UsedBy populated) when the provider is still referenced
//   - 500 / 503 as above
func ProvidersDeleteHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		name := providerNameFromPath(r)
		if name == "" {
			http.NotFound(w, r)
			return
		}

		snap := store.Snapshot()
		if _, found := snap.Providers[name]; !found {
			http.NotFound(w, r)
			return
		}

		if usedBy := referrersToProvider(snap, name); len(usedBy) > 0 {
			writeJSONStatus(w, http.StatusConflict, ConflictError{
				Error:  "provider is referenced by one or more bindings, groups, or credentials",
				Name:   name,
				UsedBy: usedBy,
			})
			return
		}

		clone := snap.Clone()
		delete(clone.Providers, name)
		if err := commitClone(clone, configDir, store); err != nil {
			writeMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// providerNameFromPath extracts the {name} segment from a providers path.
func providerNameFromPath(r *http.Request) string {
	name := strings.TrimPrefix(r.URL.Path, pathProviders)
	return strings.TrimSuffix(name, "/")
}

// decodeProviderBody reads a providerWriteBody from body. When urlName is set
// (PUT) and the body omits a name, the URL name is adopted so a body that
// carries only connection fields is accepted.
func decodeProviderBody(w http.ResponseWriter, body io.Reader, urlName string) (string, contractsconfig.Provider, bool) {
	limited := http.MaxBytesReader(w, io.NopCloser(body), maxConfigBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		http.Error(w, "request body too large or unreadable: "+err.Error(), http.StatusBadRequest)
		return "", contractsconfig.Provider{}, false
	}
	if len(raw) == 0 {
		http.Error(w, "request body is empty", http.StatusBadRequest)
		return "", contractsconfig.Provider{}, false
	}
	var wb providerWriteBody
	if err := json.Unmarshal(raw, &wb); err != nil {
		http.Error(w, "invalid provider JSON: "+err.Error(), http.StatusBadRequest)
		return "", contractsconfig.Provider{}, false
	}
	if wb.Name == "" {
		wb.Name = urlName
	}
	return wb.Name, wb.Provider, true
}
