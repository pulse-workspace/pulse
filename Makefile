# Pulse Makefile
# Single binary, multi-role IM platform

GO ?= go
BIN_DIR ?= bin
BINARY ?= $(BIN_DIR)/pulse
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS ?= -X main.version=$(VERSION) -s -w

.PHONY: help all build build-all dev cell gateway test test-unit test-integration \
        test-contract test-coverage \
        lint fmt vet clean docker docker-build docker-push compose-validate \
        migrate-up migrate-down migrate-status seed deps tidy proto

## help: Print this help
help:
	@echo "Pulse - single binary, multi-role IM platform"
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@grep -E '^## [a-zA-Z_-]+:' $(MAKEFILE_LIST) | sed 's/## //' | awk -F': ' '{printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

## all: Build + test
all: lint test build

## build: Build the pulse binary
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/pulse
	@echo "Built: $(BINARY)"

## build-all: Build for all platforms
build-all:
	@mkdir -p $(BIN_DIR)
	GOOS=linux   GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/pulse-linux-amd64   ./cmd/pulse
	GOOS=linux   GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/pulse-linux-arm64   ./cmd/pulse
	GOOS=darwin  GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/pulse-darwin-amd64  ./cmd/pulse
	GOOS=darwin  GOARCH=arm64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/pulse-darwin-arm64  ./cmd/pulse
	GOOS=windows GOARCH=amd64 $(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/pulse-windows-amd64.exe ./cmd/pulse
	@echo "Built all platforms in $(BIN_DIR)/"

## dev: Run in all-in-one dev mode
dev:
	$(GO) run ./cmd/pulse dev --config configs/dev-localhost.yaml

## cell: Run domain cell role
cell:
	$(GO) run ./cmd/pulse cell --config configs/dev-localhost.yaml

## gateway: Run WebSocket gateway role
gateway:
	$(GO) run ./cmd/pulse gateway --config configs/dev-localhost.yaml

## test: Run all tests
test: test-unit test-integration

## test-unit: Run unit tests
test-unit:
	$(GO) test -race -timeout=60s ./...

## test-integration: Run integration tests (requires Docker)
test-integration:
	$(GO) test -race -timeout=300s -tags=integration ./tests/integration/...

## test-contract: Run contract tests (requires live server)
test-contract:
	$(GO) test -race -timeout=60s -tags=contract ./tests/contract/...

## test-coverage: Run all tests with coverage profile, report per-package + total.
test-coverage:
	$(GO) test -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out | tail -1

## lint: Run linters
lint:
	@which golangci-lint > /dev/null || (echo "Install golangci-lint: https://golangci-lint.run" && exit 1)
	golangci-lint run ./...

## fmt: Format code
fmt:
	$(GO) fmt ./...

## vet: Run go vet
vet:
	$(GO) vet ./...

## clean: Clean build artifacts
clean:
	rm -rf $(BIN_DIR) coverage.html coverage.out coverage-summary.txt

## compose-validate: Validate the local Docker Compose configuration
compose-validate:
	docker compose config -q

## docker: Build Docker image
docker:
	docker build -t pulse:$(VERSION) -f Dockerfile .

## docker-build: Build with explicit tag
docker-build:
	docker build -t pulse-im/pulse:$(VERSION) -t pulse-im/pulse:latest -f Dockerfile .

## docker-push: Push to registry
docker-push:
	docker push pulse-im/pulse:$(VERSION)
	docker push pulse-im/pulse:latest

## migrate-up: Apply database migrations
migrate-up:
	$(GO) run ./cmd/migrate up

## migrate-down: Rollback last migration
migrate-down:
	$(GO) run ./cmd/migrate down

## migrate-status: Show migration status
migrate-status:
	$(GO) run ./cmd/migrate version

## seed: Seed demo data (Alice, Bob, Charlie, Admin)
seed:
	$(GO) run ./cmd/migrate seed

## deps: Install dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy

## tidy: Run go mod tidy
tidy:
	$(GO) mod tidy

## proto: Protocol message schemas
proto:
	@echo "Msgpack schemas in pkg/protocol/*.go are the source of truth"