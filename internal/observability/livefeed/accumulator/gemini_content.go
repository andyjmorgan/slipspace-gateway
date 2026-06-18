package accumulator

import (
	"encoding/json"
	"sort"
	"strings"

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
// ResponseID / PromptFeedback overwrites the running base. Unknown
// part kinds round-trip via the typed Part registry.
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
	index        int
	role         string
	textBuf      strings.Builder
	textSeen     bool
	parts        []geminicontent.Part
	finishReason *string
	tokenCount   *int
}

func newGeminiState() *geminiState {
	return &geminiState{candidates: map[int]*geminiCandidateState{}}
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
				switch part := p.(type) {
				case *geminicontent.TextPart:
					if part != nil && part.Text != "" {
						st.textBuf.WriteString(part.Text)
						st.textSeen = true
					}
				default:
					// Non-text parts (function calls, inline data, etc.)
					// pass through verbatim — Gemini does not delta
					// these, every chunk carries the whole part.
					st.parts = append(st.parts, p)
				}
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
	}
}

func (s *geminiState) assemble() geminicontent.GenerateContentResponse {
	resp := s.base
	sort.Ints(s.candOrder)
	cands := make([]geminicontent.Candidate, 0, len(s.candOrder))
	for _, idx := range s.candOrder {
		st := s.candidates[idx]
		var content *geminicontent.Content
		if st.textSeen || len(st.parts) > 0 || st.role != "" {
			parts := make([]geminicontent.Part, 0, len(st.parts)+1)
			if st.textSeen {
				parts = append(parts, &geminicontent.TextPart{Text: st.textBuf.String()})
			}
			parts = append(parts, st.parts...)
			role := st.role
			if role == "" {
				role = "model"
			}
			content = &geminicontent.Content{Role: role, Parts: parts}
		}
		candIdx := st.index
		cands = append(cands, geminicontent.Candidate{
			Content:      content,
			FinishReason: st.finishReason,
			Index:        &candIdx,
			TokenCount:   st.tokenCount,
		})
	}
	resp.Candidates = cands
	return resp
}
