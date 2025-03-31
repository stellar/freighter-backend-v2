#!/bin/bash
set -e

# Run go generate on all packages
echo "Running go generate..."
go generate ./...

echo "✅ Go generate completed successfully"
 