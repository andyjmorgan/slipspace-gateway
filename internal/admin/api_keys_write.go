package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// pathAPIKeys is the trailing-slash prefix for a single api-key's routes.
const pathAPIKeys = "/api/v1/config/api-keys/" //nolint:gosec // URL path, not a credential

// api_keys are a first-class resource managed ONLY through these dedicated
// endpoints — never embedded in a configuration payload. A key is addressed by
// a ref that matches its minted UUID first, then falls back to its name (so a
// hand-authored key with no id yet stays addressable); the secret is never an
// addressable identifier, so it never appears in a URL. The plaintext secret
// leaves the gateway exactly once, in the mint (POST) response.

// APIKeyListItem is the redacted list/detail row for one api-key.
type APIKeyListItem struct {
	ID *uuid.UUID `json:"id,omitempty"`

	Name string `json:"name"`

	Configuration string `json:"configuration"`

	Enabled bool `json:"enabled"`

	Secret RedactedSecret `json:"secret"`
}

func apiKeyListItem(k contractsconfig.APIKey) APIKeyListItem {
	return APIKeyListItem{
		ID:            k.ID,
		Name:          k.Name,
		Configuration: k.Configuration,
		Enabled:       k.Enabled,
		Secret:        redact(k.Secret),
	}
}

// APIKeysListHandler serves GET /api/v1/config/api-keys — every key, redacted,
// in name order.
func APIKeysListHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		out := make([]APIKeyListItem, 0, len(resolved.APIKeys))
		for _, k := range resolved.APIKeys {
			out = append(out, apiKeyListItem(k))
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		writeJSON(w, out)
	})
}

// APIKeyDetailHandler serves GET /api/v1/config/api-keys/{id} (redacted).
func APIKeyDetailHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		ref := apiKeyRefFromPath(r)
		if ref == "" {
			http.NotFound(w, r)
			return
		}
		idx, found := apiKeyIndexByRef(resolved.APIKeys, ref)
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, apiKeyListItem(resolved.APIKeys[idx]))
	})
}

type apiKeyCreateBody struct {
	Name string `json:"name"`

	Configuration string `json:"configuration"`

	Enabled *bool `json:"enabled,omitempty"`
}

// APIKeysCreateHandler serves POST /api/v1/config/api-keys. The gateway mints
// the secret (sk_live_…) and returns it in plaintext exactly once; every
// subsequent read is redacted.
//
//   - 201 Created (body = APIKeyReveal, plaintext secret) / 200 dry-run
//   - 400 parse / empty name / empty body
//   - 409 when the name already exists
//   - 422 when the configuration does not exist
//   - 500 / 503
func APIKeysCreateHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		raw, ok := readLimitedBody(w, r.Body)
		if !ok {
			return
		}
		var body apiKeyCreateBody
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, "invalid api key JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if body.Name == "" {
			http.Error(w, "api key name is required", http.StatusBadRequest)
			return
		}

		snap := store.Snapshot()
		if apiKeyNameExists(snap.APIKeys, body.Name) {
			writeJSONStatus(w, http.StatusConflict, ConflictError{Error: "api key name already exists", Name: body.Name})
			return
		}

		secret, err := mintAPIKeySecret()
		if err != nil {
			http.Error(w, "could not mint secret: "+err.Error(), http.StatusInternalServerError)
			return
		}
		id, err := mintAPIKeyID()
		if err != nil {
			http.Error(w, "could not mint id: "+err.Error(), http.StatusInternalServerError)
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		key := contractsconfig.APIKey{
			ID:            &id,
			Secret:        secret,
			Name:          body.Name,
			Configuration: body.Configuration,
			Enabled:       enabled,
		}
		mutate := func(c *config.ResolvedConfigV2) { c.APIKeys = append(c.APIKeys, key) }
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
		writeJSON(w, APIKeyReveal{
			Name:          key.Name,
			Secret:        key.Secret,
			Enabled:       key.Enabled,
			Configuration: key.Configuration,
		})
	})
}

type apiKeyReplaceBody struct {
	Name string `json:"name"`

	Configuration string `json:"configuration"`

	Enabled bool `json:"enabled"`
}

// APIKeysReplaceHandler serves PUT /api/v1/config/api-keys/{id}. The secret is
// immutable (rotation is a deliberate re-mint, not a PUT); rename is rejected
// (use PATCH). Replaces configuration + enabled.
func APIKeysReplaceHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		ref := apiKeyRefFromPath(r)
		if ref == "" {
			http.NotFound(w, r)
			return
		}
		raw, ok := readLimitedBody(w, r.Body)
		if !ok {
			return
		}
		var body apiKeyReplaceBody
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, "invalid api key JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		snap := store.Snapshot()
		idx, found := apiKeyIndexByRef(snap.APIKeys, ref)
		if !found {
			http.NotFound(w, r)
			return
		}
		updated := snap.APIKeys[idx]
		if body.Name != "" && body.Name != updated.Name {
			writeJSONStatus(w, http.StatusConflict, ConflictError{
				Error: "rename not supported on PUT; use PATCH",
				Name:  body.Name,
			})
			return
		}
		updated.Configuration = body.Configuration
		updated.Enabled = body.Enabled

		mutate := func(c *config.ResolvedConfigV2) { c.APIKeys[idx] = updated }
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
		writeJSON(w, apiKeyListItem(updated))
	})
}

type apiKeyPatchBody struct {
	Name *string `json:"name,omitempty"`

	Configuration *string `json:"configuration,omitempty"`

	Enabled *bool `json:"enabled,omitempty"`
}

// APIKeysPatchHandler serves PATCH /api/v1/config/api-keys/{id} — the partial
// update: toggle Enabled (the everyday off-switch), rename, or reassign the
// configuration. Omitted fields are unchanged; the secret is never touched. A
// rename to a name another key already holds is rejected with 409.
func APIKeysPatchHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		ref := apiKeyRefFromPath(r)
		if ref == "" {
			http.NotFound(w, r)
			return
		}
		raw, ok := readLimitedBody(w, r.Body)
		if !ok {
			return
		}
		var body apiKeyPatchBody
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, "invalid api key JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		snap := store.Snapshot()
		idx, found := apiKeyIndexByRef(snap.APIKeys, ref)
		if !found {
			http.NotFound(w, r)
			return
		}
		updated := snap.APIKeys[idx]
		if body.Name != nil && *body.Name != updated.Name {
			if apiKeyNameExists(snap.APIKeys, *body.Name) {
				writeJSONStatus(w, http.StatusConflict, ConflictError{Error: "api key name already exists", Name: *body.Name})
				return
			}
			updated.Name = *body.Name
		}
		if body.Configuration != nil {
			updated.Configuration = *body.Configuration
		}
		if body.Enabled != nil {
			updated.Enabled = *body.Enabled
		}

		mutate := func(c *config.ResolvedConfigV2) { c.APIKeys[idx] = updated }
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
		writeJSON(w, apiKeyListItem(updated))
	})
}

// APIKeysDeleteHandler serves DELETE /api/v1/config/api-keys/{id}. The console
// gates this behind a destructive-action warning; PATCH-disable is the
// reversible off-switch. Nothing references an api-key, so there is no
// referential-integrity guard.
func APIKeysDeleteHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		ref := apiKeyRefFromPath(r)
		if ref == "" {
			http.NotFound(w, r)
			return
		}
		snap := store.Snapshot()
		idx, found := apiKeyIndexByRef(snap.APIKeys, ref)
		if !found {
			http.NotFound(w, r)
			return
		}

		clone := snap.Clone()
		clone.APIKeys = append(clone.APIKeys[:idx], clone.APIKeys[idx+1:]...)
		if err := commitClone(clone, configDir, store); err != nil {
			writeMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func apiKeyRefFromPath(r *http.Request) string {
	return strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, pathAPIKeys), "/")
}

// apiKeyIndexByRef resolves a path ref to an api-key index: a key whose minted
// UUID stringifies to ref wins; otherwise a key whose Name equals ref. The
// UUID-first order means an id always addresses exactly one key even if a name
// happens to collide with it.
func apiKeyIndexByRef(keys contractsconfig.APIKeysConfig, ref string) (int, bool) {
	for i := range keys {
		if keys[i].ID != nil && keys[i].ID.String() == ref {
			return i, true
		}
	}
	for i := range keys {
		if keys[i].Name == ref {
			return i, true
		}
	}
	return -1, false
}

// apiKeyNameExists reports whether a key with exactly this name exists — used
// for the name-uniqueness checks on create and rename (names stay unique so the
// ref name-fallback is unambiguous).
func apiKeyNameExists(keys contractsconfig.APIKeysConfig, name string) bool {
	for i := range keys {
		if keys[i].Name == name {
			return true
		}
	}
	return false
}

// mintAPIKeyID returns a fresh random UUID for a newly created key.
func mintAPIKeyID() (uuid.UUID, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("api key id mint: %w", err)
	}
	return id, nil
}

// mintAPIKeySecret generates a fresh Sluice-issued production key:
// "sk_live_" + 32 random bytes hex-encoded.
func mintAPIKeySecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("api key mint: %w", err)
	}
	return "sk_live_" + hex.EncodeToString(b[:]), nil
}

// readLimitedBody reads the request body under the shared size cap, surfacing
// the too-large / empty cases as 400.
func readLimitedBody(w http.ResponseWriter, body io.Reader) ([]byte, bool) {
	limited := http.MaxBytesReader(w, io.NopCloser(body), maxConfigBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		http.Error(w, "request body too large or unreadable: "+err.Error(), http.StatusBadRequest)
		return nil, false
	}
	if len(raw) == 0 {
		http.Error(w, "request body is empty", http.StatusBadRequest)
		return nil, false
	}
	return raw, true
}
