NAME        := ssh-agent-multiplexer
NAME_SELECT_TOOL := ssh-agent-mux-select
PROJECTROOT := $(shell pwd)
VERSION     := $(shell git describe --tags --abbrev=1 --dirty)
REVISION    := $(shell git rev-parse --short HEAD)
OUTDIR      ?= $(PROJECTROOT)/dist

LDFLAGS := -ldflags="-s -w -X main.Version=$(VERSION:v%=%) -X main.Revision=$(REVISION)"

export GOTOOLCHAIN=auto

.PHONY: build
build: fmt lint build-only

.PHONY: build-only
build-only: build-mux build-select-tool

.PHONY: build-mux
build-mux:
	go build $(LDFLAGS) -o $(OUTDIR)/$(NAME) ./

.PHONY: build-select-tool
build-select-tool:
	go build $(LDFLAGS) -o $(OUTDIR)/$(NAME_SELECT_TOOL) ./cmd/ssh-agent-mux-select/

.PHONY: lint
lint:
	$(shell go env GOPATH)/bin/golangci-lint run

.PHONY: test
test:
	go test ./pkg/...

.PHONY: fmt
fmt:
	$(shell go env GOPATH)/bin/goimports -w ./ pkg/
	$(shell go env GOPATH)/bin/go-licenser --license ASL2-Short --licensor "Shingo Omura"

.PHONY: clean
clean:
	rm -rf $(OUTDIR)

.PHONY: setup
setup:
	cd $(shell go env GOPATH) && \
	go install golang.org/x/tools/cmd/goimports@latest && \
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin && \
	go install github.com/elastic/go-licenser@latest
