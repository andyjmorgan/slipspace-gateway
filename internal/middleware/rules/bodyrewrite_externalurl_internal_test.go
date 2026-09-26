package rules

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/bodypatch"
)

// TestBodyRewriteHandler_ExternalURLResolvesOnRequestBody pins issue #482:
// the request-phase handler must populate Refs.ExternalURL so a
// request.body rewrite templated on {external_url} splices the configured
// gateway URL in, exactly as the response-phase ApplyResponseRewrites does.
// With no external URL configured a pure-ref template misses (dropped with
// template_ref_miss) and the body forwards unmodified.
func TestBodyRewriteHandler_ExternalURLResolvesOnRequestBody(t *testing.T) {
	tests := []struct {
		name        string
		externalURL string
		template    string
		wantBody    string
	}{
		{
			name:        "configured: mixed template splices the URL",
			externalURL: "https://gw.example.com",
			template:    "{external_url}/hook",
			wantBody:    `{"model":"x","metadata":{"callback":"https://gw.example.com/hook"}}`,
		},
		{
			name:        "configured: pure ref resolves",
			externalURL: "https://gw.example.com",
			template:    "{external_url}",
			wantBody:    `{"model":"x","metadata":{"callback":"https://gw.example.com"}}`,
		},
		{
			// A pure-ref template misses when the URL is unset (mixed
			// templates substitute empty per bodypatch's design), so the
			// op drops with template_ref_miss and the body is untouched.
			name:        "unset: pure ref misses, body untouched",
			externalURL: "",
			template:    "{external_url}",
			wantBody:    `{"model":"x"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody string
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
			})
			h := BodyRewriteHandler(testMeters(t), tt.externalURL, next)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.NopCloser(stringReader(`{"model":"x"}`)))
			state := &MutableState{BodyRewrites: []bodypatch.Op{
				{Kind: bodypatch.OpSet, Path: "metadata.callback", Value: tmpl(tt.template), ActionType: "rewriteField"},
			}}
			req = req.WithContext(WithMutableState(req.Context(), state))

			h.ServeHTTP(httptest.NewRecorder(), req)

			if gotBody != tt.wantBody {
				t.Fatalf("body = %s, want %s", gotBody, tt.wantBody)
			}
		})
	}
}
