#!/usr/bin/env bash
# Generate config-dev.real/ from .env + cluster-side qwen details.
#
# Reads provider credentials from .env (gitignored), then writes a
# providers.yaml that points at real upstream APIs and a policy.yaml
# with the real credentials inlined. Output is gitignored.
#
# Required env (from .env):
#   OPENAI_API_KEY, ANTHROPIC_API_KEY, GEMINI_API_KEY
# Optional:
#   QWEN_OLLAMA_URL  default http://host.docker.internal:11434
#                    (assumes you kubectl port-forwarded the cluster's
#                    ollama-qwen36 service to laptop port 11434)

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

if [[ ! -f .env ]]; then
  echo "error: .env not found in $repo_root" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1091
source .env
set +a

require() {
  local name=$1
  if [[ -z "${!name:-}" ]]; then
    echo "error: $name is not set in .env" >&2
    exit 1
  fi
}
require OPENAI_API_KEY
require ANTHROPIC_API_KEY
require GEMINI_API_KEY

QWEN_OLLAMA_URL="${QWEN_OLLAMA_URL:-http://host.docker.internal:11434}"

out="config-dev.real"
mkdir -p "$out"

# providers.yaml — same shapes as config-dev/providers.yaml but pointed
# at real upstreams. The auth conventions (header + format overrides)
# are unchanged; the gateway's destination builder applies them per
# (provider, endpoint).
cat >"$out/providers.yaml" <<'YAML'
providers:
  openai:
    prefix: openai
    prefix_required: false
    base_url: https://api.openai.com
    endpoints:
      chat_completions:
        path: /v1/chat/completions
        method: [POST]
        accepted_paths: [/v1/chat/completions, /chat/completions]
        accepts_streaming: true
        request_kind: chat
      responses:
        path: /v1/responses
        method: [POST]
        accepted_paths: [/v1/responses]
        accepts_streaming: true
        request_kind: responses
      models:
        path: /v1/models
        method: [GET]
        accepted_paths: [/v1/models, /models]
        request_kind: passthrough

  anthropic:
    prefix: anthropic
    prefix_required: true
    base_url: https://api.anthropic.com
    required_headers:
      anthropic-version: "2023-06-01"
    endpoints:
      messages:
        path: /v1/messages
        method: [POST]
        accepted_paths: [/v1/messages, /messages]
        accepts_streaming: true
        request_kind: messages
        prefix_optional: true
      models:
        path: /v1/models
        method: [GET]
        accepted_paths: [/v1/models]
        request_kind: passthrough
      chat_completions:
        path: /v1/chat/completions
        method: [POST]
        accepted_paths: [/v1/chat/completions]
        accepts_streaming: true
        request_kind: chat
        auth_header: Authorization
        auth_format: "Bearer {key}"

  gemini:
    prefix: gemini
    prefix_required: true
    base_url: https://generativelanguage.googleapis.com
    endpoints:
      generate_content:
        path: /v1beta/models/{model}:generateContent
        method: [POST]
        accepted_paths: ["/v1beta/models/{model}:generateContent"]
        accepts_streaming: true
        request_kind: generate_content
        prefix_optional: true
      models:
        path: /v1beta/models
        method: [GET]
        accepted_paths: [/v1beta/models]
        request_kind: passthrough
      chat_completions:
        path: /v1beta/openai/chat/completions
        method: [POST]
        accepted_paths: [/v1beta/openai/chat/completions]
        accepts_streaming: true
        request_kind: chat
        auth_header: Authorization
        auth_format: "Bearer {key}"

  qwen-ollama:
    prefix: qwen-ollama
    prefix_required: true
YAML
# Append qwen-ollama base_url (interpolated) + endpoints.
cat >>"$out/providers.yaml" <<YAML
    base_url: ${QWEN_OLLAMA_URL}
    endpoints:
      chat_completions:
        path: /v1/chat/completions
        method: [POST]
        accepted_paths: [/v1/chat/completions]
        accepts_streaming: true
        request_kind: chat
      models:
        path: /v1/models
        method: [GET]
        accepted_paths: [/v1/models]
        request_kind: passthrough
YAML

# policy.yaml — single configuration "real" with real upstream
# credentials interpolated from env. Routing rules mirror config-dev:
# claude-* → anthropic, gemini-* → gemini, qwen-* → qwen-ollama.
cat >"$out/policy.yaml" <<YAML
configurations:
  real:
    upstream_credentials:
      openai: ${OPENAI_API_KEY}
      anthropic: ${ANTHROPIC_API_KEY}
      gemini: ${GEMINI_API_KEY}
      qwen-ollama: ollama-no-auth-needed

    rule_names:
      - route-claude-models-to-anthropic
      - route-gemini-models-to-gemini
      - route-qwen-models-to-ollama

    tags:
      tier: real

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev (real upstreams)"
    configuration: real
    enabled: true

rules:
  - name: route-claude-models-to-anthropic
    priority: 50
    condition:
      type: modelName
      operator: StartsWith
      expectedModelName: claude-
    actions:
      - type: changeProvider
        newProvider: anthropic
    behavior: continue

  - name: route-gemini-models-to-gemini
    priority: 50
    condition:
      type: modelName
      operator: StartsWith
      expectedModelName: gemini-
    actions:
      - type: changeProvider
        newProvider: gemini
    behavior: continue

  - name: route-qwen-models-to-ollama
    priority: 50
    condition:
      type: modelName
      operator: StartsWith
      expectedModelName: qwen
    actions:
      - type: changeProvider
        newProvider: qwen-ollama
    behavior: continue
YAML

# admin.yaml — enabled, bound to all interfaces so the host port
# mapping reaches the listener. The yaml password is a placeholder so
# `cli config validate` passes without SLIPSPACE_ADMIN_PASSWORD in env;
# at runtime, SLIPSPACE_ADMIN_PASSWORD (set by docker-compose.real.yaml
# from .env) wins over this field.
cat >"$out/admin.yaml" <<'YAML'
admin:
  enabled: true
  bind_addr: "0.0.0.0:8081"
  password: "placeholder-overridden-by-SLIPSPACE_ADMIN_PASSWORD"
YAML

echo "wrote: $out/{providers,policy,admin}.yaml"
