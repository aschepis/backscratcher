.PHONY: build clean run test install lint check help

# Binary name
BINARY_NAME=staff

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

build: ## Build the application with FTS5 support
	CGO_ENABLED=1 $(GOBUILD) $(BUILD_FLAGS) -o $(BINARY_NAME)

clean: ## Remove build artifacts
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f *.db *.db-shm *.db-wal

run: build ## Build and run the application
	./$(BINARY_NAME)

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
