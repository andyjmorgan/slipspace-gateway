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

func (s stubTranslator) Source() translate.Protocol { return s.src }
func (s stubTranslator) Target() translate.Protocol { return s.target }
func (s stubTranslator) TranslateRequest(b []byte) ([]byte, []translate.Drop, error) {
	return b, nil, nil
}
func (s stubTranslator) TranslateResponse(b []byte) ([]byte, []translate.Drop, error) {
	return b, nil, nil
}

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
	// A pair with no registered translator must miss — translation is
	// fail-closed. (messages->chat IS registered, so probe an unregistered
	// pair to assert the fail-closed signal.)
	if _, ok := translate.Lookup("messages", "responses"); ok {
		t.Error("default Lookup(messages, responses) resolved; that pair has no translator")
	}
}

func TestDefaultRegistry_PackageRegisterAndLookup(t *testing.T) {
	// Exercises the package-level Register/Lookup wrappers against the
	// process-wide default registry. Synthetic protocol names are used so this
	// never collides with a real pair (or the fail-closed-by-default check).
	const src, target = "test-src-proto", "test-dst-proto"
	translate.Register(stubTranslator{src: src, target: target})

	got, ok := translate.Lookup(src, target)
	if !ok {
		t.Fatalf("default Lookup(%q, %q) = _, false; want the just-registered translator", src, target)
	}
	if got.Source() != src || got.Target() != target {
		t.Errorf("default Lookup returned %q->%q; want %q->%q", got.Source(), got.Target(), src, target)
	}
}
