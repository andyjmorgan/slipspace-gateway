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
	Terminate bool

	Response *Response
}

// Response is the synthetic response payload produced by a terminating
// action. The pipeline turns this into a real HTTP response back to the
// client.
type Response struct {
	StatusCode int

	Body []byte

	BodyType StatusCodeBodyType
}

// ChangeProviderAction routes the request to a different provider. The
// forwarder uses the new provider's upstream credentials and endpoint mapping.
type ChangeProviderAction struct {
	Type string `yaml:"type" json:"type"`

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
	Type string `yaml:"type" json:"type"`

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
	Type string `yaml:"type" json:"type"`

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
	Type string `yaml:"type" json:"type"`

	APIKey string `yaml:"apiKey,omitempty" json:"api_key,omitempty"`

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
	Type string `yaml:"type" json:"type"`

	HeaderName string `yaml:"headerName" json:"header_name"`

	HeaderAction HeaderOp `yaml:"headerAction" json:"header_action"`

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
	Type string `yaml:"type" json:"type"`

	Key string `yaml:"key" json:"key"`

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
	Type string `yaml:"type" json:"type"`

	StatusCode int `yaml:"statusCode" json:"status_code"`

	Body string `yaml:"body,omitempty" json:"body,omitempty"`

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
	Type string `yaml:"type" json:"type"`

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

// UnknownAction is the catch-all fallback for any action discriminator the
// registry does not recognise. Type carries the unknown discriminator value
// and every other JSON field lands in DynamicProperties.Extra so the action
// round-trips intact.
type UnknownAction struct {
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
