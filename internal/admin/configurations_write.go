package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	contractsconfig "github.com/andyjmorgan/slipspace-gateway/contracts/config"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// configurationWriteBody is the POST/PUT decode target for a configuration.
//
// Credentials is map[provider]*secret with write-back-if-delivered semantics: a
// nil value (JSON null, the form for an unchanged masked secret) keeps the
// stored credential for that provider; a non-nil value sets it, including "" for
// a no-credential provider. A provider absent from the map is dropped (full
// replace of which providers carry a credential).
//
// There is deliberately no api_keys field — keys are managed solely through the
// dedicated /api-keys endpoints and are never accepted in a configuration
// payload. An api_keys field in the JSON is ignored.
type configurationWriteBody struct {
	Name string `json:"name"`

	Credentials map[string]*string `json:"credentials,omitempty"`

	Bindings []contractsconfig.Binding `json:"bindings,omitempty"`

	PassthroughBindings []contractsconfig.PassthroughBinding `json:"passthrough_bindings,omitempty"`

	RuleNames []string `json:"rule_names,omitempty"`

	Tags map[string]string `json:"tags,omitempty"`

	ConnectorBindings []contractsconfig.ConnectorBinding `json:"connector_bindings,omitempty"`
}

// buildConfiguration assembles the contract configuration from the write body,
// merging each credential against the existing stored value (mergeSecret) so a
// masked round-trip never wipes a secret.
func buildConfiguration(body configurationWriteBody, existing contractsconfig.Configuration) contractsconfig.Configuration {
	out := contractsconfig.Configuration{
		Bindings:            body.Bindings,
		PassthroughBindings: body.PassthroughBindings,
		RuleNames:           body.RuleNames,
		Tags:                body.Tags,
		ConnectorBindings:   body.ConnectorBindings,
	}
	if len(body.Credentials) > 0 {
		out.Credentials = make(map[string]string, len(body.Credentials))
		for provider, incoming := range body.Credentials {
			out.Credentials[provider] = mergeSecret(incoming, existing.Credentials[provider])
		}
	}
	return out
}

// ConfigurationsCreateHandler serves POST /api/v1/config/configurations.
//
//   - 201 Created (body = redacted ConfigurationDetail) / 200 dry-run
//   - 400 parse / empty name / empty body
//   - 409 when the configuration name already exists
//   - 422 on validation failure (unknown provider credential, bad binding, …)
//   - 500 / 503
func ConfigurationsCreateHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		body, ok := decodeConfigurationBody(w, r.Body, "")
		if !ok {
			return
		}
		if body.Name == "" {
			http.Error(w, "configuration name is required", http.StatusBadRequest)
			return
		}

		snap := store.Snapshot()
		if _, exists := snap.Configurations[body.Name]; exists {
			writeJSONStatus(w, http.StatusConflict, ConflictError{Error: "configuration already exists", Name: body.Name})
			return
		}

		cfg := buildConfiguration(body, contractsconfig.Configuration{})
		mutate := func(c *config.ResolvedConfig) {
			if c.Configurations == nil {
				c.Configurations = map[string]contractsconfig.Configuration{}
			}
			c.Configurations[body.Name] = cfg
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
		writeJSON(w, configurationDetailView(body.Name, store.Snapshot()))
	})
}

// ConfigurationsReplaceHandler serves PUT /api/v1/config/configurations/{name}.
// Credentials merge against the existing configuration (masked round-trip
// preserves stored secrets). Rename rejected.
func ConfigurationsReplaceHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		urlName := configurationNameFromPath(r)
		if urlName == "" {
			http.NotFound(w, r)
			return
		}
		body, ok := decodeConfigurationBody(w, r.Body, urlName)
		if !ok {
			return
		}
		if body.Name != urlName {
			writeJSONStatus(w, http.StatusConflict, ConflictError{
				Error: "rename not supported; body.name must match URL name",
				Name:  body.Name,
			})
			return
		}

		snap := store.Snapshot()
		existing, found := snap.Configurations[urlName]
		if !found {
			http.NotFound(w, r)
			return
		}

		cfg := buildConfiguration(body, existing)
		mutate := func(c *config.ResolvedConfig) { c.Configurations[urlName] = cfg }
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
		writeJSON(w, configurationDetailView(urlName, store.Snapshot()))
	})
}

// ConfigurationsDeleteHandler serves DELETE /api/v1/config/configurations/{name}.
// Refuses with 409 when an api_key still references the configuration (the keys
// must be reassigned or removed first). Deleting the last configuration fails
// validation (422) — there must always be at least one.
func ConfigurationsDeleteHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		name := configurationNameFromPath(r)
		if name == "" {
			http.NotFound(w, r)
			return
		}

		snap := store.Snapshot()
		if _, found := snap.Configurations[name]; !found {
			http.NotFound(w, r)
			return
		}

		if usedBy := referrersToConfiguration(snap, name); len(usedBy) > 0 {
			writeJSONStatus(w, http.StatusConflict, ConflictError{
				Error:  "configuration is referenced by one or more api keys",
				Name:   name,
				UsedBy: usedBy,
			})
			return
		}

		clone := snap.Clone()
		delete(clone.Configurations, name)
		if err := commitClone(clone, configDir, store); err != nil {
			writeMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func configurationNameFromPath(r *http.Request) string {
	return strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, pathConfigurations), "/")
}

func decodeConfigurationBody(w http.ResponseWriter, body io.Reader, urlName string) (configurationWriteBody, bool) {
	limited := http.MaxBytesReader(w, io.NopCloser(body), maxConfigBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		http.Error(w, "request body too large or unreadable: "+err.Error(), http.StatusBadRequest)
		return configurationWriteBody{}, false
	}
	if len(raw) == 0 {
		http.Error(w, "request body is empty", http.StatusBadRequest)
		return configurationWriteBody{}, false
	}
	var b configurationWriteBody
	if err := json.Unmarshal(raw, &b); err != nil {
		http.Error(w, "invalid configuration JSON: "+err.Error(), http.StatusBadRequest)
		return configurationWriteBody{}, false
	}
	if b.Name == "" {
		b.Name = urlName
	}
	return b, true
}
