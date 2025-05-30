# ==================================================================================== #
# VARIABLES
# ==================================================================================== #
BINARY_NAME=freighter-backend
# Assuming main.go is in the root. Adjust if it's in e.g. ./cmd/freighter-backend/
CMD_PATH=.

# Versioning - Reuses LABEL and BUILD_DATE from original file
VERSION ?= $(shell git rev-parse --short HEAD)$(and $(shell git status -s),-dirty-$(shell id -u -n))
BUILD_TIME := $(shell date -u +%FT%TZ)
TAG ?= stellar/freighter-backend-v2:$(VERSION)

# Go build flags
# Inject version info: requires 'var version string', 'var buildTime string' in main package
LDFLAGS = -ldflags="-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"

# ==================================================================================== #
# HELPERS
# ==================================================================================== #
.DEFAULT_GOAL := help
.PHONY: help tidy fmt vet lint generate check test build run clean all docker-build-local docker-build-tag docker-up docker-push docker-build-up

help: ## Display this help screen
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z_\-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

# ==================================================================================== #
# QUALITY & PREPARATION
# ==================================================================================== #
tidy: ## Tidy modfiles and format source files
	@echo "==> Tidying module files..."
	go mod tidy -v
	@echo "==> Formatting code..."
	go fmt ./...

fmt: ## Check if code is formatted with gofmt
	@echo "==> Checking formatting..."
	@test -z $(shell gofmt -l .) || (echo "ERROR: Unformatted files found. Run 'make tidy' or 'gofmt -w .'"; exit 1)
	@echo "Format check passed."

vet: ## Run go vet checks
	@echo "==> Running go vet..."
	go vet ./...

lint: ## Run golangci-lint linter (requires: brew install golangci-lint or equivalent)
	@echo "==> Running golangci-lint..."
	@command -v golangci-lint >/dev/null 2>&1 || { echo >&2 "ERROR: golangci-lint not found. Install it: https://golangci-lint.run/usage/install/"; exit 1; }
	golangci-lint run ./...

generate: ## Run go generate
	@echo "==> Running go generate..."
	go generate ./...

shadow: ## Run shadow analysis to find shadowed variables
	@echo "==> Running shadow analyzer..."
	@if ! command -v shadow >/dev/null 2>&1; then \
		echo "Installing shadow..."; \
		go install golang.org/x/tools/go/analysis/passes/shadow/cmd/shadow@v0.31.0; \
	fi
	$(shell go env GOPATH)/bin/shadow ./...

exhaustive: ## Check exhaustiveness of switch statements
	@echo "==> Running exhaustive..."
	@command -v exhaustive >/dev/null 2>&1 || { go install github.com/nishanths/exhaustive/cmd/exhaustive@v0.12.0; }
	$(shell go env GOPATH)/bin/exhaustive -default-signifies-exhaustive ./...

deadcode: ## Find unused code
	@echo "==> Checking for deadcode..."
	@if ! command -v deadcode >/dev/null 2>&1; then \
		echo "Installing deadcode..."; \
		go install golang.org/x/tools/cmd/deadcode@v0.31.0; \
	fi
	@output=$$($(shell go env GOPATH)/bin/deadcode -test ./...); \
	if [ -n "$$output" ]; then \
		echo "🚨 Deadcode found:"; \
		echo "$$output"; \
		exit 1; \
	else \
		echo "✅ No deadcode found"; \
	fi

goimports: ## Check import formatting and organization
	@echo "==> Checking imports..."
	@command -v goimports >/dev/null 2>&1 || { go install golang.org/x/tools/cmd/goimports@v0.31.0; }
	@non_compliant_files=$$(find . -type f -name "*.go" ! -path "*mock*" | xargs $(shell go env GOPATH)/bin/goimports -local "github.com/stellar/freighter-backend-v2" -l); \
	if [ -n "$$non_compliant_files" ]; then \
		echo "🚨 The following files are not compliant with goimports:"; \
		echo "$$non_compliant_files"; \
		exit 1; \
	else \
		echo "✅ All files are compliant with goimports."; \
	fi

govulncheck: ## Check for known vulnerabilities
	@echo "==> Running vulnerability check..."
	@command -v govulncheck >/dev/null 2>&1 || { go install golang.org/x/vuln/cmd/govulncheck@latest; }
	$(shell go env GOPATH)/bin/govulncheck ./...

check: tidy fmt vet lint generate shadow exhaustive deadcode goimports govulncheck ## Run all checks
	@echo "✅ All checks completed successfully"

# ==================================================================================== #
# TESTING
# ==================================================================================== #
unit-test: ## Run unit tests
	@echo "==> Running unit tests..."
	ENABLE_INTEGRATION_TESTS=false go test -v -race ./...

unit-test-coverage: ## Run unit tests with coverage
	@echo "==> Running unit tests with coverage..."
	ENABLE_INTEGRATION_TESTS=false go test -v -race -cover -coverprofile=c.out ./...

integration-test: ## Run integration tests
	@echo "==> Running integration tests..."
	ENABLE_INTEGRATION_TESTS=true go test -v ./internal/integrationtests/...

test-all: unit-test-coverage integration-test ## Run all tests
	@echo "✅ All tests completed successfully"

# ==================================================================================== #
# BUILD & RUN
# ==================================================================================== #
build: ## Build the application binary with version info
	@echo "==> Building binary..."
	go build $(LDFLAGS) -o $(BINARY_NAME) $(CMD_PATH)
	@echo "Binary created: ./$(BINARY_NAME)"

run: build ## Build and run the application
	@echo "==> Running application..."
	./$(BINARY_NAME)

# ==================================================================================== #
# CLEANUP
# ==================================================================================== #
clean: ## Remove build artifacts
	@echo "==> Cleaning..."
	rm -f $(BINARY_NAME)
	go clean

# ==================================================================================== #
# ALL
# ==================================================================================== #
all: tidy check test build ## Run tidy, checks, tests, and build

# ==================================================================================== #
# DOCKER OPERATIONS
# ==================================================================================== #
# Check if we need to prepend docker commands with sudo
SUDO := $(shell docker version >/dev/null 2>&1 || echo "sudo")

docker-build-local: ## Build docker image locally using compose
	docker compose -f deployments/docker-compose.yml -p freighter-backend build

docker-build-tag: ## Build docker image and tag it
	$(SUDO) docker build --pull --label org.opencontainers.image.created="$(BUILD_TIME)" -t $(TAG) -f deployments/Dockerfile .

docker-up: ## Start docker containers using compose
	docker compose -f deployments/docker-compose.yml -p freighter-backend up

docker-push: ## Push tagged docker image
	$(SUDO) docker push $(TAG)

docker-build-up: docker-build-local docker-up ## Build locally and start containers
