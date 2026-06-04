package config

import "github.com/andyjmorgan/sluice-gateway/contracts/resilience"

// This file defines the v2 configuration model (backends + bindings), added
// alongside the v1 model (Provider/Endpoint + Configuration.UpstreamCredentials
// + rule-driven routing). v2 collapses routing into config data: the inbound
// path is the protocol, and a configuration's bindings map (protocol, model) to
// a backend or resilience group. Routing rules (changeProvider/changeUrl/
// changeApiKey/useResiliencePolicy) become binding/backend/group data; rules
// shrink to pure request/response transforms.
//
// These are control-plane config types, not provider wire models — they do not
// embed DynamicProperties. See the "Provider/Config Model v2" design note.

// Recognised generative protocol names. The inbound base path maps to exactly
// one of these; the protocol fixes the typed wire shape the body is parsed as.
const (
	// ProtocolChat is the OpenAI chat-completions wire shape
	// (/v1/chat/completions). Anthropic and Gemini expose OpenAI-compat
	// surfaces under this protocol too.
	ProtocolChat = "chat"

	// ProtocolResponses is the OpenAI Responses wire shape (/v1/responses).
	ProtocolResponses = "responses"

	// ProtocolMessages is the Anthropic native Messages wire shape
	// (/v1/messages).
	ProtocolMessages = "messages"

	// ProtocolGenerateContent is the Gemini native generateContent wire shape.
	ProtocolGenerateContent = "generate_content"

	// ProtocolEmbeddings is the embeddings wire shape; model-keyed like the
	// generative protocols (the model rides in the request body).
	ProtocolEmbeddings = "embeddings"
)

// BackendsConfig is the merged top-level `backends` block — a map from backend
// name to its connection definition. A backend is a connection (base URL, auth
// scheme, transport quirks), not a credential holder: the per-config
// credentials supply the key.
type BackendsConfig map[string]Backend

// Backend is one upstream connection: where to reach it, which protocols it
// speaks (each with its own path + auth), the transport quirks every request to
// it carries, and any opaque passthrough families it exposes. The actual
// credential is not stored here — a Configuration supplies it per backend and
// it is substituted into the protocol auth format's `{key}` placeholder.
type Backend struct {
	// BaseURL is the upstream root (e.g. "https://api.openai.com"); a
	// protocol's Path is appended to it when forwarding.
	BaseURL string `yaml:"base_url" json:"base_url"`

	// RequiredHeaders are headers injected on every forwarded request to this
	// backend (e.g. "anthropic-version: 2023-06-01").
	RequiredHeaders map[string]string `yaml:"required_headers,omitempty" json:"required_headers,omitempty"`

	// Query is the default query-string parameters appended to every request
	// to this backend (e.g. Azure's "api-version"). A binding/target may add
	// or override entries; the effective set is backend ∪ override.
	Query map[string]string `yaml:"query,omitempty" json:"query,omitempty"`

	// Protocols is the generative wire shapes this backend serves, keyed by
	// protocol name (see the ProtocolX constants). Each carries the upstream
	// path and an optional per-protocol auth override — load-bearing for
	// OpenAI-compat surfaces, where the same backend speaks its native
	// protocol with one credential convention and a compat `chat` surface
	// with another (invariant #6).
	Protocols map[string]BackendProtocol `yaml:"protocols,omitempty" json:"protocols,omitempty"`

	// Passthrough is the opaque/stateful endpoint families this backend
	// exposes (e.g. Anthropic message batches), keyed by family name. These
	// are proxied verbatim with rewrites only — never typed, never
	// GenAI-telemetried, never payload-captured.
	Passthrough map[string]PassthroughFamily `yaml:"passthrough,omitempty" json:"passthrough,omitempty"`
}

// BackendProtocol is one generative protocol a backend serves.
type BackendProtocol struct {
	// Path is the upstream path template appended to Backend.BaseURL. May
	// contain placeholders the gateway substitutes (e.g. Gemini's
	// "/v1beta/models/{model}:{op}").
	Path string `yaml:"path,omitempty" json:"path,omitempty"`

	// Auth overrides the credential header/format for this protocol. nil
	// defers to the provider-native default for the wire shape. The override
	// is what lets a backend's OpenAI-compat `chat` surface authenticate with
	// "Authorization: Bearer" while its native protocol uses a different
	// header.
	Auth *BackendAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// BackendAuth is the outgoing credential convention for a protocol or
// passthrough family: which header carries the key and how the value is
// formatted.
type BackendAuth struct {
	// Header is the outgoing HTTP header name the credential is injected into
	// (e.g. "Authorization", "x-api-key", "x-goog-api-key", "api-key").
	Header string `yaml:"header" json:"header"`

	// Format templates the header value. A single `{key}` placeholder is
	// substituted with the resolved upstream credential (e.g. "Bearer {key}"
	// or "{key}").
	Format string `yaml:"format,omitempty" json:"format,omitempty"`
}

// PassthroughFamily is an opaque, stateful endpoint family proxied verbatim
// (e.g. Anthropic's message-batches surface across create/get/results/cancel).
// Not model-keyed; selected by path pattern. No typed parsing, no GenAI
// telemetry, no payload capture — only rewrites and plain HTTP metrics.
type PassthroughFamily struct {
	// Auth is the credential convention for this family. nil defers to the
	// provider-native default.
	Auth *BackendAuth `yaml:"auth,omitempty" json:"auth,omitempty"`

	// Paths is the inbound path patterns this family claims, with the methods
	// each accepts. Patterns may contain `{name}` placeholders (e.g.
	// "/v1/messages/batches/{id}/results").
	Paths []PassthroughPath `yaml:"paths" json:"paths"`
}

// PassthroughPath is one inbound path pattern of a passthrough family.
type PassthroughPath struct {
	// Match is the inbound path pattern, optionally containing `{name}`
	// placeholders.
	Match string `yaml:"match" json:"match"`

	// Methods is the HTTP methods this path accepts.
	Methods []string `yaml:"methods" json:"methods"`
}

// GroupsConfig is the merged top-level `groups` block — a map from group name
// to its resilience definition. Groups replace v1 `resilience_policies`; a
// binding references a group instead of a rule firing useResiliencePolicy.
type GroupsConfig map[string]Group

// Group is a named resilience unit: an ordered or weighted set of Targets the
// orchestrator routes across with a failure policy and circuit breaker.
// Targets are arbitrary backends, so a group cuts across providers; the only
// constraint is that every target serves the binding's protocol
// (protocol-preserving — no mid-failover translation).
type Group struct {
	// Mode selects the orchestration strategy (failover, load_balance, …).
	Mode resilience.ResilienceMode `yaml:"mode" json:"mode"`

	// FailureStatusCodes is the upstream HTTP status set treated as a failure
	// for retry / circuit-breaker accounting. Empty falls back to "5xx is a
	// failure".
	FailureStatusCodes []int `yaml:"failure_status_codes,omitempty" json:"failure_status_codes,omitempty"`

	// CircuitBreaker is the group-wide breaker. Breaker state is tracked
	// per-backend (the failure unit), so a dead backend is skipped by every
	// group that includes it.
	CircuitBreaker *resilience.CircuitBreakerConfig `yaml:"circuit_breaker,omitempty" json:"circuit_breaker,omitempty"`

	// StrictWeights, in load_balance mode, makes the first weighted-random
	// pick final — no re-roll onto another target on a retryable failure.
	// Used for canary mirroring where the under-weighted target's failures
	// must surface to the client. Ignored in failover mode.
	StrictWeights bool `yaml:"strict_weights,omitempty" json:"strict_weights,omitempty"`

	// ResponseHeaderTimeoutSeconds, when > 0, overrides the gateway-wide
	// upstream response-header timeout for every attempt under this group —
	// so a group can fail over off a slow target faster than the default.
	ResponseHeaderTimeoutSeconds int `yaml:"response_header_timeout_seconds,omitempty" json:"response_header_timeout_seconds,omitempty"`

	// Targets is the backends this group routes across, each with its own
	// optional per-target overrides (model alias, query, path, weight).
	Targets []Target `yaml:"targets" json:"targets"`
}

// Target is the atom a binding (or group) dispatches to: a backend reference
// plus per-use overrides. The overrides compose over the backend's own values
// (target wins). Alias is the model-name rewrite — placing it on the target
// (not the binding) lets a group send one logical model under a different
// upstream id per backend.
type Target struct {
	// Backend names a Backend in the top-level backends block.
	Backend string `yaml:"backend" json:"backend"`

	// Alias, when non-empty, rewrites the request body's model field to this
	// value when this target is selected (equivalent to the v1 resilience
	// ResilienceTarget.ModelRewrite).
	Alias string `yaml:"alias,omitempty" json:"alias,omitempty"`

	// Query adds or overrides query-string parameters for this target,
	// composed over Backend.Query.
	Query map[string]string `yaml:"query,omitempty" json:"query,omitempty"`

	// Path overrides the protocol path for this target (e.g. an Azure
	// deployment-specific path on a shared backend connection).
	Path string `yaml:"path,omitempty" json:"path,omitempty"`

	// Weight is the relative selection weight in load_balance mode. Zero is
	// treated as 1 (even weighting) by the orchestrator synthesiser; ignored
	// in failover mode, where Order (declaration order) drives sequencing.
	Weight int `yaml:"weight,omitempty" json:"weight,omitempty"`
}

// Binding maps a generative (protocol, model) pair to a destination — a single
// backend or a resilience group. It is the router expressed as config data.
// Selection is `(protocol-from-path, model-from-body) → first matching binding`.
// Exactly one of Backend or Group is set; Alias/Query/Path are single-backend
// sugar for the binding's implicit single Target (a group carries per-target
// overrides instead).
type Binding struct {
	// Protocol is the generative protocol this binding serves (a ProtocolX
	// constant).
	Protocol string `yaml:"protocol" json:"protocol"`

	// Models is the client-requested model patterns this binding matches.
	// Trailing-`*` wildcard, reusing the connector-filter matcher. Surface is
	// default-permissive and opt-in (invariant #1): an empty model set is a
	// catch-all for the protocol, never a default-deny.
	Models []string `yaml:"models,omitempty" json:"models,omitempty"`

	// Backend names the single destination backend. Mutually exclusive with
	// Group.
	Backend string `yaml:"backend,omitempty" json:"backend,omitempty"`

	// Group names a resilience group destination. Mutually exclusive with
	// Backend.
	Group string `yaml:"group,omitempty" json:"group,omitempty"`

	// Alias rewrites the request body model name for the single-backend case
	// (sugar for the binding's implicit Target.Alias). Ignored when Group is
	// set — group targets carry their own aliases.
	Alias string `yaml:"alias,omitempty" json:"alias,omitempty"`

	// Query / Path are single-backend per-use overrides (sugar for the
	// implicit Target). Ignored when Group is set.
	Query map[string]string `yaml:"query,omitempty" json:"query,omitempty"`

	// Path overrides the protocol path for the single-backend case.
	Path string `yaml:"path,omitempty" json:"path,omitempty"`

	// Tags are labels applied to a request matching this binding, propagated
	// to telemetry (carries forward the v1 addTag-on-route pattern).
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// PassthroughBinding exposes a backend's named passthrough family on a
// configuration. Opaque proxy — selected by the family's path patterns, not by
// model.
type PassthroughBinding struct {
	// Family names a passthrough family declared on the bound Backend.
	Family string `yaml:"family" json:"family"`

	// Backend names the Backend whose passthrough family is exposed.
	Backend string `yaml:"backend" json:"backend"`

	// Tags are labels applied to requests matching this family.
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// Configuration is the v2 policy bundle. It selects backends (with the
// credentials this configuration holds) and declares the bindings that route
// (protocol, model) to them, plus the surviving transform rules, tags, and
// connector bindings. The generative request path is identical to v1 — only
// the source of routing/credential data changes.
type Configuration struct {
	// Credentials maps backend name to the upstream credential this
	// configuration holds for it ({key} resolves from here). An empty string
	// means a no-credential backend (strip and forward).
	Credentials map[string]string `yaml:"credentials,omitempty" json:"credentials,omitempty"`

	// Bindings is the generative routing table: (protocol, model) → backend
	// or group. Evaluated in order; first match wins.
	Bindings []Binding `yaml:"bindings,omitempty" json:"bindings,omitempty"`

	// PassthroughBindings exposes opaque endpoint families this configuration
	// proxies.
	PassthroughBindings []PassthroughBinding `yaml:"passthrough_bindings,omitempty" json:"passthrough_bindings,omitempty"`

	// RuleNames lists the transform rules (from the top-level rules library)
	// this configuration applies. Routing actions have been retired into
	// bindings; what remains is body/header/query rewrites, tags, and
	// short-circuits.
	RuleNames []string `yaml:"rule_names,omitempty" json:"rule_names,omitempty"`

	// Tags are labels propagated to telemetry for every request under this
	// configuration.
	Tags map[string]string `yaml:"tags,omitempty" json:"tags,omitempty"`

	// ConnectorBindings attaches connectors with per-binding sampling /
	// filter / size-cap overrides (unchanged from v1).
	ConnectorBindings []ConnectorBinding `yaml:"connector_bindings,omitempty" json:"connector_bindings,omitempty"`
}

// Model is the top-level v2 configuration document: the shared backend and
// group catalogues plus the per-name configurations. Connectors and API keys
// carry over from v1 unchanged.
type Model struct {
	// Backends is the shared connection catalogue.
	Backends BackendsConfig `yaml:"backends,omitempty" json:"backends,omitempty"`

	// Groups is the shared resilience-group catalogue.
	Groups GroupsConfig `yaml:"groups,omitempty" json:"groups,omitempty"`

	// Configurations is the per-name policy bundles.
	Configurations map[string]Configuration `yaml:"configurations,omitempty" json:"configurations,omitempty"`

	// Connectors carries over from v1 unchanged.
	Connectors ConnectorsConfig `yaml:"connectors,omitempty" json:"connectors,omitempty"`

	// APIKeys carries over from v1 unchanged.
	APIKeys APIKeysConfig `yaml:"api_keys,omitempty" json:"api_keys,omitempty"`
}
