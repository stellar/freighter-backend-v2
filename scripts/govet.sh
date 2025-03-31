#!/bin/bash
set -e

# Run go vet on all packages
echo "Running go vet..."
go vet ./...

echo "✅ Go vet completed successfully" 
