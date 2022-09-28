NAME        := ssh-agent-multiplexer
PROJECTROOT := $(shell pwd)
VERSION     := $(if $(VERSION),$(VERSION),$(shell cat ${PROJECTROOT}/VERSION)-dev)
REVISION    := $(shell git rev-parse --short HEAD)
OUTDIR      ?= $(PROJECTROOT)/dist

LDFLAGS := -ldflags="-s -w -X main.Version=$(VERSION) -X main.Revision=$(REVISION)"

.PHONY: build
build: fmt lint build-only

.PHONY: build-only
build-only:
	go build $(LDFLAGS) -o $(OUTDIR)/$(NAME)

.PHONY: lint
lint:
	$(shell go env GOPATH)/bin/golangci-lint run

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
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin v1.49.0 && \
	go install github.com/elastic/go-licenser@latest

#
# Release
#
.PHONY: release
release: guard-RELEASE guard-RELEASE_TAG
	git diff --quiet HEAD || (echo "your current branch is dirty" && exit 1)
	git tag $(RELEASE_TAG) $(REVISION)
	git push origin $(RELEASE_TAG)
guard-%:
	@ if [ "${${*}}" = "" ]; then \
		echo "Environment variable $* is not set"; \
		exit 1; \
	fi
