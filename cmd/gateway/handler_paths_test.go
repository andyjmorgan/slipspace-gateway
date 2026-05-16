package main

import (
	"testing"
)

// substitutePlaceholders + joinPaths are leaf URL helpers. They're
// pure, trivial to test in isolation, and silently load-bearing —
// substitutePlaceholders is how Gemini's
// /v1beta/models/{model}:generateContent gets the model spliced in;
// joinPaths handles the base-URL vs endpoint-path slash collisions.
// Either regressing silently means every Gemini request hits a
// literal {model} or the upstream URL is malformed.

func TestSubstitutePlaceholders(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		path   string
		params map[string]string
		want   string
	}{
		{
			name:   "single placeholder substitutes",
			path:   "/v1beta/models/{model}:generateContent",
			params: map[string]string{"model": "gemini-2.0-flash-001"},
			want:   "/v1beta/models/gemini-2.0-flash-001:generateContent",
		},
		{
			name:   "no placeholder in path is no-op",
			path:   "/v1/chat/completions",
			params: map[string]string{"model": "gpt-4o"},
			want:   "/v1/chat/completions",
		},
		{
			name:   "nil params is no-op (early return)",
			path:   "/v1beta/models/{model}:generateContent",
			params: nil,
			want:   "/v1beta/models/{model}:generateContent",
		},
		{
			name:   "empty params is no-op (early return)",
			path:   "/v1beta/models/{model}:generateContent",
			params: map[string]string{},
			want:   "/v1beta/models/{model}:generateContent",
		},
		{
			name:   "multiple placeholders substitute independently",
			path:   "/v1/{tenant}/{model}/invoke",
			params: map[string]string{"tenant": "acme", "model": "gpt-4o"},
			want:   "/v1/acme/gpt-4o/invoke",
		},
		{
			name:   "unmatched key leaves placeholder intact",
			path:   "/v1/{model}",
			params: map[string]string{"notmodel": "x"},
			want:   "/v1/{model}",
		},
		{
			name:   "same placeholder twice substitutes both",
			path:   "/v1/{x}/and/{x}",
			params: map[string]string{"x": "ok"},
			want:   "/v1/ok/and/ok",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := substitutePlaceholders(tc.path, tc.params); got != tc.want {
				t.Errorf("substitutePlaceholders(%q, %v) = %q, want %q", tc.path, tc.params, got, tc.want)
			}
		})
	}
}

func TestJoinPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		base   string
		target string
		want   string
	}{
		{"base trailing-slash + target leading-slash collapses one", "http://upstream/", "/v1/x", "http://upstream/v1/x"},
		{"base no-slash + target leading-slash joins cleanly", "http://upstream", "/v1/x", "http://upstream/v1/x"},
		{"base trailing-slash + target no-leading-slash joins cleanly", "http://upstream/", "v1/x", "http://upstream/v1/x"},
		{"base no-slash + target no-leading-slash inserts separator", "http://upstream", "v1/x", "http://upstream/v1/x"},
		{"empty base returns target verbatim", "", "/v1/x", "/v1/x"},
		{"empty target returns base verbatim", "http://upstream", "", "http://upstream"},
		{"both empty returns empty", "", "", ""},
		{"base with path + target leading-slash", "http://upstream/api/", "/v1/x", "http://upstream/api/v1/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := joinPaths(tc.base, tc.target); got != tc.want {
				t.Errorf("joinPaths(%q, %q) = %q, want %q", tc.base, tc.target, got, tc.want)
			}
		})
	}
}
