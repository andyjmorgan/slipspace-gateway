package translate_test

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/translate"
)

// stubTranslator is a minimal Translator for registry tests. It does no real
// mapping — these tests exercise registration and lookup, not translation.
type stubTranslator struct {
	src, target string
}

func (s stubTranslator) Source() translate.Protocol                 { return s.src }
func (s stubTranslator) Target() translate.Protocol                 { return s.target }
func (s stubTranslator) TranslateRequest(b []byte) ([]byte, error)  { return b, nil }
func (s stubTranslator) TranslateResponse(b []byte) ([]byte, error) { return b, nil }

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := translate.NewRegistry()
	want := stubTranslator{src: "messages", target: "chat"}
	r.Register(want)

	got, ok := r.Lookup("messages", "chat")
	if !ok {
		t.Fatal("Lookup(messages, chat) = _, false; want registered translator")
	}
	if got.Source() != "messages" || got.Target() != "chat" {
		t.Errorf("Lookup returned %q->%q; want messages->chat", got.Source(), got.Target())
	}
}

func TestRegistry_LookupMiss_IsFailClosed(t *testing.T) {
	r := translate.NewRegistry()
	r.Register(stubTranslator{src: "messages", target: "chat"})

	// Reverse direction is a distinct pair and must not resolve.
	if _, ok := r.Lookup("chat", "messages"); ok {
		t.Error("Lookup(chat, messages) resolved; pairs are ordered, reverse must miss")
	}
	// Unregistered pair misses — the destination builder's fail-closed signal.
	if _, ok := r.Lookup("messages", "responses"); ok {
		t.Error("Lookup(messages, responses) resolved; want miss")
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	r := translate.NewRegistry()
	r.Register(stubTranslator{src: "messages", target: "chat"})
	defer func() {
		if recover() == nil {
			t.Error("Register of duplicate pair did not panic")
		}
	}()
	r.Register(stubTranslator{src: "messages", target: "chat"})
}

func TestRegistry_IdentityPanics(t *testing.T) {
	r := translate.NewRegistry()
	defer func() {
		if recover() == nil {
			t.Error("Register of identity (src == target) translator did not panic")
		}
	}()
	r.Register(stubTranslator{src: "chat", target: "chat"})
}

func TestDefaultRegistry_LookupMissByDefault(t *testing.T) {
	// No concrete translators are registered yet, so the default registry must
	// miss every pair — translation is fail-closed until a translator ships.
	if _, ok := translate.Lookup("messages", "chat"); ok {
		t.Error("default Lookup(messages, chat) resolved; no translators registered yet")
	}
}
