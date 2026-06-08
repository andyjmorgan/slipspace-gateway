// Command rollupaudit audits the streaming SSE rollup assembler
// (internal/observability/livefeed/accumulator) for fidelity loss: it runs the
// real production accumulator.Accumulate over captured streaming responses and
// reports every field the assembler drops relative to the raw event stream.
//
// It is protocol-agnostic — the same key-survival detector works for every
// endpoint the accumulator registers (chat, messages, responses,
// generate_content). For one (provider, endpoint) it reads a directory of
// captured cases (<cases>/<cid>/stream.sse, raw SSE, optionally the older
// JSON-string-wrapped form) and emits candidate "dropped key" findings: keys
// present in the stream's event payloads that do not survive into the assembled
// non-streaming body.
//
// The detector is deliberately generic, so it surfaces some benign
// stream-transport keys (event discriminators, delta wrappers) alongside real
// losses; downstream verification (the rollup-fidelity-audit workflow) classifies
// each candidate against the assembler source and provider docs before acting.
// It is the same engine that found the v1 rollup drops (caller, citations,
// usage.iterations, output_tokens_details).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/andyjmorgan/sluice-gateway/internal/observability/livefeed/accumulator"
)

// scaffold lists object keys that are pure SSE/stream transport — event and
// delta discriminators and the wrapper objects that carry per-chunk payloads.
// They legitimately have no place in a non-streaming response body, so a
// generic "did this key survive" detector must ignore them to avoid drowning
// real losses in transport noise.
var scaffold = map[string]bool{
	"type": true, "index": true, "sequence_number": true,
	"delta": true, "content_block": true, "message": true,
	"partial_json": true, "snapshot": true, "output_index": true,
	"content_index": true, "item_id": true,
	// Wrapper / ephemeral keys that carry their real payload under leaf keys
	// (which the detector still checks): "citation" is the citations_delta
	// wrapper whose url/cited_text/title leaves land in TextBlock.Citations;
	// "estimated_tokens" is a streaming-only per-delta estimate with no place
	// in the non-streaming body.
	"citation": true, "estimated_tokens": true,
}

// finding is one candidate fidelity loss for a single captured case.
type finding struct {
	CID      string `json:"cid"`
	Category string `json:"category"` // DROPPED_KEY | INVALID_OUTPUT | PARTIAL | UNRECOGNISED
	Key      string `json:"key,omitempty"`
	Sample   string `json:"sample,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

func main() {
	provider := flag.String("provider", "", "provider label passed to Accumulate (e.g. anthropic)")
	endpoint := flag.String("endpoint", "", "accumulator endpoint: chat|messages|responses|generate_content")
	cases := flag.String("cases", "", "directory of <cid>/stream.sse captured streams")
	shard := flag.String("shard", "", "optional file of correlation ids to restrict to")
	mode := flag.String("mode", "summary", "summary | findings (NDJSON)")
	flag.Parse()
	if *endpoint == "" || *cases == "" {
		fmt.Fprintln(os.Stderr, "usage: rollupaudit -provider P -endpoint E -cases DIR [-shard FILE] [-mode summary|findings]")
		os.Exit(2)
	}

	cids, err := caseIDs(*cases, *shard)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var all []finding
	processed := 0
	for _, cid := range cids {
		//nolint:gosec // standalone audit CLI reads an operator-specified corpus directory by design
		raw, err := os.ReadFile(filepath.Join(*cases, cid, "stream.sse"))
		if err != nil {
			continue
		}
		processed++
		all = append(all, auditCase(*provider, *endpoint, cid, raw)...)
	}

	if *mode == "findings" {
		enc := json.NewEncoder(os.Stdout)
		for _, f := range all {
			_ = enc.Encode(f)
		}
		return
	}
	printSummary(processed, all)
}

func caseIDs(cases, shard string) ([]string, error) {
	if shard != "" {
		b, err := os.ReadFile(shard) //nolint:gosec // operator-specified shard file path by design
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				ids = append(ids, l)
			}
		}
		return ids, nil
	}
	ents, err := os.ReadDir(cases)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range ents {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// auditCase runs the real assembler over one captured stream and diffs the
// assembled body against the raw events, returning candidate dropped keys.
func auditCase(provider, endpoint, cid string, stored []byte) []finding {
	sse := decodeStored(stored)
	res := accumulator.Accumulate(provider, endpoint, sse)
	if !res.Recognised {
		return []finding{{CID: cid, Category: "UNRECOGNISED", Detail: "no accumulator for endpoint " + endpoint}}
	}
	var out []finding
	if res.Partial {
		out = append(out, finding{CID: cid, Category: "PARTIAL", Detail: "assembler reported Partial=true"})
	}
	var outVal any
	if err := json.Unmarshal(res.Assembled, &outVal); err != nil {
		out = append(out, finding{CID: cid, Category: "INVALID_OUTPUT", Detail: "assembled body is not valid JSON: " + err.Error()})
		return out
	}

	outKeys := map[string]bool{}
	collectKeys(outVal, outKeys)

	// Walk stream events; for every non-scaffold key that never appears in the
	// assembled body, record it once with a sample value.
	seen := map[string]bool{}
	for _, ev := range parseEvents(sse) {
		if t, _ := ev["type"].(string); t == "ping" || t == "" {
			continue
		}
		streamKeys := map[string]string{} // key -> sample JSON value
		collectKeyValues(ev, streamKeys)
		for k, sample := range streamKeys {
			if scaffold[k] || outKeys[k] || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, finding{CID: cid, Category: "DROPPED_KEY", Key: k, Sample: trunc(sample, 160),
				Detail: "key present in stream events, absent from assembled body"})
		}
	}
	return out
}

func printSummary(processed int, all []finding) {
	cat := map[string]int{}
	keySig := map[string]int{}
	withFindings := map[string]bool{}
	for _, f := range all {
		cat[f.Category]++
		if f.Category == "DROPPED_KEY" {
			keySig[f.Key]++
		}
		withFindings[f.CID] = true
	}
	fmt.Printf("cases_processed=%d cases_with_findings=%d total_findings=%d\n", processed, len(withFindings), len(all))
	fmt.Println("--- by category ---")
	for _, k := range sortedKeys(cat) {
		fmt.Printf("%-16s %d\n", k, cat[k])
	}
	fmt.Println("--- dropped keys (candidate losses; verify before acting) ---")
	type kv struct {
		k string
		n int
	}
	rows := make([]kv, 0, len(keySig))
	for k, n := range keySig {
		rows = append(rows, kv{k, n})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n })
	for _, r := range rows {
		fmt.Printf("%6d  %s\n", r.n, r.k)
	}
}

// --- SSE + JSON helpers ---

// decodeStored returns the raw SSE bytes from a captured payload, transparently
// unwrapping the older JSON-string-encoded capture form.
func decodeStored(b []byte) []byte {
	t := strings.TrimSpace(string(b))
	if strings.HasPrefix(t, "\"") {
		var s string
		if json.Unmarshal([]byte(t), &s) == nil {
			return []byte(s)
		}
	}
	return b
}

// parseEvents splits an SSE byte stream into the decoded JSON object of each
// event's data: payload. Non-JSON data lines (e.g. "[DONE]") are skipped.
func parseEvents(sse []byte) []map[string]any {
	var events []map[string]any
	var data []string
	flush := func() {
		if len(data) == 0 {
			return
		}
		joined := strings.Join(data, "\n")
		data = nil
		var m map[string]any
		if json.Unmarshal([]byte(joined), &m) == nil && m != nil {
			events = append(events, m)
		}
	}
	for _, line := range strings.Split(string(sse), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
	return events
}

// collectKeys records every object key appearing anywhere in v.
func collectKeys(v any, into map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			into[k] = true
			collectKeys(child, into)
		}
	case []any:
		for _, child := range t {
			collectKeys(child, into)
		}
	}
}

// collectKeyValues records every object key appearing anywhere in v together
// with a sample JSON encoding of its value (first occurrence wins).
func collectKeyValues(v any, into map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if _, ok := into[k]; !ok {
				into[k] = jsonOf(child)
			}
			collectKeyValues(child, into)
		}
	case []any:
		for _, child := range t {
			collectKeyValues(child, into)
		}
	}
}

func sortedKeys[T any](m map[string]T) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
