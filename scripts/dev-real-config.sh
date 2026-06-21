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

yaml_quote() {
  local value=${1//\'/\'\'}
  printf "'%s'" "$value"
}

openai_key=$(yaml_quote "$OPENAI_API_KEY")
anthropic_key=$(yaml_quote "$ANTHROPIC_API_KEY")
gemini_key=$(yaml_quote "$GEMINI_API_KEY")
qwen_url=$(yaml_quote "$QWEN_OLLAMA_URL")

# providers.yaml — v2 shape, same protocol/auth conventions as
# config-dev/providers.yaml but pointed at real upstreams.
cat >"$out/providers.yaml" <<'YAML'
providers:
  openai:
    base_url: https://api.openai.com
    protocols:
      chat:
        path: /v1/chat/completions
        auth:
          header: Authorization
          format: "Bearer {key}"
      responses:
        path: /v1/responses
        auth:
          header: Authorization
          format: "Bearer {key}"
    passthrough:
      models:
        auth:
          header: Authorization
          format: "Bearer {key}"
        paths:
          - match: /v1/models
            methods: [GET]

  anthropic:
    base_url: https://api.anthropic.com
    required_headers:
      anthropic-version: "2023-06-01"
    protocols:
      messages:
        path: /v1/messages
        auth:
          header: x-api-key
          format: "{key}"
      chat:
        path: /v1/chat/completions
        auth:
          header: Authorization
          format: "Bearer {key}"
    passthrough:
      messages_batches:
        auth:
          header: x-api-key
          format: "{key}"
        paths:
          - match: /v1/messages/batches
            methods: [POST, GET]
          - match: "/v1/messages/batches/{id}"
            methods: [GET, DELETE]
          - match: "/v1/messages/batches/{id}/results"
            methods: [GET]

  gemini:
    base_url: https://generativelanguage.googleapis.com
    protocols:
      generate_content:
        path: "/v1beta/models/{model}:{op}"
        auth:
          header: x-goog-api-key
          format: "{key}"
      chat:
        path: /v1beta/openai/chat/completions
        auth:
          header: Authorization
          format: "Bearer {key}"
    passthrough:
      models:
        auth:
          header: x-goog-api-key
          format: "{key}"
        paths:
          - match: /v1beta/models
            methods: [GET]

  qwen-ollama:
YAML
# Append qwen-ollama base_url (interpolated) + protocols.
cat >>"$out/providers.yaml" <<YAML
    base_url: ${qwen_url}
    protocols:
      chat:
        path: /v1/chat/completions
YAML

# policy.yaml — single configuration "real" with real upstream credentials
# interpolated from env. Routing is v2 bindings, not v1 changeProvider rules.
cat >"$out/policy.yaml" <<YAML
configurations:
  real:
    credentials:
      openai: ${openai_key}
      anthropic: ${anthropic_key}
      gemini: ${gemini_key}
      qwen-ollama: ""

    bindings:
      - { protocol: chat, models: ["claude-*"], provider: anthropic }
      - { protocol: messages, models: ["claude-*"], provider: anthropic }
      - { protocol: chat, models: ["gemini-*"], provider: gemini }
      - { protocol: generate_content, models: ["gemini-*"], provider: gemini }
      - { protocol: chat, models: ["qwen*"], provider: qwen-ollama }
      - { protocol: chat, models: ["gpt-*", "o1*", "o3*"], provider: openai }
      - { protocol: responses, models: ["gpt-*"], provider: openai }

    passthrough_bindings:
      - { family: messages_batches, provider: anthropic }
      - { family: models, provider: openai }
      - { family: models, provider: gemini }

    tags:
      tier: real

api_keys:
  - secret: sk_dev_local_development_only_not_for_production
    name: "Local dev (real upstreams)"
    configuration: real
    enabled: true
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

go run ./cmd/cli config validate --dir "$out" >/dev/null
echo "wrote: $out/{providers,policy,admin}.yaml"
