package admin

import (
	"net/http"
	"sort"

	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// maxConfigBodyBytes caps an inbound config-write JSON body. A provider with a
// dozen protocols or a configuration with many bindings is still a few KiB;
// 256 KiB is generous headroom against a runaway client forcing the gateway to
// buffer arbitrary memory.
const maxConfigBodyBytes = 256 * 1024

// ConflictError is the 409 wire shape shared by every config-write resource —
// name-already-exists on POST, rename-not-supported on PUT, or
// still-referenced on DELETE. UsedBy is populated only in the referenced case.
type ConflictError struct {
	Error string `json:"error"`

	Name string `json:"name,omitempty"`

	UsedBy []string `json:"used_by,omitempty"`
}

// isDryRun reports whether the request asked for a validate-only preview
// (?dry_run=true) — the candidate is validated against a clone and the result
// reported, but the live snapshot is never swapped and nothing is persisted.
func isDryRun(r *http.Request) bool {
	return r.URL.Query().Get("dry_run") == "true"
}

// This file holds the shared scaffolding every per-resource config-write
// handler (providers, groups, configurations, connectors, api_keys) builds on,
// alongside the commitClone tail in rules_write.go:
//
//   - referential-integrity pre-checks (referrersTo*) so a DELETE that would
//     orphan a reference is rejected with the referrers named, before the
//     clone is even built;
//   - mergeSecret, the write-back primitive that keeps a stored secret when the
//     caller signals "unchanged" rather than supplying a new value;
//   - previewMutation, the dry-run that validates a candidate mutation against a
//     clone without swapping the live snapshot (the diff-preview's safety half).
//
// commitClone, the validate -> persist -> Store.Replace tail, lives in
// rules_write.go and is already block-agnostic — it takes a whole
// *ResolvedConfig clone, so every resource reuses it unchanged.

// PreviewResult is the validation half of a diff-preview: whether a candidate
// mutation would pass RevalidateAndIndex, and the underlying error when not.
// The structural before/after diff is assembled by each per-resource handler
// (which knows the single resource that changed) and layered on top of this.
type PreviewResult struct {
	// Valid reports whether the mutated clone passed RevalidateAndIndex.
	Valid bool `json:"valid"`

	// Error is the validation failure detail; empty when Valid.
	Error string `json:"error,omitempty"`
}

// previewMutation applies mutate to a clone of snap and reports whether the
// result validates — without persisting or swapping the live snapshot. It is
// the dry-run a topology edit (providers/groups/configurations) runs so the
// operator sees "this would be rejected" before committing, and the safety
// floor for the console's diff-preview.
func previewMutation(snap *config.ResolvedConfig, mutate func(*config.ResolvedConfig)) PreviewResult {
	clone := snap.Clone()
	mutate(clone)
	if err := clone.RevalidateAndIndex(); err != nil {
		return PreviewResult{Valid: false, Error: err.Error()}
	}
	return PreviewResult{Valid: true}
}

// mergeSecret is the write-back primitive for a secret-bearing field. GET masks
// secrets (see redact), so a console round-trip that did not touch a secret
// sends back no new value — modelled as a nil incoming pointer. nil means "keep
// the stored value"; a non-nil pointer is an explicit set, including to the
// empty string (a no-credential provider legitimately stores ""). This is why
// the marker is a pointer and not the empty string: "" is a real value, not an
// "unchanged" signal.
func mergeSecret(incoming *string, existing string) string {
	if incoming == nil {
		return existing
	}
	return *incoming
}

// referrersToProvider returns the sorted, de-duplicated list of things that
// reference provider name — configuration bindings, passthrough bindings,
// credential entries, and group targets. A provider with referrers cannot be
// deleted (RevalidateAndIndex would reject the resulting config); the handler
// surfaces this list in the 409 so the operator knows what to unbind first.
func referrersToProvider(snap *config.ResolvedConfig, name string) []string {
	var out []string
	for cfgName, cfg := range snap.Configurations {
		if _, ok := cfg.Credentials[name]; ok {
			out = append(out, "configuration:"+cfgName+" credentials")
		}
		for _, b := range cfg.Bindings {
			if b.Provider == name {
				out = append(out, "configuration:"+cfgName+" binding")
				break
			}
		}
		for _, pb := range cfg.PassthroughBindings {
			if pb.Provider == name {
				out = append(out, "configuration:"+cfgName+" passthrough")
				break
			}
		}
	}
	for grpName, g := range snap.Groups {
		for _, t := range g.Targets {
			if t.Provider == name {
				out = append(out, "group:"+grpName)
				break
			}
		}
	}
	return sortedUnique(out)
}

// referrersToGroup returns the sorted configuration bindings that target group
// name.
func referrersToGroup(snap *config.ResolvedConfig, name string) []string {
	var out []string
	for cfgName, cfg := range snap.Configurations {
		for _, b := range cfg.Bindings {
			if b.Group == name {
				out = append(out, "configuration:"+cfgName+" binding")
				break
			}
		}
	}
	return sortedUnique(out)
}

// referrersToConfiguration returns the sorted api-key names bound to
// configuration name. An api_key referencing a missing configuration fails
// validation, so a referenced configuration cannot be deleted until its keys
// are reassigned or removed.
func referrersToConfiguration(snap *config.ResolvedConfig, name string) []string {
	var out []string
	for i := range snap.APIKeys {
		k := &snap.APIKeys[i]
		if k.Configuration == name {
			label := k.Name
			if label == "" {
				label = k.Secret
			}
			out = append(out, "api_key:"+label)
		}
	}
	return sortedUnique(out)
}

// referrersToConnector returns the sorted configurations whose connector
// bindings reference connector name.
func referrersToConnector(snap *config.ResolvedConfig, name string) []string {
	var out []string
	for cfgName, cfg := range snap.Configurations {
		for _, cb := range cfg.ConnectorBindings {
			if cb.Connector == name {
				out = append(out, "configuration:"+cfgName)
				break
			}
		}
	}
	return sortedUnique(out)
}

// sortedUnique returns in sorted with duplicates removed. Always returns a
// non-nil slice so a "no referrers" result JSON-encodes as [] not null.
func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	sort.Strings(in)
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}
