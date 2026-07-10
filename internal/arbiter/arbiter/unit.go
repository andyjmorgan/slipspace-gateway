// Package arbiter is the SlipSpace Arbiter async security scanner that runs
// inside the Arbiter service. It reads the gen_ai span already landing in
// Postgres, explodes per-content-block check tasks into a durable outbox
// (ADR-004), calls CPU detector containers (PII / injection / toxicity), reduces
// findings to a verdict (ADR-017), and stores evidence. Detect-and-record only —
// no prevention this release (REF-008). It is optional: disabled by default,
// and the Arbiter is fully functional without it.
package arbiter

import (
	"encoding/json"
	"strings"

	"github.com/andyjmorgan/slipspace-gateway/internal/arbiter/store"
)

// Part kinds. genaiattr already normalizes every provider into these unified
// types at span-emit time (REF-008), so the scanner reads units straight from
// the stored parts rather than re-parsing provider bodies (ADR-015 subsumed).
const (
	kindText             = "text"
	kindToolCall         = "tool_call"
	kindToolCallResponse = "tool_call_response"
	kindReasoning        = "reasoning"
	// kindOpaque is the residual-capture catch-all for any part type the span
	// carries that we do not model — it is still scanned, never dropped
	// (non-negotiable #1, ADR-015).
	kindOpaque = "opaque"
)

// Unit is one content block to scan — a single genaiattr part (ADR-018 unit
// model). Text is the rendered scannable content; Locator records where it came
// from so a finding re-attaches to its source.
type Unit struct {
	// ID is the stable per-span unit id, e.g. "in:0:1" (input message 0, part 1).
	ID string
	// Kind is the normalized part kind (kindText, kindToolCallResponse, …).
	Kind string
	// Role is the turn author: "user", "assistant", "system", "tool".
	Role string
	// Text is the rendered content the detector scans.
	Text string
	// Locator is the JSON-serializable source pointer stored on the check task.
	Locator Locator
}

// Locator points a finding back at the exact content block it came from.
type Locator struct {
	Section  string `json:"section"` // "input" | "output"
	Message  int    `json:"message_index"`
	Part     int    `json:"part_index"`
	Kind     string `json:"kind"`
	Role     string `json:"role"`
	ToolName string `json:"tool_name,omitempty"`
	ToolID   string `json:"tool_id,omitempty"`
}

// genContent mirrors the stored span_event.gen_ai_content shape
// ({input_messages, output_messages} of [{role, parts}]). The parts retain the
// typed discriminator genaiattr assigned, which is what makes per-kind unit
// selection (ADR-018) possible.
type genContent struct {
	InputMessages  []genMessage `json:"input_messages"`
	OutputMessages []genMessage `json:"output_messages"`
}

type genMessage struct {
	Role  string    `json:"role"`
	Parts []genPart `json:"parts"`
}

type genPart struct {
	Type      string          `json:"type"`
	Content   string          `json:"content"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Response  string          `json:"response"`
	Result    string          `json:"result"`
}

func (p genPart) responseText() string {
	if p.Response != "" {
		return p.Response
	}
	return p.Result
}

// BuildUnits decodes a request event's stored gen_ai content into the scannable
// units. The span already carries only the latest turn (REF-008 latest-message
// scope), so this yields that turn's input + output parts — no cross-turn
// history to dedup. Units with no scannable text are dropped.
func BuildUnits(e store.RequestEvent) []Unit {
	content := e.DecodeSpanFields().GenAIContent
	if len(content) == 0 {
		return nil
	}
	var gc genContent
	if json.Unmarshal(content, &gc) != nil {
		return nil
	}
	var units []Unit
	units = appendUnits(units, "in", gc.InputMessages)
	units = appendUnits(units, "out", gc.OutputMessages)
	return units
}

func appendUnits(units []Unit, section string, msgs []genMessage) []Unit {
	sectionLong := "input"
	if section == "out" {
		sectionLong = "output"
	}
	for mi, m := range msgs {
		for pi, p := range m.Parts {
			kind, text := renderPart(p)
			if text == "" {
				continue
			}
			units = append(units, Unit{
				ID:   unitID(section, mi, pi),
				Kind: kind,
				Role: m.Role,
				Text: text,
				Locator: Locator{
					Section:  sectionLong,
					Message:  mi,
					Part:     pi,
					Kind:     kind,
					Role:     m.Role,
					ToolName: p.Name,
					ToolID:   p.ID,
				},
			})
		}
	}
	return units
}

// renderPart maps a stored part to its normalized kind and the plain text a
// detector should scan. Unknown part types fall to kindOpaque with their text
// content so residual capture still scans them (#1).
func renderPart(p genPart) (kind, text string) {
	switch p.Type {
	case kindText:
		return kindText, p.Content
	case kindReasoning:
		return kindReasoning, p.Content
	case kindToolCallResponse:
		return kindToolCallResponse, p.responseText()
	case kindToolCall:
		// Scan the call's name + argument JSON — injection/PII can ride in args.
		var b strings.Builder
		b.WriteString(p.Name)
		if len(p.Arguments) > 0 {
			b.WriteByte('\n')
			b.Write(p.Arguments)
		}
		return kindToolCall, strings.TrimSpace(b.String())
	default:
		// Residual capture: an unmodeled block kind is scanned as opaque text.
		return kindOpaque, p.Content
	}
}

func unitID(section string, msg, part int) string {
	var b strings.Builder
	b.WriteString(section)
	b.WriteByte(':')
	b.WriteString(itoa(msg))
	b.WriteByte(':')
	b.WriteString(itoa(part))
	return b.String()
}

// itoa avoids strconv for two small non-negative ints in a hot path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
