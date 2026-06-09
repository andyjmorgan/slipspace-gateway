package translate

import (
	"reflect"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/protocols/anthropic/messages"
	"github.com/andyjmorgan/sluice-gateway/protocols/openai/chat"
)

// Property-coverage meta-tests: every exported top-level field of the protocol
// models the (messages,chat) translator handles must be classified as either
// "mapped" or "dropped". A field in neither — e.g. one a provider adds in a
// future SDK — fails the test, forcing a conscious decision (map it, or
// register the drop) rather than letting it vanish silently. This is what keeps
// "every property is handled" true over time; the golden/differential tests
// assert the mappings are correct, this asserts none are forgotten.
//
// Scope is the top-level request/response envelopes — the most common place a
// provider grows a field. Nested polymorphic types (content blocks, tool
// shapes) are covered by the per-type golden + fuzz tests and the e2e matrix.

const (
	dispMapped  = "mapped"
	dispDropped = "dropped"
)

// requestFieldDisposition classifies every exported field of
// messages.MessagesRequest for the messages->chat request mapper.
var requestFieldDisposition = map[string]string{
	"Model":         dispMapped,  // -> chat.Model (verbatim)
	"MaxTokens":     dispMapped,  // -> chat.MaxTokens
	"Messages":      dispMapped,  // -> chat.Messages (with tool_result split)
	"System":        dispMapped,  // -> leading system message
	"Temperature":   dispMapped,  // -> chat.Temperature
	"TopP":          dispMapped,  // -> chat.TopP
	"TopK":          dispDropped, // no OpenAI equivalent
	"StopSequences": dispMapped,  // -> chat.Stop
	"Stream":        dispMapped,  // -> chat.Stream
	"Tools":         dispMapped,  // -> chat.Tools (function tools)
	"ToolChoice":    dispMapped,  // -> chat.ToolChoice (+ parallel_tool_calls)
	"Metadata":      dispMapped,  // user_id -> chat.User (rest dropped, signalled)
	"Thinking":      dispDropped, // no OpenAI equivalent
	"ServiceTier":   dispMapped,  // -> chat.ServiceTier
	"OutputConfig":  dispMapped,  // effort -> chat.ReasoningEffort

	"ContextManagement": dispDropped, // Anthropic context-management beta; no OpenAI equivalent (signalled)
}

// responseFieldDisposition classifies every exported field of
// chat.ChatCompletionResponse for the chat->messages response mapper.
var responseFieldDisposition = map[string]string{
	"ID":                dispMapped,  // -> messages.ID
	"Object":            dispDropped, // Anthropic envelope uses Type="message"
	"Created":           dispDropped, // no Anthropic equivalent
	"Model":             dispMapped,  // -> messages.Model
	"SystemFingerprint": dispDropped, // no Anthropic equivalent (signalled)
	"ServiceTier":       dispMapped,  // -> messages.Usage.ServiceTier
	"Choices":           dispMapped,  // first choice -> message + stop_reason
	"Usage":             dispMapped,  // -> messages.Usage
}

func TestRequestFieldCoverage_MessagesToChat(t *testing.T) {
	assertFieldCoverage(t, reflect.TypeOf(messages.MessagesRequest{}), requestFieldDisposition)
}

func TestResponseFieldCoverage_ChatToMessages(t *testing.T) {
	assertFieldCoverage(t, reflect.TypeOf(chat.ChatCompletionResponse{}), responseFieldDisposition)
}

// chatRequestFieldDisposition classifies every exported field of
// chat.ChatCompletionRequest for the chat->messages request mapper (the reverse
// pair).
var chatRequestFieldDisposition = map[string]string{
	"Model":               dispMapped,  // -> messages.Model (verbatim)
	"Messages":            dispMapped,  // -> system + user/assistant turns
	"MaxTokens":           dispMapped,  // -> messages.MaxTokens
	"MaxCompletionTokens": dispMapped,  // -> messages.MaxTokens (preferred)
	"Temperature":         dispMapped,  // -> messages.Temperature
	"TopP":                dispMapped,  // -> messages.TopP
	"N":                   dispDropped, // Anthropic returns a single message
	"Stream":              dispMapped,  // -> messages.Stream
	"StreamOptions":       dispDropped, // Anthropic always emits stream usage
	"Stop":                dispMapped,  // -> messages.StopSequences
	"PresencePenalty":     dispDropped, // no Anthropic equivalent
	"FrequencyPenalty":    dispDropped, // no Anthropic equivalent
	"LogitBias":           dispDropped, // no Anthropic equivalent
	"LogProbs":            dispDropped, // no Anthropic equivalent
	"TopLogProbs":         dispDropped, // no Anthropic equivalent
	"User":                dispMapped,  // -> messages.Metadata.UserID
	"Tools":               dispMapped,  // -> messages.Tools
	"ToolChoice":          dispMapped,  // -> messages.ToolChoice (+ disable_parallel)
	"ResponseFormat":      dispDropped, // no Anthropic equivalent
	"Seed":                dispDropped, // no Anthropic equivalent
	"Modalities":          dispDropped, // no Anthropic equivalent
	"ReasoningEffort":     dispMapped,  // -> messages.OutputConfig.Effort
	"ServiceTier":         dispMapped,  // -> messages.ServiceTier
	"Audio":               dispDropped, // no Anthropic equivalent
	"Metadata":            dispDropped, // only user_id maps (via User); bag dropped
	"Store":               dispDropped, // no Anthropic equivalent
	"ParallelToolCalls":   dispMapped,  // -> messages.ToolChoice.DisableParallelToolUse
}

// messagesResponseFieldDisposition classifies every exported field of
// messages.MessagesResponse for the messages->chat response mapper.
var messagesResponseFieldDisposition = map[string]string{
	"ID":                dispMapped,  // -> chat.ID
	"Type":              dispDropped, // OpenAI envelope uses Object="chat.completion"
	"Role":              dispMapped,  // -> choice message role
	"Content":           dispMapped,  // text -> content, tool_use -> tool_calls
	"Model":             dispMapped,  // -> chat.Model
	"StopReason":        dispMapped,  // -> choice finish_reason
	"StopSequence":      dispDropped, // OpenAI does not echo the matched stop string
	"Usage":             dispMapped,  // -> chat.Usage
	"Container":         dispDropped, // no OpenAI equivalent
	"StopDetails":       dispDropped, // no OpenAI equivalent
	"ContextManagement": dispDropped, // no OpenAI equivalent
}

func TestRequestFieldCoverage_ChatToMessages(t *testing.T) {
	assertFieldCoverage(t, reflect.TypeOf(chat.ChatCompletionRequest{}), chatRequestFieldDisposition)
}

func TestResponseFieldCoverage_MessagesToChat(t *testing.T) {
	assertFieldCoverage(t, reflect.TypeOf(messages.MessagesResponse{}), messagesResponseFieldDisposition)
}

// assertFieldCoverage fails when an exported, named field of typ is absent from
// disposition (an unclassified field — must be mapped or dropped), or when
// disposition carries a key that is no longer a field of typ (a stale entry).
// Embedded carriers (the DynamicProperties unknown-field sink) and unexported
// fields are skipped.
func assertFieldCoverage(t testing.TB, typ reflect.Type, disposition map[string]string) {
	t.Helper()

	present := make(map[string]struct{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Anonymous || f.PkgPath != "" {
			// Anonymous = embedded DynamicProperties (the unknown-field sink,
			// not a mapped field); PkgPath != "" = unexported.
			continue
		}
		present[f.Name] = struct{}{}
		d, ok := disposition[f.Name]
		if !ok {
			t.Errorf("%s.%s is unclassified — map it in the translator or register it as dropped, then add it to the disposition table", typ.Name(), f.Name)
			continue
		}
		if d != dispMapped && d != dispDropped {
			t.Errorf("%s.%s has invalid disposition %q, want %q or %q", typ.Name(), f.Name, d, dispMapped, dispDropped)
		}
	}

	for name := range disposition {
		if _, ok := present[name]; !ok {
			t.Errorf("disposition table lists %s.%s, which is no longer an exported field — remove the stale entry", typ.Name(), name)
		}
	}
}

// TestFieldCoverage_TrapWorks proves the meta-test actually fails on an
// unclassified field, so the guard-rail can be trusted not to silently pass.
func TestFieldCoverage_TrapWorks(t *testing.T) {
	type sample struct {
		Known   string
		Unknown string
	}
	rec := &recordingT{}
	assertFieldCoverage(rec, reflect.TypeOf(sample{}), map[string]string{"Known": dispMapped})
	if !rec.failed {
		t.Error("assertFieldCoverage did not flag an unclassified field; the trap is broken")
	}
}

// recordingT is a minimal testing.TB stand-in that records whether an error was
// reported, so TestFieldCoverage_TrapWorks can assert the trap fires without
// failing the real test.
type recordingT struct {
	testing.TB
	failed bool
}

func (r *recordingT) Errorf(string, ...any) { r.failed = true }
func (r *recordingT) Helper()               {}
