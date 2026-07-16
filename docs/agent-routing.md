# Agent-aware model routing

The gateway can identify the calling agent from request headers, ask an
advisor (the Arbiter) to classify an eligible conversation at its first
request, and pin the verdict's model for the conversation's lifetime. The
canonical use: a Claude Code subagent whose task is trivial gets its whole
conversation served by a cheaper model, while the root session is untouched.

Design note: "Agent-Aware Model Routing — classify, pin, re-route" (design
vault). This page is the operator reference.

## How it works

1. Tier-1 identification is header-only and deterministic — no body parse, no
   model call. Claude Code is `User-Agent: claude-cli/...`; a subagent
   conversation is marked by the presence of `X-Claude-Code-Agent-Id`
   (verified against live capture: 1,817/1,817 agreement with the
   system-prompt `cc_is_subagent` flag). The header's value is the pin key.
2. Eligibility gate (v1 scope): messages protocol, identified subagent, and
   the typed body declares at least one tool. Tool-less one-shots (Claude
   Code's sidecar utility prompts) never open a register entry. Codex
   `request_kind: prewarm/compaction/memory` housekeeping is likewise
   ineligible by construction (Codex is not in the v1 trigger set).
3. On a conversation's first eligible request the gateway routes it **as
   configured** and fires one asynchronous, HMAC-signed advisory call
   (`POST /api/v1/advise/route`, see [arbiter.md](arbiter.md)). The request
   path never waits: the call runs on a bounded background goroutine
   (8 in flight max; saturation abandons the attempt for a later retry).
4. The verdict is validated against the configuration's `allow_models` list —
   the advisor recommends, config decides — then stored in an in-process pin
   register (per-pod, TTL-bounded, structurally the circuit-breaker store's
   sibling).
5. Requests 2..N of the conversation apply the pin as the selected target's
   model alias, riding the existing per-attempt `changeModelName` +
   body-rewrite machinery. The pin has a binding's precedence: rules still
   run (tags, transforms, connectors) but cannot re-steer provider/model
   (invariant #7). A pinned request carries the `agent-route:<model>` tag
   into telemetry.
6. A verdict that arrives after the conversation has already seen more than
   `apply_window_requests` requests is discarded — the prompt cache is warm
   on the default model and a late switch costs more than it saves
   (pin-at-birth, never mid-life).

Failure posture: everything fails open. Advisor unreachable, timeout, non-200,
malformed verdict, disallowed model — the conversation simply continues on its
configured route. Nothing in this feature can block or fail a request.

## Gateway configuration

Two blocks. The top-level `advisors` catalogue (startup-bound, like `admin`):

```yaml
advisors:
  arbiter:
    endpoint: https://arbiter.example.com/api/v1/advise/route
    hmac_secret_file: /etc/slipspace/secrets/advisor-hmac
    gateway_id: office-gateway          # must match an Arbiter gateways[] entry
    timeout_ms: 5000                    # advisory call bound (async; optional)
```

And per-configuration `agent_routing` (read live from each request's
snapshot):

```yaml
configurations:
  production:
    agent_routing:
      advisor: arbiter
      allow_models: [claude-haiku-4-5]  # the enforcement point — required
      pin_ttl_seconds: 7200             # optional, default 2h
      apply_window_requests: 3          # optional, default 3
```

The HMAC secret is shared with the Arbiter's `gateways[]` registry entry for
`gateway_id` — the same trust pattern (and typically the same secret) as the
Record webhook.

## Arbiter configuration

The advisor endpoint is off by default. When enabled, the Arbiter answers by
prompting a judge model — conventionally routed back through a gateway on a
dedicated configuration (own api-key, `agent_routing` absent), so the judge's
spend is attributed and its traffic can never be classified or re-routed:

```yaml
advise:
  enabled: true
  upstream:
    base_url: https://gateway.example.com   # judge POSTs {base_url}/v1/messages
    api_key_file: /etc/arbiter/secrets/advisor-api-key
    model: claude-haiku-4-5
    timeout_seconds: 30
  candidates: [claude-haiku-4-5]            # models the judge may recommend
  prompt_file: /etc/arbiter/advise/rubric.md  # optional rubric override
  cache_ttl_seconds: 86400
```

The candidate list and the JSON response schema are always appended to the
prompt programmatically, so a rubric edit cannot unshape the output contract.
Verdicts are cached by template hash (agent family + system prefix + task text
+ tools), bounded and TTL'd.

Trust: the advise route verifies `X-Slipspace-Gateway-Id` +
`X-Slipspace-Signature` (hex HMAC-SHA256 of the raw body) against the same
`gateways[]` registry as the Record webhook. Judge failure returns 503 and the
gateway retries on a later request of the same conversation; a malformed judge
answer is a decided "continue" (no retry storm).

## Wire contract

`contracts/advise` is the shared schema. Request (gateway → Arbiter): identity
fields (family, entrypoint, is_subagent), conversation/session ids,
configuration/protocol/provider/model, truncated `system_prefix` and
`first_user_message` (4 KiB each), `tool_names`. Verdict (Arbiter → gateway):
`{switch, model, reason, confidence}`.

## Scope and non-goals (v1)

- Same-provider model swaps only (the pin sets a model alias; it does not
  switch provider). Cross-provider/cross-dialect pins are future work and
  must compose with translation and the passthrough credential rule.
- Root (orchestrator) sessions are never re-routed — birth classification is
  only sound for subagents, whose task arrives whole in the first request.
- The register is per-pod and non-persistent; a restart costs one
  re-classification per live conversation.
