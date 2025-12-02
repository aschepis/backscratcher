.PHONY: build clean run test install lint check help gen cli server test-coverage test-coverage-html test-coverage-func test-coverage-all

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
	rm -f coverage.out coverage.html

run: build ## Build and run the CLI
	./$(CLI_BINARY)

test: ## Run tests (with FTS5 enabled)
	CGO_ENABLED=1 $(GOTEST) -v $(BUILD_FLAGS) ./...

test-coverage: ## Run tests with coverage and display summary
	CGO_ENABLED=1 $(GOTEST) -v $(BUILD_FLAGS) -coverprofile=coverage.out -covermode=atomic ./...
	@echo ""
	@echo "Coverage summary:"
	@$(GOCMD) tool cover -func=coverage.out | tail -1

test-coverage-html: test-coverage ## Generate HTML coverage report
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "HTML coverage report generated: coverage.html"
	@echo "Opening in browser..."
	@open coverage.html 2>/dev/null || xdg-open coverage.html 2>/dev/null || echo "Please open coverage.html manually"

test-coverage-func: test-coverage ## Show function-level coverage
	@echo "Function-level coverage:"
	@$(GOCMD) tool cover -func=coverage.out

test-coverage-all: test-coverage-func test-coverage-html ## Comprehensive coverage report (function-level + HTML)
	@echo ""
	@echo "Coverage report complete!"

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
