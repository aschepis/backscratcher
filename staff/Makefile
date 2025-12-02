.PHONY: build clean run test install lint check help gen cli server

# Binary names
CLI_BINARY=staff
SERVER_BINARY=staffd

# Proto paths
PROTO_DIR=api/proto
PROTO_OUT=api/staffpb

# Build flags
BUILD_FLAGS=-tags sqlite_fts5

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: gen cli server ## Build both CLI and server

cli: gen ## Build the CLI
	CGO_ENABLED=1 $(GOBUILD) $(BUILD_FLAGS) -o $(CLI_BINARY) ./cmd/staff

server: gen ## Build the server daemon
	CGO_ENABLED=1 $(GOBUILD) $(BUILD_FLAGS) -o $(SERVER_BINARY) ./cmd/staffd

clean: ## Remove build artifacts
	$(GOCLEAN)
	rm -f $(CLI_BINARY) $(SERVER_BINARY)
	rm -f *.db *.db-shm *.db-wal

run: build ## Build and run the CLI
	./$(CLI_BINARY)

test: ## Run tests (with FTS5 enabled)
	CGO_ENABLED=1 $(GOTEST) -v $(BUILD_FLAGS) ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

check: lint test ## Run lint and tests

tidy: ## Tidy go modules
	$(GOMOD) tidy

install: ## Install dependencies
	$(GOGET) -v ./...
	$(GOMOD) tidy

dev: clean build ## Clean build for development

all: clean install build test ## Full build pipeline

gen: ## Generate Go code from protobuf definitions using buf
	buf generate
	@echo "Generated protobuf code in $(PROTO_OUT)"
