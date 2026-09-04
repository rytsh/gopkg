BINARY_NAME := gopkg
BINARY_PATH := ./cmd/$(BINARY_NAME)

BUILD_DATE := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
BUILD_COMMIT := $(shell git rev-parse --short HEAD)
VERSION := $(or $(IMAGE_TAG),$(shell git describe --tags --first-parent --match "v*" 2> /dev/null || echo v0.0.0))
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(BUILD_COMMIT) -X main.date=$(BUILD_DATE)

.DEFAULT_GOAL := help

.PHONY: test
test: ## Run all Go tests
	go test ./...

.PHONY: build
build: ## Build the current platform with GoReleaser
	goreleaser build --snapshot --clean --single-target

.PHONY: run
run: ## Serve the current directory
	go run -ldflags="$(LDFLAGS)" $(BINARY_PATH)

.PHONY: clean
clean: ## Remove build outputs
	@rm -rf bin dist

.PHONY: help
help: ## Display this help screen
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
