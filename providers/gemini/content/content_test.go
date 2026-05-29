package content

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestGenerateContentRequest_MixedPartsRoundTrip(t *testing.T) {
	in := []byte(`{` +
		`"cachedContent":"cachedContents/abc",` +
		`"contents":[` +
		`{"parts":[{"text":"describe this"},{"inlineData":{"data":"AAA","mimeType":"image/png"}}],"role":"user"},` +
		`{"parts":[{"text":"sure thing"}],"role":"model"}` +
		`],` +
		`"generationConfig":{"maxOutputTokens":1024,"responseMimeType":"text/plain","temperature":0.2,"thinkingConfig":{"includeThoughts":true,"thinkingBudget":2048}},` +
		`"safetySettings":[{"category":"HARM_CATEGORY_DANGEROUS","threshold":"BLOCK_NONE"}],` +
		`"systemInstruction":{"parts":[{"text":"be terse"}],"role":"system"},` +
		`"toolConfig":{"functionCallingConfig":{"allowedFunctionNames":["x"],"mode":"AUTO"}},` +
		`"tools":[{"functionDeclarations":[{"description":"d","name":"x","parameters":{"type":"object"}}]},{"googleSearch":{}},{"codeExecution":{}}]` +
		`}`)
	var req GenerateContentRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Contents) != 2 {
		t.Fatalf("contents len = %d", len(req.Contents))
	}
	if req.Contents[0].Role != "user" {
		t.Fatalf("role[0] = %q", req.Contents[0].Role)
	}
	if len(req.Contents[0].Parts) != 2 {
		t.Fatalf("parts[0] len = %d", len(req.Contents[0].Parts))
	}
	if _, ok := req.Contents[0].Parts[0].(*TextPart); !ok {
		t.Fatalf("parts[0][0] = %T", req.Contents[0].Parts[0])
	}
	if _, ok := req.Contents[0].Parts[1].(*InlineDataPart); !ok {
		t.Fatalf("parts[0][1] = %T", req.Contents[0].Parts[1])
	}
	if req.GenerationConfig == nil || req.GenerationConfig.Temperature == nil || *req.GenerationConfig.Temperature != 0.2 {
		t.Fatalf("temperature = %+v", req.GenerationConfig)
	}
	if req.GenerationConfig.ThinkingConfig == nil || req.GenerationConfig.ThinkingConfig.IncludeThoughts == nil {
		t.Fatalf("thinking config missing")
	}
	if req.CachedContent == nil || *req.CachedContent != "cachedContents/abc" {
		t.Fatalf("cached content = %v", req.CachedContent)
	}
	roundTripJSON(t, in, req)
}

func TestGenerateContentRequest_UnknownFieldRoundTrips(t *testing.T) {
	in := []byte(`{"contents":[{"parts":[{"text":"hi"}],"role":"user"}],"futureKnob":{"flag":true}}`)
	var req GenerateContentRequest
	if err := json.Unmarshal(in, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(req.Extra["futureKnob"]) != `{"flag":true}` {
		t.Fatalf("extras: %v", req.Extra)
	}
	roundTripJSON(t, in, req)
}

func TestContent_NullPartsRoundTrips(t *testing.T) {
	in := []byte(`{"parts":null,"role":"user"}`)
	var c Content
	if err := json.Unmarshal(in, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Parts != nil {
		t.Fatalf("parts = %v, want nil", c.Parts)
	}
}

func TestContent_NoPartsRoundTrips(t *testing.T) {
	in := []byte(`{"role":"user"}`)
	var c Content
	if err := json.Unmarshal(in, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Parts != nil {
		t.Fatalf("parts = %v", c.Parts)
	}
	roundTripJSON(t, in, c)
}

func TestContent_InvalidPartsArray(t *testing.T) {
	in := []byte(`{"parts":[{}],"role":"user"}`)
	var c Content
	err := json.Unmarshal(in, &c)
	if err == nil {
		t.Fatalf("expected error on empty part")
	}
}

func TestContent_NonObjectPayload(t *testing.T) {
	var c Content
	err := json.Unmarshal([]byte(`["not","an","object"]`), &c)
	if err == nil {
		t.Fatalf("expected error on non-object content payload")
	}
}

func TestGenerateContentResponse_FullRoundTrip(t *testing.T) {
	in := []byte(`{` +
		`"candidates":[{` +
		`"citationMetadata":{"citationSources":[{"endIndex":12,"license":"MIT","startIndex":0,"uri":"https://example.com"}]},` +
		`"content":{"parts":[{"text":"the answer is 42"}],"role":"model"},` +
		`"finishReason":"STOP",` +
		`"index":0,` +
		`"safetyRatings":[{"blocked":false,"category":"HARM_CATEGORY_DANGEROUS","probability":"NEGLIGIBLE","probabilityScore":0.01,"severity":"LOW","severityScore":0.02}],` +
		`"tokenCount":5` +
		`}],` +
		`"modelVersion":"gemini-2.5-pro",` +
		`"promptFeedback":{"blockReason":"SAFETY","safetyRatings":[{"category":"HARM_CATEGORY_DANGEROUS","probability":"NEGLIGIBLE"}]},` +
		`"responseId":"r-1",` +
		`"usageMetadata":{"cachedContentTokenCount":0,"candidatesTokenCount":10,"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":10}],"promptTokenCount":3,"promptTokensDetails":[{"modality":"TEXT","tokenCount":3}],"thoughtsTokenCount":2,"totalTokenCount":13}` +
		`}`)
	var resp GenerateContentResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("candidates len = %d", len(resp.Candidates))
	}
	c := resp.Candidates[0]
	if c.FinishReason == nil || *c.FinishReason != "STOP" {
		t.Fatalf("finish_reason = %v", c.FinishReason)
	}
	if c.Content == nil || len(c.Content.Parts) != 1 {
		t.Fatalf("candidate content missing")
	}
	if _, ok := c.Content.Parts[0].(*TextPart); !ok {
		t.Fatalf("candidate part = %T", c.Content.Parts[0])
	}
	if c.CitationMetadata == nil || len(c.CitationMetadata.CitationSources) != 1 {
		t.Fatalf("citation metadata missing")
	}
	if c.SafetyRatings[0].ProbabilityScore == nil || *c.SafetyRatings[0].ProbabilityScore != 0.01 {
		t.Fatalf("probability score = %v", c.SafetyRatings[0].ProbabilityScore)
	}
	if resp.UsageMetadata == nil || resp.UsageMetadata.TotalTokenCount == nil || *resp.UsageMetadata.TotalTokenCount != 13 {
		t.Fatalf("usage metadata = %+v", resp.UsageMetadata)
	}
	roundTripJSON(t, in, resp)
}

// TestGenerateContentResponse_ToolThinkingFields mirrors a real gemini-2.5
// tool-calling response: a function-call part carrying thoughtSignature, a
// finishMessage alongside finishReason, and usageMetadata.serviceTier. All
// must decode typed and round-trip with nothing left in Extra.
func TestGenerateContentResponse_ToolThinkingFields(t *testing.T) {
	in := []byte(`{` +
		`"candidates":[{` +
		`"content":{"parts":[{"functionCall":{"args":{"city":"Dublin"},"name":"get_weather"},"thoughtSignature":"CsABAQw51se"}],"role":"model"},` +
		`"finishMessage":"Model generated function call(s).",` +
		`"finishReason":"STOP",` +
		`"index":0` +
		`}],` +
		`"modelVersion":"gemini-2.5-flash",` +
		`"responseId":"S-QZasLLFYjX",` +
		`"usageMetadata":{"candidatesTokenCount":15,"promptTokenCount":43,"serviceTier":"standard","thoughtsTokenCount":43,"totalTokenCount":101}` +
		`}`)
	var resp GenerateContentResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	c := resp.Candidates[0]
	if c.FinishMessage == nil || *c.FinishMessage != "Model generated function call(s)." {
		t.Fatalf("finishMessage = %v", c.FinishMessage)
	}
	fc, ok := c.Content.Parts[0].(*FunctionCallPart)
	if !ok {
		t.Fatalf("part = %T", c.Content.Parts[0])
	}
	if fc.ThoughtSignature == nil || *fc.ThoughtSignature != "CsABAQw51se" {
		t.Fatalf("thoughtSignature = %v", fc.ThoughtSignature)
	}
	if resp.UsageMetadata.ServiceTier == nil || *resp.UsageMetadata.ServiceTier != "standard" {
		t.Fatalf("serviceTier = %v", resp.UsageMetadata.ServiceTier)
	}
	if len(resp.Extra) != 0 || len(c.Extra) != 0 || len(fc.Extra) != 0 {
		t.Fatalf("unmapped fields leaked: resp=%v cand=%v part=%v", resp.Extra, c.Extra, fc.Extra)
	}
	roundTripJSON(t, in, resp)
}

// TestGenerateContentResponse_ErrorEnvelope exercises Google's top-level
// error envelope returned when generateContent fails without an
// HTTP-transport error.
func TestGenerateContentResponse_ErrorEnvelope(t *testing.T) {
	in := []byte(`{"error":{"code":404,"message":"models/x is not found","status":"NOT_FOUND"}}`)
	var resp GenerateContentResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Error) == 0 {
		t.Fatalf("error envelope not captured")
	}
	if len(resp.Extra) != 0 {
		t.Fatalf("unmapped fields: %v", resp.Extra)
	}
	roundTripJSON(t, in, resp)
}

// TestFunctionDeclaration_ParametersJsonSchema covers the full-JSON-Schema
// function-param field recent SDKs (incl. gemini-cli) send in place of the
// OpenAPI-subset parameters field.
func TestFunctionDeclaration_ParametersJsonSchema(t *testing.T) {
	in := []byte(`{` +
		`"name":"get_weather",` +
		`"parametersJsonSchema":{"properties":{"city":{"type":"string"}},"required":["city"],"type":"object"},` +
		`"responseJsonSchema":{"type":"object"}` +
		`}`)
	var fd FunctionDeclaration
	if err := json.Unmarshal(in, &fd); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(fd.ParametersJsonSchema) == 0 || len(fd.ResponseJsonSchema) == 0 {
		t.Fatalf("json-schema fields not captured: params=%s response=%s", fd.ParametersJsonSchema, fd.ResponseJsonSchema)
	}
	if len(fd.Extra) != 0 {
		t.Fatalf("unmapped fields: %v", fd.Extra)
	}
	roundTripJSON(t, in, fd)
}

// TestGroundingMetadata_TypedRoundTrip mirrors a live google_search grounded
// response: searchEntryPoint, groundingChunks.web, groundingSupports.segment,
// webSearchQueries, plus the toolUsePromptTokensDetails usage breakdown. All
// decode typed and round-trip with nothing left in Extra.
func TestGroundingMetadata_TypedRoundTrip(t *testing.T) {
	in := []byte(`{` +
		`"candidates":[{` +
		`"content":{"parts":[{"text":"Max Verstappen won."}],"role":"model"},` +
		`"finishReason":"STOP",` +
		`"groundingMetadata":{` +
		`"groundingChunks":[{"web":{"title":"f1.com","uri":"https://vertex.example/r/1"}}],` +
		`"groundingSupports":[{"groundingChunkIndices":[0],"segment":{"endIndex":18,"text":"Max Verstappen won"}}],` +
		`"searchEntryPoint":{"renderedContent":"<div>search</div>"},` +
		`"webSearchQueries":["current f1 world champion"]` +
		`}` +
		`}],` +
		`"usageMetadata":{"candidatesTokenCount":5,"promptTokenCount":9,"toolUsePromptTokenCount":12,"toolUsePromptTokensDetails":[{"modality":"TEXT","tokenCount":12}],"totalTokenCount":26}` +
		`}`)
	var resp GenerateContentResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	gm := resp.Candidates[0].GroundingMetadata
	if gm == nil {
		t.Fatalf("grounding metadata nil")
	}
	if len(gm.GroundingChunks) != 1 || gm.GroundingChunks[0].Web == nil || gm.GroundingChunks[0].Web.URI != "https://vertex.example/r/1" {
		t.Fatalf("grounding chunks = %+v", gm.GroundingChunks)
	}
	if len(gm.GroundingSupports) != 1 || gm.GroundingSupports[0].Segment == nil ||
		gm.GroundingSupports[0].Segment.EndIndex == nil || *gm.GroundingSupports[0].Segment.EndIndex != 18 {
		t.Fatalf("grounding supports = %+v", gm.GroundingSupports)
	}
	if gm.SearchEntryPoint == nil || gm.SearchEntryPoint.RenderedContent == "" {
		t.Fatalf("search entry point = %+v", gm.SearchEntryPoint)
	}
	if len(resp.UsageMetadata.ToolUsePromptTokensDetails) != 1 {
		t.Fatalf("toolUsePromptTokensDetails = %+v", resp.UsageMetadata.ToolUsePromptTokensDetails)
	}
	if len(resp.Candidates[0].Extra) != 0 || len(gm.Extra) != 0 {
		t.Fatalf("unmapped leaked: cand=%v gm=%v", resp.Candidates[0].Extra, gm.Extra)
	}
	roundTripJSON(t, in, resp)
}

func TestGenerateContentResponse_GroundingMetadataRoundTrips(t *testing.T) {
	in := []byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"groundingMetadata":{"webSearchQueries":["q1"]},"logprobsResult":{"chosenCandidates":[]},"urlContextMetadata":{"urls":[]}}]}`)
	var resp GenerateContentResponse
	if err := json.Unmarshal(in, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	c := resp.Candidates[0]
	if c.GroundingMetadata == nil || len(c.GroundingMetadata.WebSearchQueries) != 1 ||
		c.GroundingMetadata.WebSearchQueries[0] != "q1" {
		t.Fatalf("grounding = %+v", c.GroundingMetadata)
	}
	if string(c.URLContextMetadata) != `{"urls":[]}` {
		t.Fatalf("url ctx = %s", c.URLContextMetadata)
	}
	if string(c.LogprobsResult) != `{"chosenCandidates":[]}` {
		t.Fatalf("logprobs = %s", c.LogprobsResult)
	}
	roundTripJSON(t, in, resp)
}

func TestGenerateContentResponse_StreamingChunkRoundTrip(t *testing.T) {
	in := []byte(`{"candidates":[{"content":{"parts":[{"text":"par"}],"role":"model"},"index":0}],"modelVersion":"gemini-2.5-pro"}`)
	var chunk GenerateContentResponse
	if err := json.Unmarshal(in, &chunk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	roundTripJSON(t, in, chunk)
}

func TestPromptFeedback_RoundTrip(t *testing.T) {
	in := []byte(`{"blockReason":"SAFETY","safetyRatings":[{"category":"HARM_CATEGORY_HATE","probability":"HIGH"}]}`)
	var f PromptFeedback
	if err := json.Unmarshal(in, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f.BlockReason != "SAFETY" {
		t.Fatalf("block reason = %q", f.BlockReason)
	}
	roundTripJSON(t, in, f)
}

func TestGoogleSearchRetrievalTool_RoundTrip(t *testing.T) {
	in := []byte(`{"dynamicRetrievalConfig":{"dynamicThreshold":0.7}}`)
	var tr GoogleSearchRetrievalTool
	if err := json.Unmarshal(in, &tr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	roundTripJSON(t, in, tr)
}

func TestURLContextTool_RoundTrip(t *testing.T) {
	in := []byte(`{}`)
	var u URLContextTool
	if err := json.Unmarshal(in, &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	roundTripJSON(t, in, u)
}

func TestContent_AllExportedFieldsHaveJSONTag(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(GenerateContentRequest{}),
		reflect.TypeOf(Content{}),
		reflect.TypeOf(GenerationConfig{}),
		reflect.TypeOf(ThinkingConfig{}),
		reflect.TypeOf(Tool{}),
		reflect.TypeOf(GoogleSearchTool{}),
		reflect.TypeOf(GoogleSearchRetrievalTool{}),
		reflect.TypeOf(CodeExecutionTool{}),
		reflect.TypeOf(URLContextTool{}),
		reflect.TypeOf(FunctionDeclaration{}),
		reflect.TypeOf(ToolConfig{}),
		reflect.TypeOf(FunctionCallingConfig{}),
		reflect.TypeOf(SafetySetting{}),
		reflect.TypeOf(GenerateContentResponse{}),
		reflect.TypeOf(Candidate{}),
		reflect.TypeOf(SafetyRating{}),
		reflect.TypeOf(CitationMetadata{}),
		reflect.TypeOf(CitationSource{}),
		reflect.TypeOf(PromptFeedback{}),
		reflect.TypeOf(UsageMetadata{}),
		reflect.TypeOf(ModalityTokenCount{}),
		reflect.TypeOf(GroundingMetadata{}),
		reflect.TypeOf(SearchEntryPoint{}),
		reflect.TypeOf(GroundingChunk{}),
		reflect.TypeOf(GroundingChunkWeb{}),
		reflect.TypeOf(GroundingSupport{}),
		reflect.TypeOf(Segment{}),
	}
	for _, rt := range types {
		t.Run(rt.Name(), func(t *testing.T) {
			for i := 0; i < rt.NumField(); i++ {
				sf := rt.Field(i)
				if sf.Anonymous || !sf.IsExported() {
					continue
				}
				if _, ok := sf.Tag.Lookup("json"); !ok {
					t.Errorf("%s.%s missing json tag", rt.Name(), sf.Name)
				}
			}
		})
	}
}

func roundTripJSON(t *testing.T, in []byte, v any) {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonValueEqual(t, in, out) {
		t.Fatalf("round-trip drift\n in: %s\nout: %s", in, out)
	}
}
