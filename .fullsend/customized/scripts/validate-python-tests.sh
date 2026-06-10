#!/usr/bin/env bash
# Validate that the Python test generator produced expected output files.
set -euo pipefail

OUTPUT_DIR="${1:-${FULLSEND_OUTPUT_DIR:-$(pwd)/output}}"
if [ ! -d "$OUTPUT_DIR" ]; then
    echo "FAIL: output directory not found: $OUTPUT_DIR"
    exit 1
fi

errors=0

# Check for Python test files
py_tests=$(find "$OUTPUT_DIR" -name "test_*.py" 2>/dev/null | wc -l)
if [ "$py_tests" -eq 0 ]; then
    echo "FAIL: no test_*.py files found in $OUTPUT_DIR"
    errors=$((errors + 1))
else
    echo "OK: found $py_tests Python test file(s)"
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
