package advise

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	contractsadvise "github.com/andyjmorgan/slipspace-gateway/contracts/advise"
	"github.com/andyjmorgan/slipspace-gateway/protocols/anthropic/messages"
)

// defaultRubric is the built-in judge instruction, overridable via
// advise.prompt_file. The candidate model list and the response schema are
// always appended programmatically so an operator-authored rubric cannot
// unshape the output contract.
const defaultRubric = `You are a routing judge for an AI gateway. A coding agent has spawned a
subagent; you see the subagent's task description, its system prompt prefix,
and its tool list. Decide whether this task is TRIVIAL enough that a smaller,
cheaper model would complete it with no meaningful quality loss.

Treat as trivial: simple lookups, file listing/reading, renaming, formatting,
single-file summaries, mechanical search-and-report tasks, short factual
questions. Treat as non-trivial: multi-step reasoning, writing or refactoring
non-trivial code, architectural analysis, debugging, security review, and
anything whose scope is unclear. When unsure, do not switch.`

// maxJudgeResponseBytes caps the judge's HTTP response body.
const maxJudgeResponseBytes = 1 << 20

// judgeAgentID is the named-agent id stamped on every judge request
// (X-Slipspace-Agent-Id), so judge inferences appear in telemetry as a named
// agent rather than anonymous SDK traffic.
const judgeAgentID = "advise-judge"

// ResolveRubric returns the effective judge rubric: the contents of
// promptFile when set, else the built-in default. Resolved once at startup —
// both the judge and the applied-config snapshot (served at /api/v1/settings)
// consume the same resolved text, so the console always shows exactly what
// the judge runs with.
func ResolveRubric(promptFile string) (string, error) {
	if promptFile == "" {
		return defaultRubric, nil
	}
	raw, err := os.ReadFile(promptFile) //nolint:gosec // operator-trusted config path
	if err != nil {
		return "", fmt.Errorf("advise: read prompt_file: %w", err)
	}
	return string(raw), nil
}

// Judge classifies advisory requests by prompting a judge model over the
// Anthropic messages wire shape (conventionally through a SlipSpace gateway
// on a dedicated advisor configuration).
type Judge struct {
	baseURL    string
	apiKey     string
	model      string
	rubric     string
	candidates []string
	// schema is the JSON schema the judge's output is server-side constrained
	// to (structured outputs, output_config.format). Built once from the
	// candidate list: the verdict's model field is an enum of the candidates
	// plus "" — the judge cannot emit a non-candidate model.
	schema map[string]any
	httpc  *http.Client
}

// verdictSchema builds the structured-outputs schema for advise.Verdict.
// Structured-outputs rules: every object needs additionalProperties:false;
// numeric range constraints are unsupported (confidence bounds live in the
// prompt); enum is supported and carries the candidate allow-list.
func verdictSchema(candidates []string) map[string]any {
	modelEnum := append([]string{""}, candidates...)
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"switch", "model", "reason", "confidence"},
		"properties": map[string]any{
			"switch": map[string]any{
				"type":        "boolean",
				"description": "true to re-route the conversation to a cheaper model, false to continue as configured",
			},
			"model": map[string]any{
				"type":        "string",
				"enum":        modelEnum,
				"description": "the candidate model to switch to; empty string when switch is false",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "short justification for the verdict",
			},
			"confidence": map[string]any{
				"type":        "number",
				"description": "self-reported confidence between 0 and 1",
			},
		},
	}
}

// NewJudge builds a judge. rubric empty applies the built-in default.
func NewJudge(baseURL, apiKey, model, rubric string, candidates []string, timeout time.Duration) *Judge {
	if rubric == "" {
		rubric = defaultRubric
	}
	return &Judge{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		rubric:     rubric,
		candidates: candidates,
		schema:     verdictSchema(candidates),
		httpc:      &http.Client{Timeout: timeout},
	}
}

// Judge implements the handler's judge interface.
func (j *Judge) Judge(ctx context.Context, req contractsadvise.Request) (contractsadvise.Verdict, error) {
	system := fmt.Sprintf(`%s

Candidate cheaper models you may recommend (pick exactly one when switching):
%s

Respond with ONLY a JSON object, no prose, matching:
{"switch": <bool>, "model": "<candidate model or empty>", "reason": "<short>", "confidence": <0..1>}`,
		j.rubric, "- "+strings.Join(j.candidates, "\n- "))

	user := fmt.Sprintf(`Agent family: %s (entrypoint %s, subagent %t)
Requested model: %s
Declared tools (%d): %s

System prompt prefix:
%s

Task (first user message):
%s`,
		req.AgentFamily, req.Entrypoint, req.IsSubagent,
		req.Model,
		len(req.ToolNames), strings.Join(req.ToolNames, ", "),
		req.SystemPrefix,
		req.FirstUserMessage)

	body := map[string]any{
		"model":      j.model,
		"max_tokens": 300,
		"system":     system,
		// Structured outputs (GA, no beta header; supported on the cheap
		// judge tiers incl. Haiku 4.5): the response is server-side
		// constrained to the verdict schema, with the model field an enum of
		// the candidates. parseVerdict below stays as the fallback for
		// upstreams that ignore the field.
		"output_config": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"schema": j.schema,
			},
		},
		"messages": []map[string]any{
			{"role": "user", "content": user},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return contractsadvise.Verdict{}, fmt.Errorf("advise: marshal judge request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, j.baseURL+"/v1/messages", bytes.NewReader(raw)) //nolint:gosec // G704: baseURL is the operator-configured judge upstream (advise.upstream.base_url), same trust model as connector URLs
	if err != nil {
		return contractsadvise.Verdict{}, fmt.Errorf("advise: build judge request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+j.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	// Link the judge inference to the conversation it is deciding for: the
	// gateway maps X-Slipspace-Thread-Id onto the span's conversation id, so
	// the console's conversation view shows the judge call alongside the
	// subagent's own requests. The named-agent id labels it as judge traffic.
	// Session id is deliberately NOT set — that would fold the judge's spend
	// into the user session's rollups.
	if req.ConversationID != "" {
		httpReq.Header.Set("X-Slipspace-Thread-Id", req.ConversationID)
	}
	httpReq.Header.Set("X-Slipspace-Agent-Id", judgeAgentID)

	resp, err := j.httpc.Do(httpReq) //nolint:gosec // G704: request URL is the operator-configured judge upstream, not user input
	if err != nil {
		return contractsadvise.Verdict{}, fmt.Errorf("advise: judge call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respRaw, err := io.ReadAll(io.LimitReader(resp.Body, maxJudgeResponseBytes))
	if err != nil {
		return contractsadvise.Verdict{}, fmt.Errorf("advise: read judge response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return contractsadvise.Verdict{}, fmt.Errorf("advise: judge status %d", resp.StatusCode)
	}

	var mresp messages.MessagesResponse
	if err := json.Unmarshal(respRaw, &mresp); err != nil {
		return contractsadvise.Verdict{}, fmt.Errorf("advise: decode judge response: %w", err)
	}
	text := responseText(&mresp)
	verdict, ok := parseVerdict(text)
	if !ok {
		// A malformed judge answer is a decided "continue", not an error —
		// retrying the same prompt would likely misbehave the same way.
		return contractsadvise.Verdict{Switch: false, Reason: "judge_malformed"}, nil
	}
	if verdict.Switch && !in(j.candidates, verdict.Model) {
		return contractsadvise.Verdict{Switch: false, Reason: "judge_noncandidate:" + verdict.Model}, nil
	}
	return verdict, nil
}

// responseText concatenates the text blocks of the judge's reply.
func responseText(resp *messages.MessagesResponse) string {
	var out strings.Builder
	for _, b := range resp.Content {
		if tb, ok := b.(*messages.TextBlock); ok {
			out.WriteString(tb.Text)
		}
	}
	return out.String()
}

// parseVerdict extracts the first JSON object from text and decodes it. The
// judge is instructed to answer with bare JSON, but models occasionally wrap
// it in prose or a code fence.
func parseVerdict(text string) (contractsadvise.Verdict, bool) {
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return contractsadvise.Verdict{}, false
	}
	var v contractsadvise.Verdict
	if err := json.Unmarshal([]byte(text[start:end+1]), &v); err != nil {
		return contractsadvise.Verdict{}, false
	}
	return v, true
}

// in reports whether s is in list.
func in(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
