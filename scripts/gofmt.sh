#!/bin/bash
set -e

# Run gofmt to check if all files are formatted
echo "Checking if code is formatted with gofmt..."
UNFORMATTED=$(gofmt -l .)

if [ -n "$UNFORMATTED" ]; then
  echo "The following files are not formatted correctly:"
  echo "$UNFORMATTED"
  echo ""
  echo "Running gofmt to format the code..."
  gofmt -w .
  echo "Code formatting completed."
else
  echo "✅ All files are properly formatted."
fi
