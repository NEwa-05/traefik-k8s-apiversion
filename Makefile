BINARY  := tkav
PKG     := ./cmd/tkav
DIST    := dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Platforms built by build-all, as GOOS/GOARCH.
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

GOLANGCI_LINT        ?= golangci-lint
GOLANGCI_LINT_CONFIG := .github/.golangci.yml

LDFLAGS := -s -w -X main.version=$(VERSION)
GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)'

.DEFAULT_GOAL := all

.PHONY: all
all: lint test build ## Lint, test and build for the current platform

.PHONY: build
build: ## Build tkav for the current platform
	$(GOBUILD) -o $(BINARY)$(shell go env GOEXE) $(PKG)

.PHONY: build-all
build-all: clean-dist ## Build tkav for every platform of PLATFORMS into dist/, with checksums
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; ext=; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		out=$(DIST)/$(BINARY)-$(VERSION)-$$os-$$arch$$ext; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch $(GOBUILD) -o $$out $(PKG) || exit 1; \
	done
	cd $(DIST) && shasum -a 256 $(BINARY)-* > checksums.txt

.PHONY: test
test: ## Run the tests
	go test -race -cover ./...

.PHONY: lint
lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run --config $(GOLANGCI_LINT_CONFIG)

.PHONY: fmt
fmt: ## Format the code with the formatters of the golangci-lint configuration
	$(GOLANGCI_LINT) fmt --config $(GOLANGCI_LINT_CONFIG)

.PHONY: clean-dist
clean-dist:
	rm -rf $(DIST)

.PHONY: clean
clean: clean-dist ## Remove build outputs
	rm -f $(BINARY) $(BINARY).exe

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'
