package translate

import (
	cfg "github.com/andyjmorgan/slipspace-gateway/contracts/config"
)

// openAIAnthropicChat is the (chat -> messages) translator: it accepts an
// OpenAI Chat Completions request/response and translates to/from Anthropic
// Messages. It is the reverse of anthropicOpenAIChat — registering both makes
// the messages<->chat pair bidirectional (an OpenAI Chat client can target an
// Anthropic Messages upstream, and vice versa). It is the concrete pairing of
// the request and response mappers in this package.
type openAIAnthropicChat struct{}

// Source reports the OpenAI Chat Completions protocol.
func (openAIAnthropicChat) Source() Protocol { return cfg.ProtocolChat }

// Target reports the Anthropic Messages protocol.
func (openAIAnthropicChat) Target() Protocol { return cfg.ProtocolMessages }

// TranslateRequest maps an OpenAI Chat request body to Anthropic Messages.
func (openAIAnthropicChat) TranslateRequest(body []byte) ([]byte, []Drop, error) {
	return translateChatRequestToMessages(body)
}

// TranslateResponse maps a non-streaming Anthropic Messages response body back
// to OpenAI Chat.
func (openAIAnthropicChat) TranslateResponse(body []byte) ([]byte, []Drop, error) {
	return translateMessagesResponseToChat(body)
}

// NewStreamTranslator returns a fresh stateful translator for one streaming
// Anthropic Messages response, satisfying StreamCapable so the gateway can
// translate streaming responses (not just non-streaming ones).
func (openAIAnthropicChat) NewStreamTranslator() StreamTranslator {
	return &messagesToChatStream{}
}

// TranslateError maps an Anthropic error response body to the OpenAI error
// envelope, satisfying ErrorTranslator.
func (openAIAnthropicChat) TranslateError(status int, body []byte) ([]byte, error) {
	return translateMessagesErrorToChat(status, body)
}

// init registers the (chat, messages) translator with the default registry —
// the one permitted use of init in this codebase (polymorphic/translator
// registration).
func init() { Register(openAIAnthropicChat{}) }
