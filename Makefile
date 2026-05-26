SHELL := /bin/bash

DIST_DIR := ./dist
OAPI_SCHEMA_FILE := api/api.yaml
OAPI_CONFIG_FILE := api/deepmap/api.yaml
OAPI_GENERATED_DIR := ./internal/api/generated

COMPOSE ?= docker compose

# Build metadata embedded into the binary via -ldflags.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

.PHONY: all build clean oapi mod-download lint test test-integration test-integration-up test-integration-down cover vuln sec

all: clean oapi build

mod-download:
	go mod download

build:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/dinonce ./cmd/dinonce

clean:
	rm -rf $(DIST_DIR)
	rm -rf $(OAPI_GENERATED_DIR)

# oapi-codegen, golangci-lint, govulncheck and gosec are declared as Go
# `tool` dependencies in go.mod (Go 1.24+), so we invoke them via `go tool`.
# No `go install` step is needed: `go tool` builds and caches them on first use.
oapi:
	mkdir -p $(OAPI_GENERATED_DIR)
	go tool oapi-codegen --config=$(OAPI_CONFIG_FILE) $(OAPI_SCHEMA_FILE)

lint:
	go tool golangci-lint run ./...

test:
	go test -race -count=1 -short ./internal/api/...

test-integration: test-integration-up
	go test -race -count=1 ./internal/ticket/...
	$(MAKE) test-integration-down

test-integration-up:
	$(COMPOSE) -f docker-compose.test.yaml up -d --wait

test-integration-down:
	$(COMPOSE) -f docker-compose.test.yaml down -v

cover:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out | tail -1

vuln:
	go tool govulncheck ./...

sec:
	go tool gosec -quiet ./...
