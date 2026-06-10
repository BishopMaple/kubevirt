#!/usr/bin/env bash
# Validate that the Go test generator produced expected output files.
set -euo pipefail

OUTPUT_DIR="${1:-${FULLSEND_OUTPUT_DIR:-$(pwd)/output}}"

if [ ! -d "$OUTPUT_DIR" ]; then
    mkdir -p "$OUTPUT_DIR"
fi

errors=0

# Check for Go test files
go_tests=$(find "$OUTPUT_DIR" -name "*_test.go" 2>/dev/null | wc -l)
if [ "$go_tests" -eq 0 ]; then
    for search_root in /sandbox/workspace/pr-repo/outputs/go-tests /sandbox/workspace/target-repo/outputs/go-tests; do
        if [ -d "$search_root" ]; then
            pr_tests=$(find "$search_root" -name "*_test.go" 2>/dev/null | head -1)
            if [ -n "$pr_tests" ]; then
                pr_out_dir=$(dirname "$pr_tests")
                echo "Found Go tests in target repo: $pr_out_dir"
                cp -r "$pr_out_dir"/* "$OUTPUT_DIR/" 2>/dev/null || true
                go_tests=$(find "$OUTPUT_DIR" -name "*_test.go" 2>/dev/null | wc -l)
                break
            fi
        fi
    done
fi

if [ "$go_tests" -eq 0 ]; then
    echo "FAIL: no *_test.go files found"
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

echo "PASS: output validated ($(find "$OUTPUT_DIR" -type f 2>/dev/null | wc -l) files in extraction dir)"
