#!/bin/bash
set -e

# Run all checks in sequence
echo "=== Running all Go checks ==="

# Run gofmt
echo -e "\n=== Running gofmt ==="
./scripts/gofmt.sh

# Run go vet
echo -e "\n=== Running go vet ==="
./scripts/govet.sh

# Run staticcheck if available
if command -v staticcheck &> /dev/null; then
  echo -e "\n=== Running staticcheck ==="
  ./scripts/golint.sh
else
  echo -e "\n⚠️ Skipping staticcheck (not installed)"
  echo "Install it with: go install honnef.co/go/tools/cmd/staticcheck@latest"
fi

# Run go generate
echo -e "\n=== Running go generate ==="
./scripts/gogenerate.sh

echo -e "\n✅ All checks completed successfully" 