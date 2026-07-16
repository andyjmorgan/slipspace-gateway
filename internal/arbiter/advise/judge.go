package advise

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// Judge classifies advisory requests by prompting a judge model over the
// Anthropic messages wire shape (conventionally through a SlipSpace gateway
// on a dedicated advisor configuration).
type Judge struct {
	baseURL    string
	apiKey     string
	model      string
	rubric     string
	candidates []string
	httpc      *http.Client
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
