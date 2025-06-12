NAME        := ssh-agent-multiplexer
NAME_SELECT_TOOL := ssh-agent-mux-select
PROJECTROOT := $(shell pwd)
VERSION     := $(shell git describe --tags --abbrev=1 --dirty)
REVISION    := $(shell git rev-parse --short HEAD)
DISTDIR      ?= $(PROJECTROOT)/dist
DEVDIR      ?= $(PROJECTROOT)/.dev

LDFLAGS := -ldflags="-s -w -X main.Version=$(VERSION:v%=%) -X main.Revision=$(REVISION)"

export GOTOOLCHAIN=auto

.PHONY: build
build: fmt lint build-only ## Build (with fmt, lint)

.PHONY: build-only
build-only: build-mux build-select-tool  ## Build only (without fmt,lint)

.PHONY: build-mux
build-mux:  ## Build ssh-agent-multiplexer
	go build $(LDFLAGS) -o $(DISTDIR)/$(NAME) ./cmd/ssh-agent-multiplexer/

.PHONY: build-select-tool
build-select-tool:  ## Build ssh-agent-mux-select
	go build $(LDFLAGS) -o $(DISTDIR)/$(NAME_SELECT_TOOL) ./cmd/ssh-agent-mux-select/

.PHONY: lint
lint:  ## Run lint
	GOOS=darwin $(GOLANGCI_LINT) run
	GOOS=linux $(GOLANGCI_LINT) run

.PHONY: test
test:  ## Run tests
	go test ./...

.PHONY: fmt
fmt:  ## Format code
	$(GOIMPORTS) -w ./ pkg/
	$(GO_LICENSER) --license ASL2-Short --licensor "Shingo Omura"

.PHONY: clean
clean:  # Clean $(DISTDIR)
	rm -rf $(DISTDIR)

.PHONY: clean-dev
clean-dev:  ## Clean $(DEVDIR)
	rm -rf $(DEVDIR)

#
# Dev Tools
#
DEVTOOL_BIN_DIR := $(DEVDIR)/bin
GOIMPORTS       := $(DEVTOOL_BIN_DIR)/goimports
GOLANGCI_LINT   := $(DEVTOOL_BIN_DIR)/golangci-lint
GO_LICENSER     := $(DEVTOOL_BIN_DIR)/go-licenser

.PHONY: setup goimports golangci-lint go-licenser
setup: goimports golangci-lint go-licenser  ## Install Dev Tools to $(DEVDIR)/bin

goimports: $(GOIMPORTS)
$(GOIMPORTS): $(DEVTOOL_BIN_DIR)
	GOBIN=$(DEVTOOL_BIN_DIR) go install golang.org/x/tools/cmd/goimports@latest

golangci-lint: $(GOLANGCI_LINT)
$(GOLANGCI_LINT): $(DEVTOOL_BIN_DIR)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b  $(DEVTOOL_BIN_DIR)

go-licenser: $(GO_LICENSER)
$(GO_LICENSER): $(DEVTOOL_BIN_DIR)
	GOBIN=$(DEVTOOL_BIN_DIR) go install github.com/elastic/go-licenser@latest

$(DEVTOOL_BIN_DIR):
	mkdir -p $(DEVTOOL_BIN_DIR)

help: ## Display this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
