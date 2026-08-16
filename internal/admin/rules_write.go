package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	rulescontract "github.com/andyjmorgan/slipspace-gateway/contracts/rules"
	"github.com/andyjmorgan/slipspace-gateway/internal/config"
)

// maxRuleBodyBytes caps the inbound JSON body on POST/PUT to keep a
// runaway client from forcing the gateway to buffer arbitrary memory
// while decoding. 256 KiB fits any realistic rule (a 1000-child group
// tree is still under 10 KiB) with plenty of headroom.
const maxRuleBodyBytes = 256 * 1024

// RuleConflictError is the wire shape for 409 responses on rule
// writes. Same envelope whether the conflict is name-already-exists
// (POST) or rule-still-referenced (DELETE); UsedBy is populated only
// in the referenced case.
type RuleConflictError struct {
	Error string `json:"error"`

	Name string `json:"name,omitempty"`

	UsedBy []string `json:"used_by,omitempty"`
}

// RuleValidationError is the wire shape for 422 responses — the clone
// failed Validate after the mutation was applied. Operators get the
// underlying error string so they can correct authoring mistakes
// without having to grep the gateway logs.
type RuleValidationError struct {
	Error string `json:"error"`

	Detail string `json:"detail"`
}

// RulesCreateHandler serves POST /api/v1/config/rules. Decodes the
// request body into a RuleContract (polymorphic Condition + Actions
// dispatched by the existing UnmarshalJSON), clones the current
// snapshot, appends the rule to clone.Rules, runs Validate + reindex
// on the clone, persists policy.yaml, then swaps the snapshot via
// Store.Replace.
//
// Response codes:
//
//   - 201 Created on success, body = RuleDetail
//   - 400 on JSON parse failure, missing name, or empty body
//   - 409 Conflict (RuleConflictError) when rule name already exists
//   - 422 Unprocessable Entity (RuleValidationError) when the post-
//     mutation Validate fails (e.g. unknown referenced policy on
//     UseResiliencePolicyAction — though for a freshly-added rule
//     that error is rare; included for consistency with PUT)
//   - 500 on disk write failure (permission denied, etc.)
//   - 503 when the store or config dir is not wired (admin write
//     surface disabled by deployment)
func RulesCreateHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		rule, ok := decodeRuleBody(w, r.Body)
		if !ok {
			return
		}
		if rule.Name == "" {
			http.Error(w, "rule name is required", http.StatusBadRequest)
			return
		}

		snap := store.Snapshot()
		if _, exists := snap.RuleIndex[rule.Name]; exists {
			writeConflict(w, http.StatusConflict, RuleConflictError{
				Error: "rule already exists",
				Name:  rule.Name,
			})
			return
		}

		clone := snap.Clone()
		clone.Rules = append(clone.Rules, rule)

		if err := commitClone(clone, configDir, store); err != nil {
			writeMutationError(w, err)
			return
		}

		w.WriteHeader(http.StatusCreated)
		writeJSON(w, ruleDetailFromClone(store.Snapshot(), rule.Name))
	})
}

// RulesReplaceHandler serves PUT /api/v1/config/rules/{name}. The URL
// name is authoritative — if the body carries a Name field, it must
// match the URL name (rename via PUT is rejected with 409 to avoid
// the half-renamed Configuration.RuleNames problem).
//
// Response codes:
//
//   - 200 OK on success, body = updated RuleDetail
//   - 400 on JSON parse failure or empty path
//   - 404 when no rule with the URL name exists
//   - 409 Conflict on rename attempt
//   - 422 on validation failure
//   - 500 on disk write failure
//   - 503 when admin write disabled
func RulesReplaceHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		urlName := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, pathRules), "/")
		if urlName == "" {
			http.NotFound(w, r)
			return
		}
		rule, ok := decodeRuleBody(w, r.Body)
		if !ok {
			return
		}
		if rule.Name == "" {
			rule.Name = urlName
		}
		if rule.Name != urlName {
			writeConflict(w, http.StatusConflict, RuleConflictError{
				Error: "rename not supported; body.name must match URL name",
				Name:  rule.Name,
			})
			return
		}

		snap := store.Snapshot()
		idx, found := indexOfRule(snap.Rules, urlName)
		if !found {
			http.NotFound(w, r)
			return
		}

		clone := snap.Clone()
		clone.Rules[idx] = rule

		if err := commitClone(clone, configDir, store); err != nil {
			writeMutationError(w, err)
			return
		}

		writeJSON(w, ruleDetailFromClone(store.Snapshot(), rule.Name))
	})
}

// RulesDeleteHandler serves DELETE /api/v1/config/rules/{name}.
// Refuses with 409 when the rule is still referenced by one or more
// Configurations' RuleNames lists — operators must unbind it first.
// This keeps the post-delete state guaranteed to pass Validate
// (validateLibraries enforces every RuleNames entry resolves).
//
// Response codes:
//
//   - 204 No Content on success
//   - 404 when no rule with the URL name exists
//   - 409 Conflict (RuleConflictError with UsedBy populated) when
//     the rule is still bound to one or more configurations
//   - 422 on a post-mutation validation failure (defensive — should
//     not happen since deleting an unreferenced rule cannot create
//     new invariant violations)
//   - 500 on disk write failure
//   - 503 when admin write disabled
func RulesDeleteHandler(store *config.Store, configDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writableGuard(w, store, configDir) {
			return
		}
		urlName := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, pathRules), "/")
		if urlName == "" {
			http.NotFound(w, r)
			return
		}

		snap := store.Snapshot()
		idx, found := indexOfRule(snap.Rules, urlName)
		if !found {
			http.NotFound(w, r)
			return
		}

		usedBy := configurationsReferencingRule(snap, urlName)
		if len(usedBy) > 0 {
			writeConflict(w, http.StatusConflict, RuleConflictError{
				Error:  "rule is referenced by one or more configurations",
				Name:   urlName,
				UsedBy: usedBy,
			})
			return
		}

		clone := snap.Clone()
		clone.Rules = append(clone.Rules[:idx], clone.Rules[idx+1:]...)

		if err := commitClone(clone, configDir, store); err != nil {
			writeMutationError(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

// writableGuard 503s when the store or config dir is not wired. The
// admin write endpoints are useless without both — the store has no
// snapshot to clone, or there is nowhere to persist the result.
func writableGuard(w http.ResponseWriter, store *config.Store, configDir string) bool {
	if store == nil || store.Snapshot() == nil {
		http.Error(w, "config unavailable", http.StatusServiceUnavailable)
		return false
	}
	if configDir == "" {
		http.Error(w, "admin write surface disabled (no config dir wired)", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// decodeRuleBody pulls a RuleContract out of body. Decode failures
// (malformed JSON, unknown polymorphic type discriminator, invalid
// UUID on the id field) come back from the existing UnmarshalJSON
// chain with descriptive messages, so we surface them verbatim as
// 400 responses.
func decodeRuleBody(w http.ResponseWriter, body io.Reader) (rulescontract.RuleContract, bool) {
	limited := http.MaxBytesReader(w, io.NopCloser(body), maxRuleBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		http.Error(w, "request body too large or unreadable: "+err.Error(), http.StatusBadRequest)
		return rulescontract.RuleContract{}, false
	}
	if len(raw) == 0 {
		http.Error(w, "request body is empty", http.StatusBadRequest)
		return rulescontract.RuleContract{}, false
	}
	var rule rulescontract.RuleContract
	if err := json.Unmarshal(raw, &rule); err != nil {
		http.Error(w, "invalid rule JSON: "+err.Error(), http.StatusBadRequest)
		return rulescontract.RuleContract{}, false
	}
	return rule, true
}

// commitClone is the shared tail every write handler runs: validate
// the clone, reindex it, persist it via config.WriteConfig (which
// routes each editable block back to its SourceFiles origin, not to a
// fixed policy.yaml), then atomically swap. On any error the live
// snapshot is untouched. This ordering — validate → persist → Replace
// — is CLAUDE.md invariant 9.
func commitClone(clone *config.ResolvedConfig, configDir string, store *config.Store) error {
	if err := clone.RevalidateAndIndex(); err != nil {
		return validationError{wrapped: err}
	}
	if err := config.WriteConfig(configDir, clone); err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	store.Replace(clone)
	return nil
}

// validationError flags a commit failure that originated in
// Validate. writeMutationError uses this to surface 422 with the
// underlying detail string; non-validation failures get 500.
type validationError struct {
	wrapped error
}

func (e validationError) Error() string { return e.wrapped.Error() }
func (e validationError) Unwrap() error { return e.wrapped }

func writeMutationError(w http.ResponseWriter, err error) {
	var ve validationError
	if errors.As(err, &ve) {
		writeJSONStatus(w, http.StatusUnprocessableEntity, RuleValidationError{
			Error:  "validation failed",
			Detail: ve.Error(),
		})
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func writeConflict(w http.ResponseWriter, status int, body RuleConflictError) {
	writeJSONStatus(w, status, body)
}

func writeJSONStatus(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// At this point headers are sent; logging would help but the
		// admin package does not own a logger. Best effort.
		_ = err
	}
}

// indexOfRule returns the position of the rule with name in rules,
// or (-1, false) if not present.
func indexOfRule(rules []rulescontract.RuleContract, name string) (int, bool) {
	for i := range rules {
		if rules[i].Name == name {
			return i, true
		}
	}
	return -1, false
}

// configurationsReferencingRule returns the sorted list of
// configuration names whose RuleNames slice contains ruleName.
func configurationsReferencingRule(snap *config.ResolvedConfig, ruleName string) []string {
	out := make([]string, 0)
	for name, cfg := range snap.Configurations {
		for _, rn := range cfg.RuleNames {
			if rn == ruleName {
				out = append(out, name)
				break
			}
		}
	}
	return sortedCopy(out)
}

// ruleDetailFromClone projects the post-write snapshot's rule into
// the same RuleDetail shape the GET handler returns, so create and
// replace responses are interchangeable with a follow-up GET.
func ruleDetailFromClone(snap *config.ResolvedConfig, name string) RuleDetail {
	rule, ok := snap.RuleIndex[name]
	if !ok {
		// Should not happen — Replace just published a snapshot that
		// contained this rule. Defensive fallback returns the bare
		// shape so the handler still produces JSON.
		return RuleDetail{Name: name, UsedBy: []string{}}
	}
	return RuleDetail{
		Name:      rule.Name,
		Behavior:  rule.Behavior,
		Condition: rule.Condition,
		Actions:   rule.Actions,
		UsedBy:    configurationsReferencingRule(snap, name),
	}
}
