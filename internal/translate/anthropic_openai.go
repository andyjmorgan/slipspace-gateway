package translate

import (
	cfg "github.com/andyjmorgan/sluice-gateway/contracts/config"
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

// init registers the (messages, chat) translator with the default registry —
// the one permitted use of init in this codebase (polymorphic/translator
// registration).
func init() { Register(anthropicOpenAIChat{}) }
