BINARY      := fling
MODULE      := github.com/lthummus/fileflinger

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

PROTOC_GEN_GO_VERSION      := v1.36.12
PROTOC_GEN_GO_GRPC_VERSION := v1.6.2

GO ?= go

.DEFAULT_GOAL := help
.PHONY: help build build-linux install run test race lint vet tidy proto tools snapshot packages deb spk clean version

help:
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) \
	    | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build ./fling for the host platform
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) .

build-linux: ## Cross-build dist/fling-linux-amd64
	GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BINARY)-linux-amd64 .

install: ## Install fling into $(GOPATH)/bin
	$(GO) install -trimpath -ldflags '$(LDFLAGS)' .

run: build ## Build, then run all-in-one on :6446 with a scratch DB
	./$(BINARY) serve \
	    --socket-path /tmp/fileflinger/daemon.sock \
	    --fd-socket-path /tmp/fileflinger/fd.sock \
	    --database-file /tmp/fileflinger/dev.db \
	    --base-url http://localhost:6446

test: ## Run tests
	$(GO) test ./...

race: ## Run tests under the race detector
	$(GO) test -race ./...

vet: ## go vet
	$(GO) vet ./...

lint: ## golangci-lint (install: brew install golangci-lint)
	golangci-lint run

tidy: ## Tidy and verify modules
	$(GO) mod tidy
	$(GO) mod verify

version: build
	./$(BINARY) --version

## --- codegen -------------------------------------------------------------

proto:
	protoc \
	    --go_out=. --go_opt=paths=source_relative \
	    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	    proto/fileflinger.v1.proto

tools: ## Install pinned codegen plugins (protoc itself: brew install protobuf)
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

snapshot: ## Build release archives, .debs, .rpms and .spks into dist/ without publishing
	goreleaser release --snapshot --clean

packages: ## Build the .debs and .rpms (and .spks, which come along with every build) into dist/
	goreleaser release --snapshot --clean --skip=archive

deb: packages

spk: ## Build just the Synology DSM 7 packages into dist/
	goreleaser release --snapshot --clean --skip=archive,nfpm

clean:
	rm -rf $(BINARY) dist/
