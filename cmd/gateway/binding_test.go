package main

import (
	"encoding/json"
	"testing"

	contractsconfig "github.com/andyjmorgan/sluice-gateway/contracts/config"
	cc "github.com/andyjmorgan/sluice-gateway/contracts/connector"
)

// makeRec returns a baseline Record the binding tests mutate to
// exercise individual predicates. Provider/endpoint/model/status/tags
// + body sizes are populated so filter + oversize behave realistically.
func makeRec() cc.Record {
	return cc.Record{
		ID:             "r-1",
		CorrelationID:  "corr-1",
		Configuration:  "production",
		Provider:       "openai",
		Endpoint:       "chat_completions",
		Model:          "gpt-4o-mini",
		UpstreamStatus: 200,
		Response:       cc.ResponsePart{Status: 200, BodyBytes: 1024, Body: json.RawMessage(`"resp"`)},
		Request:        cc.RequestPart{BodyBytes: 512, Body: json.RawMessage(`"req"`)},
		Tags:           []string{"surface:openai-chat", "audit:large-prompt"},
		SchemaVersion:  cc.SchemaVersion,
	}
}

// ---------- evaluateBinding (composition) ----------

func TestEvaluateBinding_PassThrough_NoOverrides(t *testing.T) {
	rec := makeRec()
	out, ship, _ := evaluateBinding(rec, contractsconfig.ConnectorBinding{Connector: "x"}, contractsconfig.ConnectorTypeS3)
	if !ship {
		t.Fatal("expected ship with no overrides")
	}
	if out.ID != rec.ID || len(out.Request.Body) == 0 {
		t.Errorf("default path should not mutate the record")
	}
}

func TestEvaluateBinding_SamplingOutSkips(t *testing.T) {
	// Find a correlation_id that lands above sampling=0.0001 under
	// the current FNV-1a 64 hash. Computed at runtime so a change to
	// the hash mixing doesn't silently flip the test from "sampling
	// reliably drops" to "sampling reliably includes".
	b := contractsconfig.ConnectorBinding{Connector: "x", Sampling: 0.0001}
	rec := makeRec()
	for i := 0; i < 10_000; i++ {
		rec.CorrelationID = "outbucket-" + intToString(i)
		if !samplingIncludes(rec, b) {
			break
		}
	}
	if samplingIncludes(rec, b) {
		t.Fatalf("could not find a correlation_id that buckets above 0.0001 in 10k tries; FNV distribution broken?")
	}
	_, ship, _ := evaluateBinding(rec, b, contractsconfig.ConnectorTypeS3)
	if ship {
		t.Error("0.0001 sampling with an out-bucket id should drop the record")
	}
}

func TestEvaluateBinding_FilterMissSkips(t *testing.T) {
	rec := makeRec()
	b := contractsconfig.ConnectorBinding{
		Connector: "x",
		Filter:    &contractsconfig.ConnectorFilter{Providers: []string{"anthropic"}},
	}
	_, ship, _ := evaluateBinding(rec, b, contractsconfig.ConnectorTypeS3)
	if ship {
		t.Error("anthropic-only filter should reject an openai record")
	}
}

func TestEvaluateBinding_OversizeMetadataOnlyStrips(t *testing.T) {
	rec := makeRec()
	b := contractsconfig.ConnectorBinding{Connector: "x", MaxBodyBytes: intPtr(256)}
	out, ship, ov := evaluateBinding(rec, b, contractsconfig.ConnectorTypeS3)
	if !ship {
		t.Fatal("metadata_only default should still ship")
	}
	if len(out.Request.Body) != 0 || !out.Request.BodyOmitted {
		t.Errorf("request body not stripped: %+v", out.Request)
	}
	if len(out.Response.Body) != 0 || !out.Response.BodyOmitted {
		t.Errorf("response body not stripped: %+v", out.Response)
	}
	if !ov.Triggered || ov.Dropped {
		t.Errorf("oversize outcome = %+v, want triggered + not dropped", ov)
	}
}

func TestEvaluateBinding_OversizeDropRecordSkips(t *testing.T) {
	rec := makeRec()
	b := contractsconfig.ConnectorBinding{
		Connector:         "x",
		MaxBodyBytes:      intPtr(256),
		OversizeBehaviour: "drop_record",
	}
	_, ship, ov := evaluateBinding(rec, b, contractsconfig.ConnectorTypeS3)
	if ship {
		t.Error("drop_record on oversize should skip the record")
	}
	if !ov.Triggered || !ov.Dropped {
		t.Errorf("oversize outcome = %+v, want triggered + dropped", ov)
	}
}

// ---------- samplingIncludes ----------

func TestSamplingIncludes_Defaults(t *testing.T) {
	rec := makeRec()
	if !samplingIncludes(rec, contractsconfig.ConnectorBinding{}) {
		t.Error("zero sampling means default 1.0 → always include")
	}
	if !samplingIncludes(rec, contractsconfig.ConnectorBinding{Sampling: 1.0}) {
		t.Error("explicit 1.0 → always include")
	}
}

func TestSamplingIncludes_CorrelationIDIsDeterministic(t *testing.T) {
	rec := makeRec()
	b := contractsconfig.ConnectorBinding{Sampling: 0.5}
	first := samplingIncludes(rec, b)
	for i := 0; i < 100; i++ {
		if samplingIncludes(rec, b) != first {
			t.Fatal("same correlation_id must yield same sampling decision")
		}
	}
}

func TestSamplingIncludes_DifferentCorrelationIDsSplit(t *testing.T) {
	// Across many synthetic correlation ids, half should be in / half
	// out at sampling=0.5 with deterministic hashing — sanity that the
	// hash spreads bucket assignments.
	in, total := 0, 1000
	for i := 0; i < total; i++ {
		rec := makeRec()
		rec.CorrelationID = "corr-" + intToString(i)
		if samplingIncludes(rec, contractsconfig.ConnectorBinding{Sampling: 0.5}) {
			in++
		}
	}
	// 1000 trials with p=0.5 → 95% CI roughly [469, 531]; allow generous
	// slack for the bounded test fixture.
	if in < 400 || in > 600 {
		t.Errorf("sampled in = %d/%d, want roughly 500", in, total)
	}
}

func TestSamplingIncludes_RandomMode(t *testing.T) {
	// Random mode ignores correlation_id; substitute the RNG seam so
	// the test asserts deterministically.
	orig := sampleRandFloat64
	t.Cleanup(func() { sampleRandFloat64 = orig })

	rec := makeRec()
	b := contractsconfig.ConnectorBinding{
		Sampling:    0.5,
		SamplingKey: contractsconfig.SamplingKeyRandom,
	}
	sampleRandFloat64 = func() float64 { return 0.49 }
	if !samplingIncludes(rec, b) {
		t.Error("random=0.49 should be IN at sampling=0.5")
	}
	sampleRandFloat64 = func() float64 { return 0.51 }
	if samplingIncludes(rec, b) {
		t.Error("random=0.51 should be OUT at sampling=0.5")
	}
}

// ---------- matchesFilter ----------

func TestMatchesFilter_NilFilterIncludesAll(t *testing.T) {
	if !matchesFilter(makeRec(), nil) {
		t.Error("nil filter should not exclude")
	}
}

func TestMatchesFilter_Providers(t *testing.T) {
	rec := makeRec()
	if !matchesFilter(rec, &contractsconfig.ConnectorFilter{Providers: []string{"openai"}}) {
		t.Error("openai should match openai")
	}
	if matchesFilter(rec, &contractsconfig.ConnectorFilter{Providers: []string{"anthropic"}}) {
		t.Error("openai should not match anthropic-only")
	}
}

func TestMatchesFilter_Endpoints(t *testing.T) {
	rec := makeRec()
	if !matchesFilter(rec, &contractsconfig.ConnectorFilter{Endpoints: []string{"chat_completions"}}) {
		t.Error("endpoint match")
	}
	if matchesFilter(rec, &contractsconfig.ConnectorFilter{Endpoints: []string{"messages"}}) {
		t.Error("endpoint miss should reject")
	}
}

func TestMatchesFilter_ModelWildcard(t *testing.T) {
	rec := makeRec()
	rec.Model = "claude-haiku-4-5"
	if !matchesFilter(rec, &contractsconfig.ConnectorFilter{Models: []string{"claude-*"}}) {
		t.Error("claude-* should match claude-haiku-4-5")
	}
	if matchesFilter(rec, &contractsconfig.ConnectorFilter{Models: []string{"gpt-*"}}) {
		t.Error("gpt-* should not match claude")
	}
}

func TestMatchesFilter_ModelExact(t *testing.T) {
	rec := makeRec()
	if !matchesFilter(rec, &contractsconfig.ConnectorFilter{Models: []string{"gpt-4o-mini"}}) {
		t.Error("exact match should pass")
	}
	if matchesFilter(rec, &contractsconfig.ConnectorFilter{Models: []string{"gpt-4o"}}) {
		t.Error("exact mismatch should reject")
	}
}

func TestMatchesFilter_StatusRange(t *testing.T) {
	rec := makeRec()
	rec.UpstreamStatus = 503
	if !matchesFilter(rec, &contractsconfig.ConnectorFilter{StatusMin: 500, StatusMax: 599}) {
		t.Error("503 should land in 500-599")
	}
	rec.UpstreamStatus = 200
	if matchesFilter(rec, &contractsconfig.ConnectorFilter{StatusMin: 500, StatusMax: 599}) {
		t.Error("200 should not land in 500-599")
	}
}

func TestMatchesFilter_StatusDefaultsTo200_599(t *testing.T) {
	rec := makeRec()
	rec.UpstreamStatus = 0
	rec.Response.Status = 200
	if !matchesFilter(rec, &contractsconfig.ConnectorFilter{}) {
		t.Error("default range should include 200")
	}
}

func TestMatchesFilter_StatusFallsBackToResponseStatus(t *testing.T) {
	rec := makeRec()
	rec.UpstreamStatus = 0
	rec.Response.Status = 503
	if matchesFilter(rec, &contractsconfig.ConnectorFilter{StatusMin: 200, StatusMax: 299}) {
		t.Error("503 via response.Status should fail 200-299 range")
	}
}

func TestMatchesFilter_TagsAny(t *testing.T) {
	rec := makeRec()
	if !matchesFilter(rec, &contractsconfig.ConnectorFilter{TagsAny: []string{"audit:large-prompt"}}) {
		t.Error("tags_any should match")
	}
	if matchesFilter(rec, &contractsconfig.ConnectorFilter{TagsAny: []string{"audit:other"}}) {
		t.Error("tags_any with no overlap should reject")
	}
}

func TestMatchesFilter_TagsAll(t *testing.T) {
	rec := makeRec()
	if !matchesFilter(rec, &contractsconfig.ConnectorFilter{
		TagsAll: []string{"surface:openai-chat", "audit:large-prompt"},
	}) {
		t.Error("all required tags present should match")
	}
	if matchesFilter(rec, &contractsconfig.ConnectorFilter{
		TagsAll: []string{"surface:openai-chat", "audit:missing"},
	}) {
		t.Error("missing required tag should reject")
	}
}

// ---------- applyOversize ----------

func TestApplyOversize_NoCapShipsAsIs(t *testing.T) {
	rec := makeRec()
	out, ship, _ := applyOversize(rec, contractsconfig.ConnectorBinding{}, contractsconfig.ConnectorTypeS3)
	if !ship {
		t.Fatal("no cap should ship")
	}
	if len(out.Request.Body) == 0 {
		t.Error("body should not be stripped")
	}
}

func TestApplyOversize_WithinCapShipsAsIs(t *testing.T) {
	rec := makeRec()
	rec.Request.BodyBytes = 100
	rec.Response.BodyBytes = 200
	out, ship, _ := applyOversize(rec, contractsconfig.ConnectorBinding{MaxBodyBytes: intPtr(500)}, contractsconfig.ConnectorTypeS3)
	if !ship || out.Request.BodyOmitted || out.Response.BodyOmitted {
		t.Errorf("within-cap should not strip: %+v", out)
	}
}

func TestApplyOversize_UnknownBehaviourShipsAsIs(t *testing.T) {
	// Config validator rejects unknown values, but defence-in-depth:
	// an unrecognised behaviour falls through to ship-as-is so the
	// operator at least sees a record.
	rec := makeRec()
	rec.Request.BodyBytes = 1024
	out, ship, _ := applyOversize(rec, contractsconfig.ConnectorBinding{
		MaxBodyBytes:      intPtr(256),
		OversizeBehaviour: "explode",
	}, contractsconfig.ConnectorTypeS3)
	if !ship {
		t.Fatal("unknown behaviour should fall through to ship")
	}
	if out.Request.BodyOmitted {
		t.Error("unknown behaviour should not strip")
	}
}

// ---------- per-type default + zero override ----------

func TestApplyOversize_WebhookDefaultCapStripsOversize(t *testing.T) {
	// A webhook binding that leaves max_body_bytes unset inherits the
	// 1 MiB protective default — a 2 MiB body trips metadata_only.
	rec := makeRec()
	rec.Request.BodyBytes = 2 << 20
	out, ship, ov := applyOversize(rec, contractsconfig.ConnectorBinding{Connector: "hook"}, contractsconfig.ConnectorTypeWebhook)
	if !ship {
		t.Fatal("metadata_only default should still ship")
	}
	if !out.Request.BodyOmitted {
		t.Errorf("webhook default cap should strip a 2 MiB body: %+v", out.Request)
	}
	if !ov.Triggered || ov.Dropped || ov.CapBytes != contractsconfig.WebhookDefaultMaxBodyBytes {
		t.Errorf("oversize outcome = %+v, want triggered + cap=%d", ov, contractsconfig.WebhookDefaultMaxBodyBytes)
	}
}

func TestApplyOversize_WebhookUnderDefaultCapShips(t *testing.T) {
	rec := makeRec()
	rec.Request.BodyBytes = 512 << 10 // 512 KiB, under the 1 MiB default
	rec.Response.BodyBytes = 0
	out, ship, _ := applyOversize(rec, contractsconfig.ConnectorBinding{Connector: "hook"}, contractsconfig.ConnectorTypeWebhook)
	if !ship || out.Request.BodyOmitted {
		t.Errorf("a body under the webhook default should ship intact: %+v", out.Request)
	}
}

func TestApplyOversize_WebhookExplicitZeroOverridesToUnlimited(t *testing.T) {
	// An explicit max_body_bytes: 0 opts the webhook binding out of the
	// default — the 2 MiB body ships intact.
	rec := makeRec()
	rec.Request.BodyBytes = 2 << 20
	out, ship, ov := applyOversize(rec, contractsconfig.ConnectorBinding{
		Connector:    "hook",
		MaxBodyBytes: intPtr(0),
	}, contractsconfig.ConnectorTypeWebhook)
	if !ship || out.Request.BodyOmitted {
		t.Errorf("explicit zero should be unlimited, body intact: %+v", out.Request)
	}
	if ov.Triggered {
		t.Errorf("explicit-zero override should not trigger oversize: %+v", ov)
	}
}

func TestApplyOversize_S3HasNoDefaultCap(t *testing.T) {
	// Blob stores ingest large objects out of band — an unset cap means
	// no cap, even for a body well past the webhook default.
	rec := makeRec()
	rec.Request.BodyBytes = 8 << 20
	out, ship, _ := applyOversize(rec, contractsconfig.ConnectorBinding{Connector: "audit"}, contractsconfig.ConnectorTypeS3)
	if !ship || out.Request.BodyOmitted {
		t.Errorf("s3 has no default cap, body should ship intact: %+v", out.Request)
	}
}

// ---------- helpers ----------

func intPtr(i int) *int { return &i }

func intToString(i int) string {
	// Tiny stdlib-free int→string to keep the test focused. The hash
	// distribution test doesn't need fmt.
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
