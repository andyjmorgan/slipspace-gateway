package agentroute

import (
	"encoding/json"
	"testing"

	"github.com/andyjmorgan/slipspace-gateway/internal/bodypatch"
	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
)

// reqFromJSON decodes a MessagesRequest through the real unmarshaller so
// unknown output_config keys land in DynamicProperties.Extra exactly as they
// would on the wire.
func reqFromJSON(t *testing.T, raw string) *messages.MessagesRequest {
	t.Helper()
	var req messages.MessagesRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return &req
}

func TestReconcileBody(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		pinned   string
		wantPath string // "" = no ops expected
	}{
		{
			// The observed live failure: a fable client's effort forwarded
			// across a haiku pin is a 400 on every pinned request.
			name:     "haiku pin strips effort-only output_config wholesale",
			raw:      `{"model":"claude-fable-5","output_config":{"effort":"high"}}`,
			pinned:   "claude-haiku-4-5",
			wantPath: "output_config",
		},
		{
			name:     "haiku pin strips only effort when format is present",
			raw:      `{"model":"m","output_config":{"effort":"high","format":{"type":"json_schema","schema":{}}}}`,
			pinned:   "claude-haiku-4-5",
			wantPath: "output_config.effort",
		},
		{
			name:     "haiku pin strips only effort when unknown keys ride output_config",
			raw:      `{"model":"m","output_config":{"effort":"high","future_control":true}}`,
			pinned:   "claude-haiku-4-5",
			wantPath: "output_config.effort",
		},
		{
			name:   "haiku pin with no effort leaves the body alone",
			raw:    `{"model":"m","output_config":{"format":{"type":"json_schema","schema":{}}}}`,
			pinned: "claude-haiku-4-5",
		},
		{
			name:   "haiku pin with no output_config leaves the body alone",
			raw:    `{"model":"m"}`,
			pinned: "claude-haiku-4-5",
		},
		{
			name:   "non-haiku pin never touches effort",
			raw:    `{"model":"m","output_config":{"effort":"high"}}`,
			pinned: "claude-sonnet-5",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ops := ReconcileBody(reqFromJSON(t, tc.raw), tc.pinned)
			if tc.wantPath == "" {
				if len(ops) != 0 {
					t.Fatalf("ops = %+v, want none", ops)
				}
				return
			}
			if len(ops) != 1 {
				t.Fatalf("ops = %+v, want exactly one", ops)
			}
			op := ops[0]
			if op.Kind != bodypatch.OpRemove || op.Path != tc.wantPath || op.ActionType != pinActionType {
				t.Errorf("op = %+v, want OpRemove %q (%s)", op, tc.wantPath, pinActionType)
			}
		})
	}

	// Nil request (no typed capture) is a no-op, never a panic.
	if ops := ReconcileBody(nil, "claude-haiku-4-5"); ops != nil {
		t.Errorf("nil request ops = %+v, want nil", ops)
	}
}
