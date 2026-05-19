package admin

import "testing"

func TestRedact_EmptyAndShort(t *testing.T) {
	t.Run("empty returns zero", func(t *testing.T) {
		got := redact("")
		if got.Length != 0 || got.Last4 != "" {
			t.Errorf("redact(empty) = %+v", got)
		}
	})
	t.Run("≤4 runes returns full value", func(t *testing.T) {
		got := redact("abcd")
		if got.Length != 4 || got.Last4 != "abcd" {
			t.Errorf("redact(abcd) = %+v", got)
		}
		got = redact("ab")
		if got.Length != 2 || got.Last4 != "ab" {
			t.Errorf("redact(ab) = %+v", got)
		}
	})
	t.Run("longer secret keeps last 4", func(t *testing.T) {
		got := redact("sk_dev_local_abcdef")
		if got.Length != len("sk_dev_local_abcdef") {
			t.Errorf("Length = %d", got.Length)
		}
		if got.Last4 != "cdef" {
			t.Errorf("Last4 = %q, want cdef", got.Last4)
		}
	})
	t.Run("unicode is rune-aware", func(t *testing.T) {
		got := redact("αβγδεζη") // 7 runes
		if got.Length != 7 {
			t.Errorf("Length = %d", got.Length)
		}
		if got.Last4 != "δεζη" {
			t.Errorf("Last4 = %q", got.Last4)
		}
	})
}

func TestRedactMap_EmptyVsNil(t *testing.T) {
	if out := redactMap(nil); len(out) != 0 {
		t.Errorf("redactMap(nil) = %v", out)
	}
	if out := redactMap(map[string]string{}); len(out) != 0 {
		t.Errorf("redactMap(empty) = %v", out)
	}
	out := redactMap(map[string]string{"openai": "abcdefghij"})
	if out["openai"].Last4 != "ghij" {
		t.Errorf("openai = %+v", out["openai"])
	}
}
