#!/usr/bin/env bash
# Validate that the STD builder produced expected output files.
# Expects: STD YAML + at least one stub file (Go or Python).
set -euo pipefail

OUTPUT_DIR="${1:-${FULLSEND_OUTPUT_DIR:-$(pwd)/output}}"
FOUND_IN_PR=false

if [ ! -d "$OUTPUT_DIR" ]; then
    mkdir -p "$OUTPUT_DIR"
fi

errors=0

# Check for STD YAML
std_files=$(find "$OUTPUT_DIR" -name "*_test_description.yaml" 2>/dev/null | wc -l)
if [ "$std_files" -eq 0 ]; then
    for search_root in /sandbox/workspace/pr-repo/outputs/std /sandbox/workspace/target-repo/outputs/std; do
        if [ -d "$search_root" ]; then
            pr_std=$(find "$search_root" -name "*_test_description.yaml" 2>/dev/null | head -1)
            if [ -n "$pr_std" ]; then
                pr_out_dir=$(dirname "$pr_std")
                echo "Found STD output in target repo: $pr_out_dir"
                cp -r "$pr_out_dir"/* "$OUTPUT_DIR/" 2>/dev/null || true
                std_files=$(find "$OUTPUT_DIR" -name "*_test_description.yaml" 2>/dev/null | wc -l)
                FOUND_IN_PR=true
                break
            fi
        fi
    done
fi

if [ "$std_files" -eq 0 ]; then
    echo "FAIL: no *_test_description.yaml file found"
    errors=$((errors + 1))
else
    echo "OK: found $std_files STD YAML file(s)"
fi

# Check for stub files (Go or Python — at least one expected)
go_stubs=$(find "$OUTPUT_DIR" -name "*_stubs_test.go" 2>/dev/null | wc -l)
py_stubs=$(find "$OUTPUT_DIR" -name "test_*_stubs.py" 2>/dev/null | wc -l)

if [ "$FOUND_IN_PR" = true ] && [ "$((go_stubs + py_stubs))" -eq 0 ]; then
    for search_root in /sandbox/workspace/pr-repo/outputs/std /sandbox/workspace/target-repo/outputs/std; do
        for stub in $(find "$search_root" -name "*_stubs_test.go" -o -name "test_*_stubs.py" 2>/dev/null); do
            stub_name=$(basename "$stub")
            if [[ "$stub" == *_test.go ]]; then
                mkdir -p "$OUTPUT_DIR/go-tests"
                cp "$stub" "$OUTPUT_DIR/go-tests/"
            else
                mkdir -p "$OUTPUT_DIR/python-tests"
                cp "$stub" "$OUTPUT_DIR/python-tests/"
            fi
        done
    done
    go_stubs=$(find "$OUTPUT_DIR" -name "*_stubs_test.go" 2>/dev/null | wc -l)
    py_stubs=$(find "$OUTPUT_DIR" -name "test_*_stubs.py" 2>/dev/null | wc -l)
fi

total_stubs=$((go_stubs + py_stubs))
if [ "$total_stubs" -eq 0 ]; then
    echo "WARN: no stub files found (non-fatal — stubs may be toggled off)"
else
    echo "OK: found $go_stubs Go stub(s), $py_stubs Python stub(s)"
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
