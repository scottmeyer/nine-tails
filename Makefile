SHELL        := /bin/bash
.SHELLFLAGS  := -eu -o pipefail -c
.DEFAULT_GOAL := help

BIN   := nine-tails
GO    ?= go

.PHONY: help
help: ## list targets
	@awk 'BEGIN {FS = ":.*##"; printf "nine-tails\n\nTargets:\n"} /^[a-zA-Z_-]+:.*?##/ {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## build ./bin/nine-tails
	$(GO) build -o bin/$(BIN) ./cmd/$(BIN)

.PHONY: install
install: ## go install into $(go env GOPATH)/bin
	$(GO) install ./cmd/$(BIN)

.PHONY: test
test: ## run all tests
	$(GO) test ./...

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## gofmt all sources
	gofmt -l -w .

.PHONY: clean
clean: ## remove build output
	rm -rf bin
