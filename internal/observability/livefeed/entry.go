package livefeed

import "time"

// Entry is one completed-request record in the ring. Built at OnComplete
// by the per-request reporter observer and appended via Ring.Append.
//
// All fields are value-typed so the Entry can be shallow-copied to
// subscribers without aliasing the writer's storage. RulesMatched is
// always a freshly-allocated slice owned by the Entry.
type Entry struct {
	// EventID is a UUID minted at Append time. Stable identifier the
	// SPA uses as a row key and the eventual /messages/{id} body
	// endpoint will key off when it lands.
	EventID string

	// At is the wall-clock time the request completed.
	At time.Time

	// CorrelationID is the gateway-assigned request UUID, also
	// surfaced on the response as X-Sluice-Correlation-Id.
	CorrelationID string

	// Provider, Endpoint, Model are the routed labels after rule
	// mutation — same values that hit the connector pipeline's
	// Record.
	Provider string
	Endpoint string
	Model    string

	// Method is the inbound HTTP verb (GET, POST, …) of the client
	// request. Lets the console distinguish a model list from a
	// completion at a glance.
	Method string

	// Configuration is the resolved configuration name the request
	// ran under. Cardinality is bounded by operator policy.
	Configuration string

	// StatusCode is the HTTP status the client received.
	StatusCode int

	// DurationMs is the end-to-end duration in milliseconds.
	DurationMs int64

	// Streaming is true iff the upstream response was an SSE stream.
	Streaming bool

	// UpstreamError carries the transport-level error string when the
	// upstream could not be reached or tore the connection before
	// headers arrived. Empty on the success path.
	UpstreamError string

	// TokensIn / TokensOut / TokensCached / TokensCacheCreation mirror
	// the extracted upstream usage block on contracts/events/Request.
	// Zero when the response carried no usage data (e.g. streaming
	// without include_usage, client cancelled before terminal chunk,
	// or the upstream simply omitted usage).
	TokensIn            int
	TokensOut           int
	TokensCached        int
	TokensCacheCreation int

	// Tags is the set of rule-attached tags (AddTagAction) in
	// first-attach order. Empty when no tagging rule fired for the
	// request.
	Tags []string

	// RulesMatched lists every rule that fired during evaluation, in
	// the order they ran. Empty when the rule engine matched nothing
	// or when the configuration has no rules attached.
	RulesMatched []RuleHit

	// PolicyRef is the resilience policy the request was orchestrated
	// against, mirroring contracts/events.Request.PolicyRef. Empty for
	// single-shot requests.
	PolicyRef string

	// Attempts is the per-attempt orchestrator record, in attempt
	// order. Always populated for requests bound to a policy; the
	// terminal entry is the attempt that committed (or the last one
	// before exhaustion). Empty for single-shot.
	Attempts []AttemptHit
}

// AttemptHit is the live-feed projection of one resilience attempt —
// mirrors a subset of events.AttemptRecord so the SPA modal can render
// the per-attempt breakdown without round-tripping the full connector
// Record.
type AttemptHit struct {
	// Target is the resilience target name attempted.
	Target string

	// StartedAt is the orchestrator's wall-clock start time for this
	// attempt.
	StartedAt time.Time

	// DurationMs is the orchestrator-measured attempt duration. Zero
	// for cb_blocked attempts where nothing ran.
	DurationMs int64

	// StatusCode is the upstream-reported status, when received. Zero
	// for transport errors and cb_blocked.
	StatusCode int

	// Error is the transport-level error string, empty on success and
	// cb_blocked.
	Error string

	// Outcome categorises the attempt: "success", "failure_status",
	// "transport_error", or "cb_blocked".
	Outcome string
}

// RuleHit is the compact per-rule record the live-feed surfaces to the
// SPA. Mirrors a subset of events.RuleMatched — enough for the UI to
// render the chain of matches per request without the full envelope.
type RuleHit struct {
	// RuleName is the operator-authored rule name. Always populated.
	RuleName string

	// ActionsApplied lists the action type discriminators that fired
	// for this match, in execution order.
	ActionsApplied []string

	// Terminated is true iff a terminating action short-circuited the
	// pipeline.
	Terminated bool

	// ErrorMessage carries the human-readable error string when an
	// action failed during apply. Empty on the success path.
	ErrorMessage string
}
