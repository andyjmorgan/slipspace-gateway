package translate

import (
	cfg "github.com/andyjmorgan/slipspace-gateway/contracts/config"
)

// anthropicOpenAIChat is the (messages -> chat) translator: it accepts an
// Anthropic Messages request/response and translates to/from OpenAI Chat
// Completions. It is the concrete pairing of the request and response mappers
// in this package.
type anthropicOpenAIChat struct{}

// Source reports the Anthropic Messages protocol.
func (anthropicOpenAIChat) Source() Protocol { return cfg.ProtocolMessages }

// Target reports the OpenAI Chat Completions protocol.
func (anthropicOpenAIChat) Target() Protocol { return cfg.ProtocolChat }

// TranslateRequest maps an Anthropic Messages request body to OpenAI Chat.
func (anthropicOpenAIChat) TranslateRequest(body []byte) ([]byte, []Drop, error) {
	return translateMessagesRequestToChat(body)
}

// TranslateResponse maps a non-streaming OpenAI Chat response body back to
// Anthropic Messages.
func (anthropicOpenAIChat) TranslateResponse(body []byte) ([]byte, []Drop, error) {
	return translateChatResponseToMessages(body)
}

// NewStreamTranslator returns a fresh stateful translator for one streaming
// OpenAI Chat response, satisfying StreamCapable so the gateway can translate
// streaming responses (not just non-streaming ones).
func (anthropicOpenAIChat) NewStreamTranslator() StreamTranslator {
	return &chatToMessagesStream{}
}

// TranslateError maps an OpenAI error response body to the Anthropic error
// envelope, satisfying ErrorTranslator.
func (anthropicOpenAIChat) TranslateError(status int, body []byte) ([]byte, error) {
	return translateChatErrorToMessages(status, body)
}

// init registers the (messages, chat) translator with the default registry.
// Registration into a package-level registry is the sanctioned use of init
// here (see CLAUDE.md); note the protocols/ polymorphic factories need no
// init at all — they are package-level var initializers.
func init() { Register(anthropicOpenAIChat{}) }
