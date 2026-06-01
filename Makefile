GO         ?= go
NPM        ?= npm
GOLANGCI   ?= golangci-lint
COVER_OUT  := coverage.out
COVER_MIN  := 95
GATE       := scripts/coverage-gate.sh

DEV_ENV := \
	SLUICE_CONFIG_DIR=./config-dev \
	SLUICE_HTTP_BIND=0.0.0.0:8585 \
	SLUICE_PROMETHEUS_BIND=0.0.0.0:9090 \
	SLUICE_LOG_LEVEL=debug

# When the SPA is built, Vite emits to internal/admin/webdist/. The
# committed placeholder.html stands in for index.html on a fresh
# checkout so go:embed has something to attach.
WEB_OUT := internal/admin/webdist/index.html

.PHONY: all build test lint fmt vet coverage dev dev-with-overlay dev-compose dev-compose-down dev-real dev-real-down e2e py-compat smoke clean tools tools-proto proto web web-install web-dev

all: lint vet test

# `make build` includes a fresh SPA bundle. Skip the dependency with
# `make build NO_WEB=1` if you've already run `make web` and want to
# avoid the (~1s) Vite invocation.
ifndef NO_WEB
build: web
endif
build:
	$(GO) build ./...

web: web-install
	# Vite's emptyOutDir is off so placeholder.html + .gitignore survive
	# rebuilds. Clear only the generated artefacts here before invoking
	# Vite — anything not in this list is preserved.
	rm -rf internal/admin/webdist/index.html \
		internal/admin/webdist/assets \
		internal/admin/webdist/favicon.ico \
		internal/admin/webdist/sluice.png \
		internal/admin/webdist/sluice.svg
	cd web && $(NPM) run build

web-install:
	cd web && $(NPM) install --silent

web-dev: web-install
	cd web && $(NPM) run dev

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...
	$(GO) run golang.org/x/tools/cmd/goimports@latest -w -local github.com/andyjmorgan/sluice-gateway .

lint:
	$(GOLANGCI) run ./...

test:
	$(GO) test -race -coverprofile=$(COVER_OUT) -covermode=atomic $$($(GO) list ./... | grep -v 'web/node_modules')

coverage: test
	@if [ -s $(COVER_OUT) ]; then $(GO) tool cover -func=$(COVER_OUT) | tail -n 1; fi
	@$(GATE) $(COVER_OUT) $(COVER_MIN)

dev:
	docker compose -f docker-compose.yaml up -d mockllm
	$(DEV_ENV) $(GO) run ./cmd/gateway

dev-with-overlay:
	@test -f docker-compose.dev.yaml || { echo "docker-compose.dev.yaml not found; copy docker-compose.dev.yaml.example"; exit 1; }
	docker compose -f docker-compose.yaml -f docker-compose.dev.yaml up -d mockllm
	$(DEV_ENV) $(GO) run ./cmd/gateway

# Full-stack compose: gateway image (SPA embedded) + mockllm. Use this
# to exercise the production-shaped flow end-to-end — admin console on :8081,
# data plane on :8585. Slower iteration than `make dev` (image rebuild on Go
# or SPA changes); for SPA-only hot reload, leave this running and start
# `make web-dev` separately (the Vite dev server proxies /api/v1 to :8081).
dev-compose:
	docker compose up -d --build

dev-compose-down:
	docker compose down

# Real-upstream compose: generates config-dev.real/ from .env, then
# brings up the gateway pointed at api.openai.com / api.anthropic.com
# / generativelanguage.googleapis.com + a host-port-forwarded ollama.
# Requires .env to contain OPENAI_API_KEY, ANTHROPIC_API_KEY,
# GEMINI_API_KEY. For the qwen-ollama path you need a kubectl
# port-forward to host port 11434 running separately.
dev-real:
	bash scripts/dev-real-config.sh
	docker compose -f docker-compose.yaml -f docker-compose.real.yaml --env-file .env up -d --no-deps --build gateway

dev-real-down:
	docker compose -f docker-compose.yaml -f docker-compose.real.yaml down

e2e:
	TESTCONTAINERS_RYUK_DISABLED=true $(GO) test -tags=e2e -race -count=1 -timeout=5m ./test/e2e/...

py-compat:
	$(GO) build -o /tmp/sluice-gateway ./cmd/gateway
	$(GO) build -o /tmp/sluice-mockllm ./cmd/mockllm
	cd test/python && uv run --project . pytest -v

# Run the post-deploy smoke suite against a live gateway.
#
# Required:  SLUICE_API_KEY=sk_live_...           (managed-mode key)
# Optional:  SLUICE_BASE_URL=https://...          (default: https://sluice.donkeywork.dev)
#            SLUICE_SMOKE_QWEN=true               (enable cluster-side qwen redirect tests)
smoke:
	cd test/smoke && uv run --project . pytest -v

clean:
	rm -f $(COVER_OUT) coverage.html

tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

# tools-proto installs the protobuf Go plugins. protoc itself comes from the OS
# package manager (macOS: `brew install protobuf`).
tools-proto:
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# proto regenerates the control-plane gRPC stubs. Generated *.pb.go are
# committed, so CI builds without protoc — only regeneration needs the toolchain
# (see tools-proto). goimports normalises the generated import grouping so
# `make fmt` stays a no-op on the output.
proto:
	protoc \
	  --go_out=. --go_opt=module=github.com/andyjmorgan/sluice-gateway \
	  --go-grpc_out=. --go-grpc_opt=module=github.com/andyjmorgan/sluice-gateway \
	  proto/controlplane/v1/fleet.proto
	$(GO) run golang.org/x/tools/cmd/goimports@latest -w -local github.com/andyjmorgan/sluice-gateway internal/controlplane/fleetpb
