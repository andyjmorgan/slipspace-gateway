// Package tokens extracts upstream-reported token usage from a captured
// response body and aggregates it into a per-request snapshot the reporter
// publishes on the gateway.request event and feeds to the
// gateway.tokens.* OTel counters.
//
// The package is named "middleware" by convention — it sits alongside
// bodycapture, rules, and resilience — but it is not in the HTTP chain.
// Token data only exists after the response, so Extract is called from
// the reporter at OnComplete against the same captured response bytes
// the body store already holds.
//
// Dispatch is keyed by the resolved endpoint name (`chat_completions`,
// `messages`, `responses`, `generate_content`) — the same key the
// livefeed/accumulator package uses. Provider is accepted but unused for
// now: OpenAI-compat surfaces on Anthropic and Gemini emit the OpenAI
// chunk shape under the same endpoint key, so endpoint alone disambiguates
// the v1.0 surface.
//
// Three aggregation strategies are exposed (LastWins, Addition,
// Incremental) so the package can grow to vendors that emit per-chunk
// deltas (Perplexity-style) without changing the API. Every v1.0
// extractor uses LastWins — empirically confirmed against real
// OpenAI/Anthropic/Gemini responses: OpenAI emits usage once on the
// terminal chunk; Anthropic's message_delta carries the cumulative
// running total ("The token counts shown in the usage field of the
// message_delta event are cumulative" — Anthropic docs); Gemini emits
// usageMetadata on the response body (and per-chunk running totals when
// streaming, last value wins).
//
// On any error path — malformed JSON, missing usage, truncated stream,
// unknown endpoint — Extract returns Snapshot{Recognised: false}. The
// caller skips bumping counters and leaves TokensIn/Out/Cached/
// CacheCreation at zero on the event. No partial / best-guess
// accounting: a missing total is preferable to a wrong total.
package tokens
