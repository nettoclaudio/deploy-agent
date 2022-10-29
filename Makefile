GO ?= go
GOLANGCI_LINT ?= golangci-lint
PROTOC ?= protoc

.PHONY: all
all: lint test

.PHONY: test
test: generate
	$(GO) test -race ./...

.PHONY: lint
lint: generate
	$(GOLANGCI_LINT) run ./...

.PHONY: generate
generate:
	$(PROTOC) --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/v1alpha1/*.proto
