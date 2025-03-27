#!/bin/bash
set -e

# Check if staticcheck is installed
if ! command -v staticcheck &> /dev/null; then
  echo "Error: staticcheck is not installed"
  echo "Install it with: go install honnef.co/go/tools/cmd/staticcheck@latest"
  exit 1
fi

# Run staticcheck on all packages
echo "Running staticcheck..."
staticcheck ./...

echo "✅ Staticcheck completed successfully" 