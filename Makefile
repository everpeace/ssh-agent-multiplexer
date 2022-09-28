NAME        := ssh-agent-multiplexer
PROJECTROOT := $(shell pwd)
VERSION     := $(if $(VERSION),$(VERSION),$(shell cat ${PROJECTROOT}/VERSION)-dev)
REVISION    := $(shell git rev-parse --short HEAD)
OUTDIR      ?= $(PROJECTROOT)/dist

LDFLAGS := -ldflags="-s -w -X main.Version=$(VERSION) -X main.Revision=$(REVISION)"

.PHONY: build
build: fmt lint
	go build $(LDFLAGS) -o $(OUTDIR)/$(NAME)


.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: clean
clean:
	rm -rf $(OUTDIR)
