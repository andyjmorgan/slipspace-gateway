package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrThinkNotBool is returned by ThinkOption.Bool when the underlying wire
// value is not a JSON bool (either absent, null, or a level string).
var ErrThinkNotBool = errors.New("openai/chat: think is not a bool")

// ErrThinkNotLevel is returned by ThinkOption.Level when the underlying wire
// value is not a JSON string (either absent, null, or a bool).
var ErrThinkNotLevel = errors.New("openai/chat: think is not a level string")

// ThinkOption is Ollama's polymorphic "think" request field: a JSON bool
// toggling thinking on or off, or a level string ("high", "medium", "low",
// "max") on models that support graded thinking. The raw bytes are retained
// verbatim so the exact wire shape (bool vs string) round-trips
// byte-equivalent; use IsBool/IsLevel to inspect the shape and Bool/Level to
// project it. Same raw-preservation pattern as MessageContent.
// Ref: https://docs.ollama.com/api/chat
type ThinkOption struct {
	raw json.RawMessage
}

// NewThinkBool builds a ThinkOption that marshals as the JSON bool v.
func NewThinkBool(v bool) ThinkOption {
	raw, _ := json.Marshal(v)
	return ThinkOption{raw: raw}
}

// NewThinkLevel builds a ThinkOption that marshals as the JSON string level
// (e.g., "high"). The level is not validated — the provider owns the
// vocabulary and this package only preserves the wire shape.
func NewThinkLevel(level string) ThinkOption {
	raw, _ := json.Marshal(level)
	return ThinkOption{raw: raw}
}

// UnmarshalJSON records the raw bytes verbatim, deferring shape detection to
// IsBool/IsLevel and decoding to Bool/Level. Any JSON value is accepted so an
// unanticipated provider shape still round-trips intact.
func (t *ThinkOption) UnmarshalJSON(data []byte) error {
	t.raw = append(t.raw[:0], data...)
	return nil
}

// MarshalJSON returns the raw think payload. An empty ThinkOption emits JSON
// null so it round-trips identically to an explicit null.
func (t ThinkOption) MarshalJSON() ([]byte, error) {
	if len(t.raw) == 0 {
		return []byte("null"), nil
	}
	return t.raw, nil
}

// Raw returns a defensive copy of the underlying JSON bytes.
func (t ThinkOption) Raw() json.RawMessage {
	out := make(json.RawMessage, len(t.raw))
	copy(out, t.raw)
	return out
}

// IsBool reports whether the think value is a JSON bool.
func (t ThinkOption) IsBool() bool {
	trimmed := bytes.TrimSpace(t.raw)
	return bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false"))
}

// IsLevel reports whether the think value is a JSON string (a thinking
// level such as "high").
func (t ThinkOption) IsLevel() bool {
	trimmed := bytes.TrimSpace(t.raw)
	return len(trimmed) > 0 && trimmed[0] == '"'
}

// Bool decodes the think value as a JSON bool. Returns ErrThinkNotBool when
// the underlying shape is anything else.
func (t ThinkOption) Bool() (bool, error) {
	if !t.IsBool() {
		return false, ErrThinkNotBool
	}
	var v bool
	if err := json.Unmarshal(t.raw, &v); err != nil {
		return false, fmt.Errorf("openai/chat: decode think bool: %w", err)
	}
	return v, nil
}

// Level decodes the think value as a JSON string (e.g., "high"). Returns
// ErrThinkNotLevel when the underlying shape is anything else.
func (t ThinkOption) Level() (string, error) {
	if !t.IsLevel() {
		return "", ErrThinkNotLevel
	}
	var v string
	if err := json.Unmarshal(t.raw, &v); err != nil {
		return "", fmt.Errorf("openai/chat: decode think level: %w", err)
	}
	return v, nil
}
