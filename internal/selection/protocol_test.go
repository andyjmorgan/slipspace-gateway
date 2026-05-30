package selection_test

import (
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/selection"
)

func TestProtocolForPath(t *testing.T) {
	cases := []struct {
		path     string
		protocol string
		ok       bool
		model    string
		op       string
	}{
		{"/v1/chat/completions", selection.ProtocolChat, true, "", ""},
		{"/chat/completions", selection.ProtocolChat, true, "", ""},
		{"/v1/responses", selection.ProtocolResponses, true, "", ""},
		{"/responses", selection.ProtocolResponses, true, "", ""},
		{"/v1/messages", selection.ProtocolMessages, true, "", ""},
		{"/messages", selection.ProtocolMessages, true, "", ""},
		{"/v1/embeddings", selection.ProtocolEmbeddings, true, "", ""},
		{"/v1beta/models/gemini-2.5-pro:generateContent", selection.ProtocolGenerateContent, true, "gemini-2.5-pro", "generateContent"},
		{"/v1beta/models/gemini-2.5-flash:streamGenerateContent", selection.ProtocolGenerateContent, true, "gemini-2.5-flash", "streamGenerateContent"},
		{"/v1/messages/batches", "", false, "", ""},
		{"/v1beta/models/x:unknownOp", "", false, "", ""},
		{"/v1beta/models/a/b:generateContent", "", false, "", ""},
		{"/v1beta/models/:generateContent", "", false, "", ""},
		{"/unknown", "", false, "", ""},
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
		})
	}
}
