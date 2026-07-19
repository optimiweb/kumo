SHELL := /bin/sh

.DEFAULT_GOAL := help

GO ?= go
BIN_DIR ?= bin
KUMO_BIN ?= $(BIN_DIR)/kumo
COVERPROFILE ?= coverage.out
COVERHTML ?= coverage.html
TEST_FLAGS ?= -count=1
INTEGRATION_TEST_PACKAGES ?= . ./test/integration
GOFILES = $$($(GO) list -f '{{range .GoFiles}}{{$$.Dir}}/{{.}} {{end}}{{range .TestGoFiles}}{{$$.Dir}}/{{.}} {{end}}{{range .XTestGoFiles}}{{$$.Dir}}/{{.}} {{end}}' ./...)

.PHONY: help build build-kumo run clean fmt fmt-check vet mod-verify tidy tidy-check test test-unit test-integration test-race coverage coverage-html check ci

help: ## List available targets.
	@printf '%s\n' \
		'Kumo development targets:' \
		'  make build              Build every Go package.' \
		'  make build-kumo         Build the kumo CLI at $(KUMO_BIN).' \
		'  make run ARGS="..."     Build and run the kumo CLI.' \
		'  make fmt                Format Go source files.' \
		'  make fmt-check          Fail when Go source is not formatted.' \
		'  make vet                Run go vet.' \
		'  make mod-verify         Verify downloaded module content.' \
		'  make tidy               Synchronize go.mod and go.sum.' \
		'  make tidy-check         Fail when go.mod or go.sum need updates.' \
		'  make test-unit          Run unit tests without integration tests.' \
		'  make test-integration   Run HTTP-backed root integration tests with -race.' \
		'  make test               Run unit and integration tests.' \
		'  make test-race          Run the complete test suite with -race.' \
		'  make coverage           Write combined coverage to $(COVERPROFILE).' \
		'  make coverage-html      Write HTML coverage to $(COVERHTML).' \
		'  make check              Run formatting, vet, module, tests, and build checks.' \
		'  make ci                 Run the CI-quality verification suite.' \
		'  make clean              Remove generated binaries and coverage files.'

build: ## Build every Go package.
	$(GO) build ./...

build-kumo: ## Build the kumo CLI.
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(KUMO_BIN) ./cmd/kumo

run: build-kumo ## Build and run the kumo CLI; pass arguments with ARGS="...".
	$(KUMO_BIN) $(ARGS)

clean: ## Remove generated binaries and coverage files.
	rm -rf $(BIN_DIR) $(COVERPROFILE) $(COVERHTML)

fmt: ## Format Go source files.
	gofmt -w $(GOFILES)

fmt-check: ## Fail when Go source is not formatted.
	test -z "$$(gofmt -l $(GOFILES))"

vet: ## Run static analysis provided by Go.
	$(GO) vet ./...

mod-verify: ## Verify downloaded module content against go.sum.
	$(GO) mod verify

tidy: ## Synchronize go.mod and go.sum.
	$(GO) mod tidy

tidy-check: ## Fail when go.mod or go.sum need updates.
	$(GO) mod tidy -diff

test: test-unit test-integration ## Run unit and integration tests.

test-unit: ## Run unit tests without HTTP-backed integration tests.
	$(GO) test $(TEST_FLAGS) ./...

test-integration: ## Run HTTP-backed root integration tests with the race detector.
	$(GO) test -race -tags=integration $(TEST_FLAGS) $(INTEGRATION_TEST_PACKAGES)

test-race: ## Run the complete test suite with the race detector.
	$(GO) test -race -tags=integration $(TEST_FLAGS) ./...

coverage: ## Write coverage for the complete test suite.
	$(GO) test -tags=integration -covermode=atomic -coverprofile=$(COVERPROFILE) $(TEST_FLAGS) ./...
	$(GO) tool cover -func=$(COVERPROFILE)

coverage-html: coverage ## Write HTML coverage for the complete test suite.
	$(GO) tool cover -html=$(COVERPROFILE) -o $(COVERHTML)

check: fmt-check vet mod-verify tidy-check test build ## Run local pre-commit checks.

ci: fmt-check vet mod-verify tidy-check test-race build ## Run the CI-quality verification suite.
