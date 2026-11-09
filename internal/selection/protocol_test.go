package selection_test

import (
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/selection"
)

func TestProtocolForPath(t *testing.T) {
	cases := []struct {
		path     string
		protocol string
		ok       bool
		model    string
		op       string
		version  string
	}{
		{"/v1/chat/completions", selection.ProtocolChat, true, "", "", ""},
		{"/chat/completions", selection.ProtocolChat, true, "", "", ""},
		{"/v1/responses", selection.ProtocolResponses, true, "", "", ""},
		{"/responses", selection.ProtocolResponses, true, "", "", ""},
		{"/v1/messages", selection.ProtocolMessages, true, "", "", ""},
		{"/messages", selection.ProtocolMessages, true, "", "", ""},
		{"/v1/embeddings", selection.ProtocolEmbeddings, true, "", "", ""},
		{"/v1beta/models/gemini-2.5-pro:generateContent", selection.ProtocolGenerateContent, true, "gemini-2.5-pro", "generateContent", "v1beta"},
		{"/v1beta/models/gemini-2.5-flash:streamGenerateContent", selection.ProtocolGenerateContent, true, "gemini-2.5-flash", "streamGenerateContent", "v1beta"},
		// google-genai 3.0.0 defaults to the stable v1 surface, which shares
		// the /{version}/models/{model}:{op} shape (issue #631).
		{"/v1/models/gemini-2.5-pro:generateContent", selection.ProtocolGenerateContent, true, "gemini-2.5-pro", "generateContent", "v1"},
		{"/v1/models/gemini-2.5-flash:streamGenerateContent", selection.ProtocolGenerateContent, true, "gemini-2.5-flash", "streamGenerateContent", "v1"},
		{"/v1/messages/batches", "", false, "", "", ""},
		{"/v1beta/models/x:unknownOp", "", false, "", "", ""},
		{"/v1beta/models/a/b:generateContent", "", false, "", "", ""},
		{"/v1beta/models/:generateContent", "", false, "", "", ""},
		{"/v1/models/x:unknownOp", "", false, "", "", ""},
		{"/v1/models/a/b:generateContent", "", false, "", "", ""},
		{"/v1/models/:generateContent", "", false, "", "", ""},
		// The catalogue listings are passthrough, never generate_content.
		{"/v1/models", "", false, "", "", ""},
		{"/v1beta/models", "", false, "", "", ""},
		{"/v1/models/", "", false, "", "", ""},
		// Only the two documented Gemini API versions are recognised.
		{"/v2/models/gemini-2.5-pro:generateContent", "", false, "", "", ""},
		{"/v1alpha/models/gemini-2.5-pro:generateContent", "", false, "", "", ""},
		{"/models/gemini-2.5-pro:generateContent", "", false, "", "", ""},
		{"v1/models/gemini-2.5-pro:generateContent", "", false, "", "", ""},
		{"/unknown", "", false, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			proto, params, ok := selection.ProtocolForPath(tc.path)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if proto != tc.protocol {
				t.Errorf("protocol = %q, want %q", proto, tc.protocol)
			}
			if tc.model != "" && params["model"] != tc.model {
				t.Errorf("model = %q, want %q", params["model"], tc.model)
			}
			if tc.op != "" && params["op"] != tc.op {
				t.Errorf("op = %q, want %q", params["op"], tc.op)
			}
			if tc.version != "" && params["version"] != tc.version {
				t.Errorf("version = %q, want %q", params["version"], tc.version)
			}
		})
	}
}
