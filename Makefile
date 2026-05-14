GO         ?= go
GOLANGCI   ?= golangci-lint
COVER_OUT  := coverage.out
COVER_MIN  := 95
GATE       := scripts/coverage-gate.sh

.PHONY: all build test lint fmt vet coverage dev clean tools

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
	docker compose -f docker-compose.yaml -f docker-compose.dev.yaml up -d mockllm nats
	$(GO) run ./cmd/gateway

clean:
	rm -f $(COVER_OUT) coverage.html

tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
