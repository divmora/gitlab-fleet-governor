# ==============================================================================
# Makefile - GitLab Fleet Governor
# ==============================================================================

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

# Module & Paths
MODULE       := github.com/divmora/gitlab-fleet-governor
BIN_NAME     := gitlab-fleet-governor
BIN_DIR      := $(CURDIR)/bin
DIST_DIR     := $(CURDIR)/dist
COVERAGE_DIR := $(CURDIR)/coverage
CMD_PKG      := ./cmd/gitlab-fleet-governor

# Version & Build Metadata
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE   ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Go Environment
GO_MIN_VERSION := 1.26
GO             ?= go
GOFLAGS        ?=

LDFLAGS := -s -w \
  -X $(MODULE)/pkg/version.Version=$(VERSION) \
  -X $(MODULE)/pkg/version.GitCommit=$(GIT_COMMIT) \
  -X $(MODULE)/pkg/version.BuildDate=$(BUILD_DATE)

# Container Images
DOCKER_REGISTRY      ?= ghcr.io/divmora
DOCKER_IMAGE         ?= $(DOCKER_REGISTRY)/$(BIN_NAME):$(VERSION)
DOCKER_IMAGE_LATEST  ?= $(DOCKER_REGISTRY)/$(BIN_NAME):latest
DOCKER_LAMBDA_IMAGE  ?= $(DOCKER_REGISTRY)/$(BIN_NAME)-lambda:$(VERSION)
DOCKER_LAMBDA_LATEST ?= $(DOCKER_REGISTRY)/$(BIN_NAME)-lambda:latest

# ==============================================================================
# Top-Level Targets
# ==============================================================================

.PHONY: all
all: check-go-version lint test build ## Run full lint, test suite, and local compilation

# ==============================================================================
# Environment & Verification
# ==============================================================================

.PHONY: check-go-version
check-go-version: ## Verify installed Go version is >= 1.26
	@current_version=$$($(GO) version | awk '{print $$3}' | sed 's/go//'); \
	major=$$(echo "$$current_version" | cut -d. -f1); \
	minor=$$(echo "$$current_version" | cut -d. -f2); \
	if [ "$$major" -lt 1 ] || { [ "$$major" -eq 1 ] && [ "$$minor" -lt 26 ]; }; then \
		echo "ERROR: Go version $$current_version is below minimum required version $(GO_MIN_VERSION)"; \
		exit 1; \
	fi; \
	echo "Go version $$current_version satisfies requirement (>= $(GO_MIN_VERSION))"

# ==============================================================================
# Build Targets
# ==============================================================================

.PHONY: build
build: check-go-version ## Build CLI binary for host platform
	@echo "==> Building $(BIN_NAME) ($(VERSION))"
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BIN_NAME) $(CMD_PKG)
	@echo "Compiled binary: $(BIN_DIR)/$(BIN_NAME)"

.PHONY: build-all
build-all: check-go-version ## Cross-compile binaries for Linux, macOS, and Windows (amd64/arm64)
	@echo "==> Cross-compiling $(BIN_NAME) for all platforms"
	@mkdir -p $(DIST_DIR)
	@for os in linux darwin windows; do \
		for arch in amd64 arm64; do \
			ext=""; \
			if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
			out="$(DIST_DIR)/$(BIN_NAME)_$${os}_$${arch}$${ext}"; \
			echo "Building $$out..."; \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $$out $(CMD_PKG) || exit 1; \
		done; \
	done
	@echo "Cross-compilation complete in $(DIST_DIR)"

.PHONY: build-lambda
build-lambda: check-go-version ## Compile AWS Lambda custom runtime bootstrap zip (arm64 & amd64)
	@echo "==> Building AWS Lambda bootstrap zip"
	@mkdir -p $(BIN_DIR) $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/bootstrap $(CMD_PKG)
	(cd $(BIN_DIR) && zip -q $(DIST_DIR)/$(BIN_NAME)-lambda-arm64.zip bootstrap)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/bootstrap $(CMD_PKG)
	(cd $(BIN_DIR) && zip -q $(DIST_DIR)/$(BIN_NAME)-lambda-amd64.zip bootstrap)
	@rm -f $(BIN_DIR)/bootstrap
	@echo "Lambda bundles created in $(DIST_DIR)"

# ==============================================================================
# Quality Assurance & Testing
# ==============================================================================

.PHONY: test
test: check-go-version ## Run all unit tests with race detector and coverage
	@echo "==> Running test suite with race detector"
	$(GO) test -v -race -cover ./...

.PHONY: test-short
test-short: check-go-version ## Run short test suite
	@echo "==> Running short test suite"
	$(GO) test -short ./...

.PHONY: cover
cover: check-go-version ## Run tests and generate HTML coverage report
	@echo "==> Generating coverage profile"
	@mkdir -p $(COVERAGE_DIR)
	$(GO) test -v -race -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	$(GO) tool cover -func=$(COVERAGE_DIR)/coverage.out
	@echo "Coverage report generated at: $(COVERAGE_DIR)/coverage.html"

.PHONY: lint
lint: check-go-version ## Run golangci-lint on the entire workspace
	@echo "==> Running golangci-lint"
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=5m ./...; \
	else \
		echo "golangci-lint not found. Running go vet instead."; \
		$(GO) vet ./...; \
	fi

.PHONY: vet
vet: check-go-version ## Run go vet static analysis
	@echo "==> Running go vet"
	$(GO) vet ./...

.PHONY: fmt
fmt: ## Format Go source files using gofmt and goimports
	@echo "==> Formatting code"
	gofmt -s -w .
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w -local $(MODULE) .; \
	fi

.PHONY: tidy
tidy: ## Tidy and verify go modules
	@echo "==> Tidying go.mod and go.sum"
	$(GO) mod tidy
	$(GO) mod verify

.PHONY: test-coverage
test-coverage: cover ## Alias for cover (generate HTML coverage report)

.PHONY: dev-setup
dev-setup: check-go-version tidy ## Set up local development dependencies and tidy modules

.PHONY: lambda-package
lambda-package: build-lambda ## Alias for build-lambda (compile AWS Lambda bootstrap zip)

# ==============================================================================
# Docker Targets
# ==============================================================================

.PHONY: docker-build
docker-build: ## Build standard CLI/Daemon container image
	@echo "==> Building Docker image: $(DOCKER_IMAGE)"
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(DOCKER_IMAGE) \
		-t $(DOCKER_IMAGE_LATEST) \
		-f Dockerfile .

.PHONY: docker-build-multiarch
docker-build-multiarch: ## Build multi-arch container image with Buildx
	@echo "==> Building multi-arch Docker image: $(DOCKER_IMAGE)"
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(DOCKER_IMAGE) \
		-t $(DOCKER_IMAGE_LATEST) \
		-f Dockerfile .

.PHONY: docker-build-lambda
docker-build-lambda: ## Build AWS Lambda container image
	@echo "==> Building Lambda Docker image: $(DOCKER_LAMBDA_IMAGE)"
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(DOCKER_LAMBDA_IMAGE) \
		-t $(DOCKER_LAMBDA_LATEST) \
		-f Dockerfile.lambda .

# ==============================================================================
# Release & Cleanup
# ==============================================================================

.PHONY: release-snapshot
release-snapshot: ## Test GoReleaser release workflow locally in snapshot mode
	@echo "==> Testing GoReleaser snapshot build"
	goreleaser release --snapshot --clean --skip=publish

.PHONY: clean
clean: ## Clean up build artifacts, dist files, and test coverage
	@echo "==> Cleaning build outputs"
	@rm -rf $(BIN_DIR) $(DIST_DIR) $(COVERAGE_DIR) coverage.out coverage.txt

# ==============================================================================
# Self-Documenting Help
# ==============================================================================

.PHONY: help
help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
