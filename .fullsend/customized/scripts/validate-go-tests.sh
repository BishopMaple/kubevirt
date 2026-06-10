#!/usr/bin/env bash
# Validate that the Go test generator produced expected output files.
set -euo pipefail

OUTPUT_DIR="${1:-${FULLSEND_OUTPUT_DIR:-$(pwd)/output}}"
if [ ! -d "$OUTPUT_DIR" ]; then
    echo "FAIL: output directory not found: $OUTPUT_DIR"
    exit 1
fi

errors=0

# Check for Go test files
go_tests=$(find "$OUTPUT_DIR" -name "*_test.go" 2>/dev/null | wc -l)
if [ "$go_tests" -eq 0 ]; then
    echo "FAIL: no *_test.go files found in $OUTPUT_DIR"
    errors=$((errors + 1))
else
    echo "OK: found $go_tests Go test file(s)"
fi

# Check for summary
if [ -f "$OUTPUT_DIR/summary.yaml" ]; then
    echo "OK: summary.yaml found"
else
    echo "WARN: summary.yaml not found (non-fatal)"
fi

if [ "$errors" -gt 0 ]; then
    echo "FAIL: $errors validation error(s)"
    exit 1
fi


echo "PASS: output validated"
