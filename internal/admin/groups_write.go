package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	"github.com/andyjmorgan/sluice-gateway/internal/config"
)

// pathGroups is the trailing-slash prefix for a single group's routes.
const pathGroups = "/api/v1/config/groups/"

// groupView is the read + write wire shape for one group: the editable contract
// Group plus its name (the map key). Groups carry no secrets, so the contract
// type serialises directly. The richer live-circuit-breaker projection stays on
// /api/v1/policies; this surface is the editable shape.
type groupView struct {
	Name string `json:"name"`

	contractsconfig.Group
}

// GroupsListHandler serves GET /api/v1/config/groups — the editable groups in
// name order.
func GroupsListHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		out := make([]groupView, 0, len(resolved.Groups))
		for name, g := range resolved.Groups {
			out = append(out, groupView{Name: name, Group: g})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		writeJSON(w, out)
	})
}

// GroupDetailHandler serves GET /api/v1/config/groups/{name}.
func GroupDetailHandler(store *config.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved := snapshot(store)
		if resolved == nil {
			http.Error(w, "config unavailable", http.StatusServiceUnavailable)
			return
		}
		name := groupNameFromPath(r)
		if name == "" {
			http.NotFound(w, r)
			return
		}
		g, ok := resolved.Groups[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, groupView{Name: name, Group: g})
	})
}

// GroupsCreateHandler serves POST /api/v1/config/groups. ?dry_run=true validates
// without persisting.
//
//   - 201 Created (body = groupView) / 200 dry-run (PreviewResult)
//   - 400 parse / empty name / empty body
//   - 409 when the group name already exists
//   - 422 on validation failure (e.g. no targets, target backend unknown)
//   - 500 / 503
func GroupsCreateHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		name, g, ok := decodeGroupBody(w, r.Body, "")
		if !ok {
			return
		}
		if name == "" {
			http.Error(w, "group name is required", http.StatusBadRequest)
			return
		}

		snap := store.Snapshot()
		if _, exists := snap.Groups[name]; exists {
			writeJSONStatus(w, http.StatusConflict, ConflictError{Error: "group already exists", Name: name})
			return
		}

		mutate := func(c *config.ResolvedConfigV2) {
			if c.Groups == nil {
				c.Groups = contractsconfig.GroupsConfig{}
			}
			c.Groups[name] = g
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
		writeJSON(w, groupView{Name: name, Group: store.Snapshot().Groups[name]})
	})
}

// GroupsReplaceHandler serves PUT /api/v1/config/groups/{name}. Rename rejected.
func GroupsReplaceHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		urlName := groupNameFromPath(r)
		if urlName == "" {
			http.NotFound(w, r)
			return
		}
		name, g, ok := decodeGroupBody(w, r.Body, urlName)
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
		if _, found := snap.Groups[urlName]; !found {
			http.NotFound(w, r)
			return
		}

		mutate := func(c *config.ResolvedConfigV2) { c.Groups[urlName] = g }
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
		writeJSON(w, groupView{Name: urlName, Group: store.Snapshot().Groups[urlName]})
	})
}

// GroupsDeleteHandler serves DELETE /api/v1/config/groups/{name}. Refuses with
// 409 when a binding still targets the group.
func GroupsDeleteHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		name := groupNameFromPath(r)
		if name == "" {
			http.NotFound(w, r)
			return
		}

		snap := store.Snapshot()
		if _, found := snap.Groups[name]; !found {
			http.NotFound(w, r)
			return
		}

		if usedBy := referrersToGroup(snap, name); len(usedBy) > 0 {
			writeJSONStatus(w, http.StatusConflict, ConflictError{
				Error:  "group is referenced by one or more bindings",
				Name:   name,
				UsedBy: usedBy,
			})
			return
		}

		clone := snap.Clone()
		delete(clone.Groups, name)
		if err := commitClone(clone, configDir, store); err != nil {
			writeMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func groupNameFromPath(r *http.Request) string {
	return strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, pathGroups), "/")
}

func decodeGroupBody(w http.ResponseWriter, body io.Reader, urlName string) (string, contractsconfig.Group, bool) {
	limited := http.MaxBytesReader(w, io.NopCloser(body), maxConfigBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		http.Error(w, "request body too large or unreadable: "+err.Error(), http.StatusBadRequest)
		return "", contractsconfig.Group{}, false
	}
	if len(raw) == 0 {
		http.Error(w, "request body is empty", http.StatusBadRequest)
		return "", contractsconfig.Group{}, false
	}
	var gv groupView
	if err := json.Unmarshal(raw, &gv); err != nil {
		http.Error(w, "invalid group JSON: "+err.Error(), http.StatusBadRequest)
		return "", contractsconfig.Group{}, false
	}
	if gv.Name == "" {
		gv.Name = urlName
	}
	return gv.Name, gv.Group, true
}
