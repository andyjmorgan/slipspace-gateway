GO         ?= go
GOLANGCI   ?= golangci-lint
COVER_OUT  := coverage.out
COVER_MIN  := 95
GATE       := scripts/coverage-gate.sh

DEV_ENV := \
	SLUICE_CONFIG_DIR=./config-dev \
	SLUICE_HTTP_BIND=0.0.0.0:8585 \
	SLUICE_PROMETHEUS_BIND=0.0.0.0:9090 \
	SLUICE_NATS_URL=nats://localhost:4222 \
	SLUICE_LOG_LEVEL=debug

.PHONY: all build test lint fmt vet coverage dev dev-with-overlay e2e py-compat clean tools

all: lint vet test

build:
	$(GO) build ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...
	$(GO) run golang.org/x/tools/cmd/goimports@latest -w -local github.com/andyjmorgan/sluice-gateway .

lint:
	$(GOLANGCI) run ./...

test:
	$(GO) test -race -coverprofile=$(COVER_OUT) -covermode=atomic ./...

coverage: test
	@if [ -s $(COVER_OUT) ]; then $(GO) tool cover -func=$(COVER_OUT) | tail -n 1; fi
	@$(GATE) $(COVER_OUT) $(COVER_MIN)

dev:
	docker compose -f docker-compose.yaml up -d mockllm nats
	$(DEV_ENV) $(GO) run ./cmd/gateway

dev-with-overlay:
	@test -f docker-compose.dev.yaml || { echo "docker-compose.dev.yaml not found; copy docker-compose.dev.yaml.example"; exit 1; }
	docker compose -f docker-compose.yaml -f docker-compose.dev.yaml up -d mockllm nats
	$(DEV_ENV) $(GO) run ./cmd/gateway

e2e:
	$(GO) test -tags=e2e -race -timeout=2m ./test/e2e/...

py-compat:
	$(GO) build -o /tmp/sluice-gateway ./cmd/gateway
	$(GO) build -o /tmp/sluice-mockllm ./cmd/mockllm
	cd test/python && uv run --project . pytest -v

clean:
	rm -f $(COVER_OUT) coverage.html

tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
