package accumulator

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/andyjmorgan/slipspace-gateway/models"
	geminicontent "github.com/andyjmorgan/slipspace-gateway/protocols/gemini/content"
)

// accumulateGeminiContent walks a Gemini streamGenerateContent SSE
// stream and reassembles it into the JSON-encoded
// GenerateContentResponse the non-streaming endpoint would have
// returned.
//
// Gemini emits one GenerateContentResponse per SSE event with the
// same shape as the non-streaming endpoint, so reassembly is a
// per-candidate merge across chunks: adjacent TextPart fragments
// concatenate, FunctionCallPart entries land in arrival order, and
// the last non-empty FinishReason / UsageMetadata / ModelVersion /
// ResponseID / ModelStatus / PromptFeedback / Error (and any unknown
// top-level fields, per key) overwrites the running base. Candidate
// metadata Gemini delivers on its own chunks (groundingMetadata,
// urlContextMetadata, citationMetadata, safetyRatings, finishMessage,
// logprobsResult and any unknown fields) is likewise carried
// last-non-empty-wins so it survives reassembly. Non-empty means
// carrying content: a placeholder `{}` or `null` arriving after the
// populated chunk never overwrites a populated subtree. Unknown part
// kinds round-trip via the typed Part registry.
func accumulateGeminiContent(raw []byte) Result {
	state := newGeminiState()
	res := Result{}

	for _, ev := range parseSSE(raw) {
		if ev.Data == "" {
			continue
		}
		var chunk geminicontent.GenerateContentResponse
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			res.Partial = true
			continue
		}
		state.absorb(&chunk)
	}

	out, err := json.Marshal(state.assemble())
	if err != nil {
		res.Partial = true
		return res
	}
	res.Assembled = out
	return res
}

type geminiState struct {
	base       geminicontent.GenerateContentResponse
	candidates map[int]*geminiCandidateState
	candOrder  []int
}

type geminiCandidateState struct {
	index int
	role  string
	// parts holds the candidate's content parts in the exact order Gemini
	// streamed them — text, executableCode, codeExecutionResult, ... must
	// round-trip in place. Consecutive plain text deltas are merged in
	// appendPart; everything else keeps its position.
	parts        []geminicontent.Part
	finishReason *string
	tokenCount   *int
	// Candidate-level metadata Gemini delivers on its own chunks (usually the
	// terminal one) — carried last-non-empty-wins so it survives reassembly
	// rather than being dropped, where "empty" includes a non-nil pointer to a
	// zero struct and a raw `{}`/`null` (a wire placeholder must not wipe a
	// previously populated subtree). groundingMetadata is the load-bearing one
	// (Google Search web grounding); the rest round-trip for fidelity.
	finishMessage      *string
	safetyRatings      []geminicontent.SafetyRating
	citationMetadata   *geminicontent.CitationMetadata
	groundingMetadata  *geminicontent.GroundingMetadata
	urlContextMetadata json.RawMessage
	logprobsResult     json.RawMessage
	extra              map[string]json.RawMessage
}

func newGeminiState() *geminiState {
	return &geminiState{candidates: map[int]*geminiCandidateState{}}
}

// appendPart adds p to the candidate's ordered part list, preserving the
// stream order Gemini emitted. The one merge is consecutive plain TextParts:
// Gemini streams the model's prose as adjacent text deltas the non-streaming
// response would have delivered as a single block. A text part bearing a
// thought flag, thoughtSignature, or unknown fields is NOT plain and stays its
// own part so those fields survive; non-text parts (functionCall,
// executableCode, codeExecutionResult, ...) always keep their position so a
// text/code/text transcript round-trips in order rather than collapsing all
// text to the front.
func (st *geminiCandidateState) appendPart(p geminicontent.Part) {
	if tp, ok := p.(*geminicontent.TextPart); ok {
		if tp == nil {
			return
		}
		if tp.Text == "" && plainText(tp) {
			// Empty padding fragment carrying no signature/thought — nothing to keep.
			return
		}
		if plainText(tp) && len(st.parts) > 0 {
			if last, ok := st.parts[len(st.parts)-1].(*geminicontent.TextPart); ok && plainText(last) {
				last.Text += tp.Text
				return
			}
		}
	}
	st.parts = append(st.parts, p)
}

// plainText reports whether tp is an ordinary text fragment carrying no thought
// flag, signature, or unknown fields — i.e. safe to concatenate with an
// adjacent plain text part without losing data.
func plainText(tp *geminicontent.TextPart) bool {
	return tp.Thought == nil && tp.ThoughtSignature == nil && len(tp.Extra) == 0
}

func (s *geminiState) absorb(chunk *geminicontent.GenerateContentResponse) {
	if chunk.PromptFeedback != nil {
		s.base.PromptFeedback = chunk.PromptFeedback
	}
	if chunk.UsageMetadata != nil {
		s.base.UsageMetadata = chunk.UsageMetadata
	}
	if chunk.ModelVersion != "" {
		s.base.ModelVersion = chunk.ModelVersion
	}
	if chunk.ResponseID != "" {
		s.base.ResponseID = chunk.ResponseID
	}
	if ms := chunk.ModelStatus; ms != nil &&
		(ms.ModelStage != "" || ms.RetirementTime != "" || ms.Message != "" || len(ms.Extra) > 0) {
		s.base.ModelStatus = ms
	}
	// Google can deliver the top-level error envelope mid-stream (e.g. a 429
	// after some candidates already streamed); a non-streaming decode keeps
	// it, so the rollup must too.
	if !rawEmpty(chunk.Error) {
		s.base.Error = chunk.Error
	}
	// Unknown top-level fields ride in DynamicProperties.Extra — merge them
	// onto the base (last-wins per key, mirroring the per-candidate merge
	// below) so the invariant-#1 safety net holds on the rollup path.
	for k, v := range chunk.Extra {
		if s.base.Extra == nil {
			s.base.Extra = map[string]json.RawMessage{}
		}
		s.base.Extra[k] = v
	}

	for _, cand := range chunk.Candidates {
		idx := 0
		if cand.Index != nil {
			idx = *cand.Index
		}
		st, ok := s.candidates[idx]
		if !ok {
			st = &geminiCandidateState{index: idx}
			s.candidates[idx] = st
			s.candOrder = append(s.candOrder, idx)
		}
		if cand.Content != nil {
			if cand.Content.Role != "" {
				st.role = cand.Content.Role
			}
			for _, p := range cand.Content.Parts {
				st.appendPart(p)
			}
		}
		if cand.FinishReason != nil {
			fr := *cand.FinishReason
			st.finishReason = &fr
		}
		if cand.TokenCount != nil {
			tc := *cand.TokenCount
			st.tokenCount = &tc
		}
		if cand.FinishMessage != nil {
			fm := *cand.FinishMessage
			st.finishMessage = &fm
		}
		if len(cand.SafetyRatings) > 0 {
			st.safetyRatings = cand.SafetyRatings
		}
		if cm := cand.CitationMetadata; cm != nil &&
			(len(cm.CitationSources) > 0 || len(cm.Extra) > 0) {
			st.citationMetadata = cm
		}
		if gm := cand.GroundingMetadata; gm != nil &&
			(gm.SearchEntryPoint != nil || len(gm.GroundingChunks) > 0 ||
				len(gm.GroundingSupports) > 0 || len(gm.WebSearchQueries) > 0 ||
				!rawEmpty(gm.RetrievalMetadata) || len(gm.Extra) > 0) {
			st.groundingMetadata = gm
		}
		if !rawEmpty(cand.URLContextMetadata) {
			st.urlContextMetadata = cand.URLContextMetadata
		}
		if !rawEmpty(cand.LogprobsResult) {
			st.logprobsResult = cand.LogprobsResult
		}
		for k, v := range cand.Extra {
			if st.extra == nil {
				st.extra = map[string]json.RawMessage{}
			}
			st.extra[k] = v
		}
	}
}

// rawEmpty reports whether raw carries no content — absent, JSON null, or an
// empty object. The last-non-empty-wins carries use it so a wire placeholder
// (`{}` / `null`) arriving after the populated chunk cannot wipe the subtree.
func rawEmpty(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null" || s == "{}"
}

func (s *geminiState) assemble() geminicontent.GenerateContentResponse {
	resp := s.base
	sort.Ints(s.candOrder)
	cands := make([]geminicontent.Candidate, 0, len(s.candOrder))
	for _, idx := range s.candOrder {
		st := s.candidates[idx]
		var content *geminicontent.Content
		if len(st.parts) > 0 || st.role != "" {
			role := st.role
			if role == "" {
				role = "model"
			}
			parts := st.parts
			if parts == nil {
				parts = []geminicontent.Part{}
			}
			content = &geminicontent.Content{Role: role, Parts: parts}
		}
		candIdx := st.index
		cands = append(cands, geminicontent.Candidate{
			Content:            content,
			FinishReason:       st.finishReason,
			FinishMessage:      st.finishMessage,
			Index:              &candIdx,
			SafetyRatings:      st.safetyRatings,
			CitationMetadata:   st.citationMetadata,
			TokenCount:         st.tokenCount,
			GroundingMetadata:  st.groundingMetadata,
			URLContextMetadata: st.urlContextMetadata,
			LogprobsResult:     st.logprobsResult,
			DynamicProperties:  models.DynamicProperties{Extra: st.extra},
		})
	}
	resp.Candidates = cands
	return resp
}
