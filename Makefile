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

check: fmt vet lint generate ## Run all checks (format, vet, lint, generate)
	@echo "✅ All checks completed successfully"

# ==================================================================================== #
# TESTING
# ==================================================================================== #
test: ## Run tests
	@echo "==> Running tests..."
	go test -v ./...

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
docker-build-local: ## Build docker image locally using compose
	docker compose -f deployments/docker-compose.yml -p freighter-backend build

docker-build-tag: ## Build docker image and tag it
	DOCKER_BUILDKIT=1 docker build -t $(TAG) -f deployments/Dockerfile --label org.opencontainers.image.created="$(BUILD_TIME)" .

docker-up: ## Start docker containers using compose
	docker compose -f deployments/docker-compose.yml -p freighter-backend up

docker-push: ## Push tagged docker image
	docker push $(TAG)

docker-build-up: docker-build-local docker-up ## Build locally and start containers
