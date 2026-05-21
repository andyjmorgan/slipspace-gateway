package rules

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/andyjmorgan/sluice-gateway/models"
)

// Action is the polymorphic interface implemented by every concrete action
// type plus the UnknownAction fallback. ActionType returns the "type"
// discriminator carried on the wire.
type Action interface {
	ActionType() string

	isAction()
}

// Outcome reports the effect of applying an Action. Terminate signals the
// pipeline to short-circuit; Response is populated when the action returns a
// synthetic response to the client.
type Outcome struct {
	// Terminate, when true, halts the pipeline after this action.
	Terminate bool

	// Response, when non-nil, is the synthetic response the pipeline should
	// return to the client instead of forwarding upstream.
	Response *Response
}

// Response is the synthetic response payload produced by a terminating
// action. The pipeline turns this into a real HTTP response back to the
// client.
type Response struct {
	// StatusCode is the HTTP status returned to the client.
	StatusCode int

	// Body is the raw response body bytes.
	Body []byte

	// BodyType selects the Content-Type family; the pipeline maps this to
	// the actual Content-Type header.
	BodyType StatusCodeBodyType
}

// ChangeProviderAction routes the request to a different provider. The
// forwarder uses the new provider's upstream credentials and endpoint mapping.
type ChangeProviderAction struct {
	// Type is the polymorphic discriminator; always "changeProvider".
	Type string `yaml:"type" json:"type"`

	// NewProvider is the provider name to route to. Must exist in providers.yaml.
	NewProvider string `yaml:"newProvider" json:"new_provider"`

	models.DynamicProperties
}

// ActionType returns the "changeProvider" discriminator.
func (ChangeProviderAction) ActionType() string { return "changeProvider" }

func (ChangeProviderAction) isAction() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *ChangeProviderAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a ChangeProviderAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// ChangeModelNameAction rewrites the model name in the request body.
type ChangeModelNameAction struct {
	// Type is the polymorphic discriminator; always "changeModelName".
	Type string `yaml:"type" json:"type"`

	// NewModelName is the replacement model identifier written into the
	// request body's model field.
	NewModelName string `yaml:"newModelName" json:"new_model_name"`

	models.DynamicProperties
}

// ActionType returns the "changeModelName" discriminator.
func (ChangeModelNameAction) ActionType() string { return "changeModelName" }

func (ChangeModelNameAction) isAction() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *ChangeModelNameAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a ChangeModelNameAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// ChangeUrlAction overrides the upstream base URL for this request.
type ChangeUrlAction struct {
	// Type is the polymorphic discriminator; always "changeUrl".
	Type string `yaml:"type" json:"type"`

	// NewURL replaces the resolved provider's BaseURL for this single
	// request only.
	NewURL string `yaml:"newUrl" json:"new_url"`

	models.DynamicProperties
}

// ActionType returns the "changeUrl" discriminator.
func (ChangeUrlAction) ActionType() string { return "changeUrl" }

func (ChangeUrlAction) isAction() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *ChangeUrlAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a ChangeUrlAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// ChangeApiKeyAction overrides the upstream API key for this request. When
// UseSluiceKey is true the inbound Sluice key is forwarded instead, for
// passthrough scenarios.
type ChangeApiKeyAction struct {
	// Type is the polymorphic discriminator; always "changeApiKey".
	Type string `yaml:"type" json:"type"`

	// APIKey is the literal upstream key substituted on the way out. Ignored
	// when UseSluiceKey is true.
	APIKey string `yaml:"apiKey,omitempty" json:"api_key,omitempty"`

	// UseSluiceKey, when true, forwards the inbound Sluice-issued bearer
	// verbatim instead of substituting an upstream credential — used for
	// passthrough auth scenarios.
	UseSluiceKey bool `yaml:"useSluiceKey,omitempty" json:"use_sluice_key,omitempty"`

	models.DynamicProperties
}

// ActionType returns the "changeApiKey" discriminator.
func (ChangeApiKeyAction) ActionType() string { return "changeApiKey" }

func (ChangeApiKeyAction) isAction() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *ChangeApiKeyAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a ChangeApiKeyAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// SetHeaderAction modifies an HTTP header on the outgoing request.
type SetHeaderAction struct {
	// Type is the polymorphic discriminator; always "setHeader".
	Type string `yaml:"type" json:"type"`

	// HeaderName is the header to modify on the outgoing request.
	HeaderName string `yaml:"headerName" json:"header_name"`

	// HeaderAction selects how HeaderValue is applied — Set, Append,
	// Prepend, or Remove.
	HeaderAction HeaderOp `yaml:"headerAction" json:"header_action"`

	// HeaderValue is the value applied per HeaderAction. Ignored for
	// HeaderRemove.
	HeaderValue string `yaml:"headerValue,omitempty" json:"header_value,omitempty"`

	models.DynamicProperties
}

// ActionType returns the "setHeader" discriminator.
func (SetHeaderAction) ActionType() string { return "setHeader" }

func (SetHeaderAction) isAction() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *SetHeaderAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a SetHeaderAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// AppendQueryStringAction appends a query-string parameter to the outgoing
// URL.
type AppendQueryStringAction struct {
	// Type is the polymorphic discriminator; always "appendQueryString".
	Type string `yaml:"type" json:"type"`

	// Key is the query-string parameter name.
	Key string `yaml:"key" json:"key"`

	// Value is the query-string parameter value.
	Value string `yaml:"value" json:"value"`

	models.DynamicProperties
}

// ActionType returns the "appendQueryString" discriminator.
func (AppendQueryStringAction) ActionType() string { return "appendQueryString" }

func (AppendQueryStringAction) isAction() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *AppendQueryStringAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a AppendQueryStringAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// ReturnStatusCodeAction is a TERMINATING action that short-circuits the
// pipeline and returns a synthetic response to the client.
type ReturnStatusCodeAction struct {
	// Type is the polymorphic discriminator; always "returnStatusCode".
	Type string `yaml:"type" json:"type"`

	// StatusCode is the HTTP status returned to the client.
	StatusCode int `yaml:"statusCode" json:"status_code"`

	// Body is the response body sent to the client.
	Body string `yaml:"body,omitempty" json:"body,omitempty"`

	// BodyType selects the Content-Type family applied to Body.
	BodyType StatusCodeBodyType `yaml:"bodyType,omitempty" json:"body_type,omitempty"`

	models.DynamicProperties
}

// ActionType returns the "returnStatusCode" discriminator.
func (ReturnStatusCodeAction) ActionType() string { return "returnStatusCode" }

func (ReturnStatusCodeAction) isAction() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *ReturnStatusCodeAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a ReturnStatusCodeAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// LlmImpersonationAction is a TERMINATING action that returns a fake LLM
// completion to the client without contacting upstream. Useful for
// blocked-content responses that still look like a model completion.
type LlmImpersonationAction struct {
	// Type is the polymorphic discriminator; always "llmImpersonation".
	Type string `yaml:"type" json:"type"`

	// Message is the synthetic completion text wrapped in the upstream
	// provider's response shape.
	Message string `yaml:"message" json:"message"`

	models.DynamicProperties
}

// ActionType returns the "llmImpersonation" discriminator.
func (LlmImpersonationAction) ActionType() string { return "llmImpersonation" }

func (LlmImpersonationAction) isAction() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *LlmImpersonationAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a LlmImpersonationAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// AddTagAction attaches a tag to the request as it flows through the
// engine. Tags are set-membership values (sorted, deduplicated by the
// evaluator) — multiple addTag actions across the rule chain accumulate
// onto the same request. The reporter surfaces them on
// gateway.request, the live-feed Entry, and a side-channel
// gateway.tags.applied.total counter.
//
// Downstream rules can match on accumulated tags via TagCondition,
// which makes addTag both a side-effect and an input to subsequent
// rules. Rule order in Configuration.RuleNames is the producer/
// consumer ordering — put the tag-producing rule before the
// tag-consuming rule.
type AddTagAction struct {
	// Type is the polymorphic discriminator; always "addTag".
	Type string `yaml:"type" json:"type"`

	// Tag is the tag string to attach. Trimmed and required —
	// empty values are rejected at evaluate time so misconfigured
	// rules do not silently no-op. Convention: free-form strings;
	// operators often use a `prefix:value` form (e.g. "tier:gold",
	// "audit:pii") to namespace.
	Tag string `yaml:"tag" json:"tag"`

	models.DynamicProperties
}

// ActionType returns the "addTag" discriminator.
func (AddTagAction) ActionType() string { return "addTag" }

func (AddTagAction) isAction() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *AddTagAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a AddTagAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

// UnknownAction is the catch-all fallback for any action discriminator the
// registry does not recognise. Type carries the unknown discriminator value
// and every other JSON field lands in DynamicProperties.Extra so the action
// round-trips intact.
type UnknownAction struct {
	// Type holds the unknown discriminator value verbatim so it can be
	// re-emitted on marshal.
	Type string `yaml:"type" json:"type"`

	models.DynamicProperties
}

// ActionType returns the unknown discriminator value verbatim.
func (a UnknownAction) ActionType() string { return a.Type }

func (UnknownAction) isAction() {}

// UnmarshalJSON routes unknown fields through DynamicProperties.
func (a *UnknownAction) UnmarshalJSON(data []byte) error {
	return models.UnmarshalDynamic(data, a)
}

// MarshalJSON merges DynamicProperties.Extra back into the wire payload.
func (a UnknownAction) MarshalJSON() ([]byte, error) { return models.MarshalDynamic(a) }

var actionRegistry = models.PolymorphicRegistry[Action]{
	DiscriminatorField: "type",
	Factories: map[string]func() Action{
		"changeProvider":    func() Action { return &ChangeProviderAction{} },
		"changeModelName":   func() Action { return &ChangeModelNameAction{} },
		"changeUrl":         func() Action { return &ChangeUrlAction{} },
		"changeApiKey":      func() Action { return &ChangeApiKeyAction{} },
		"setHeader":         func() Action { return &SetHeaderAction{} },
		"appendQueryString": func() Action { return &AppendQueryStringAction{} },
		"returnStatusCode":  func() Action { return &ReturnStatusCodeAction{} },
		"llmImpersonation":  func() Action { return &LlmImpersonationAction{} },
		"addTag":            func() Action { return &AddTagAction{} },
	},
	Fallback: func(disc string) Action { return &UnknownAction{Type: disc} },
}

// UnmarshalAction decodes a single Action, dispatching on the "type"
// discriminator and falling back to UnknownAction for any unrecognised value
// so the payload round-trips intact.
func UnmarshalAction(data []byte) (Action, error) {
	return actionRegistry.UnmarshalOne(data)
}

// decodeActionNode dispatches a YAML node to the concrete action type by
// inspecting its "type" key. Unknown discriminators fall back to
// UnknownAction for parity with the JSON path.
func decodeActionNode(node *yaml.Node) (Action, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("rules: action: expected mapping, got %v", node.Kind)
	}
	disc, err := peekYAMLDiscriminator(node, "type")
	if err != nil {
		return nil, err
	}
	factory, ok := actionRegistry.Factories[disc]
	if !ok {
		ua := &UnknownAction{Type: disc}
		if err := decodeUnknownYAMLNode(node, &ua.DynamicProperties); err != nil {
			return nil, err
		}
		return ua, nil
	}
	act := factory()
	if err := node.Decode(act); err != nil {
		return nil, fmt.Errorf("rules: action %q: %w", disc, err)
	}
	return act, nil
}
