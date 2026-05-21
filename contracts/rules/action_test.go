package rules_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/contracts/rules"
)

func TestUnmarshalAction_ChangeProvider(t *testing.T) {
	raw := `{"type":"changeProvider","new_provider":"anthropic"}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cp, ok := act.(*rules.ChangeProviderAction)
	if !ok {
		t.Fatalf("type = %T", act)
	}
	if cp.NewProvider != "anthropic" {
		t.Errorf("NewProvider = %q", cp.NewProvider)
	}
	if cp.ActionType() != "changeProvider" {
		t.Errorf("ActionType = %q", cp.ActionType())
	}
}

func TestUnmarshalAction_ChangeModelName(t *testing.T) {
	raw := `{"type":"changeModelName","new_model_name":"gpt-4o-mini"}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cm, ok := act.(*rules.ChangeModelNameAction)
	if !ok {
		t.Fatalf("type = %T", act)
	}
	if cm.NewModelName != "gpt-4o-mini" {
		t.Errorf("bad: %+v", cm)
	}
}

func TestUnmarshalAction_ChangeUrl(t *testing.T) {
	raw := `{"type":"changeUrl","new_url":"https://api.example.com"}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cu, ok := act.(*rules.ChangeUrlAction)
	if !ok {
		t.Fatalf("type = %T", act)
	}
	if cu.NewURL != "https://api.example.com" {
		t.Errorf("bad: %+v", cu)
	}
}

func TestUnmarshalAction_ChangeApiKey(t *testing.T) {
	raw := `{"type":"changeApiKey","api_key":"sk-xxx","use_sluice_key":true}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ck, ok := act.(*rules.ChangeApiKeyAction)
	if !ok {
		t.Fatalf("type = %T", act)
	}
	if ck.APIKey != "sk-xxx" || !ck.UseSluiceKey {
		t.Errorf("bad: %+v", ck)
	}
}

func TestUnmarshalAction_SetHeader(t *testing.T) {
	raw := `{"type":"setHeader","header_name":"X-Routing","header_action":"Set","header_value":"foo"}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sh, ok := act.(*rules.SetHeaderAction)
	if !ok {
		t.Fatalf("type = %T", act)
	}
	if sh.HeaderName != "X-Routing" || sh.HeaderAction != rules.HeaderSet || sh.HeaderValue != "foo" {
		t.Errorf("bad: %+v", sh)
	}
}

func TestUnmarshalAction_AppendQueryString(t *testing.T) {
	raw := `{"type":"appendQueryString","key":"tenant","value":"acme"}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	aq, ok := act.(*rules.AppendQueryStringAction)
	if !ok {
		t.Fatalf("type = %T", act)
	}
	if aq.Key != "tenant" || aq.Value != "acme" {
		t.Errorf("bad: %+v", aq)
	}
}

func TestUnmarshalAction_ReturnStatusCode(t *testing.T) {
	raw := `{"type":"returnStatusCode","status_code":429,"body":"slow down","body_type":"text"}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rs, ok := act.(*rules.ReturnStatusCodeAction)
	if !ok {
		t.Fatalf("type = %T", act)
	}
	if rs.StatusCode != 429 || rs.BodyType != rules.StatusBodyText || rs.Body != "slow down" {
		t.Errorf("bad: %+v", rs)
	}
}

func TestUnmarshalAction_LlmImpersonation(t *testing.T) {
	raw := `{"type":"llmImpersonation","message":"blocked"}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	li, ok := act.(*rules.LlmImpersonationAction)
	if !ok {
		t.Fatalf("type = %T", act)
	}
	if li.Message != "blocked" {
		t.Errorf("Message = %q", li.Message)
	}
}

func TestUnmarshalAction_Unknown_FallsBack(t *testing.T) {
	raw := `{"type":"futureAction","payload":{"k":1}}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ua, ok := act.(*rules.UnknownAction)
	if !ok {
		t.Fatalf("type = %T", act)
	}
	if ua.ActionType() != "futureAction" {
		t.Errorf("ActionType = %q", ua.ActionType())
	}
	if _, ok := ua.Extra["payload"]; !ok {
		t.Errorf("payload missing from Extra: %v", ua.Extra)
	}
}

func TestUnmarshalAction_RoundTrip_UnknownPreserved(t *testing.T) {
	raw := `{"type":"setHeader","header_name":"X-Foo","header_action":"Append","header_value":"bar","experimental":true}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(act)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got, want map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("got unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		t.Fatalf("want unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip diverged.\n got=%s\nwant=%s", out, raw)
	}
}

func TestUnknownAction_RoundTrip(t *testing.T) {
	raw := `{"type":"newKind","custom":"x","nested":{"k":1}}`
	act, err := rules.UnmarshalAction([]byte(raw))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(act)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got, want map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("got unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		t.Fatalf("want unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip diverged.\n got=%s\nwant=%s", out, raw)
	}
}

func TestUnmarshalAction_MissingDiscriminator(t *testing.T) {
	_, err := rules.UnmarshalAction([]byte(`{"header_name":"X-Foo"}`))
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestActionTypes_BlankImpl(t *testing.T) {
	tests := []struct {
		name string
		act  rules.Action
		want string
	}{
		{"changeProvider", rules.ChangeProviderAction{}, "changeProvider"},
		{"changeModelName", rules.ChangeModelNameAction{}, "changeModelName"},
		{"changeUrl", rules.ChangeUrlAction{}, "changeUrl"},
		{"changeApiKey", rules.ChangeApiKeyAction{}, "changeApiKey"},
		{"setHeader", rules.SetHeaderAction{}, "setHeader"},
		{"appendQueryString", rules.AppendQueryStringAction{}, "appendQueryString"},
		{"returnStatusCode", rules.ReturnStatusCodeAction{}, "returnStatusCode"},
		{"llmImpersonation", rules.LlmImpersonationAction{}, "llmImpersonation"},
		{"addTag", rules.AddTagAction{}, "addTag"},
		{"unknown", rules.UnknownAction{Type: "z"}, "z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.act.ActionType(); got != tt.want {
				t.Errorf("ActionType = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOutcome_TerminatingResponse(t *testing.T) {
	o := rules.Outcome{
		Terminate: true,
		Response: &rules.Response{
			StatusCode: 429,
			Body:       []byte("slow"),
			BodyType:   rules.StatusBodyText,
		},
	}
	if !o.Terminate || o.Response.StatusCode != 429 {
		t.Errorf("outcome shape: %+v", o)
	}
}
