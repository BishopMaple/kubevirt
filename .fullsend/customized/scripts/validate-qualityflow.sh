#!/usr/bin/env bash
# validate-qualityflow.sh — Validate that the QualityFlow agent produced
# the expected output files: STP, STD, and optionally stubs.
#
# Used by the validation_loop in harness/qualityflow.yaml.
set -euo pipefail

OUTPUT_DIR="${1:-${FULLSEND_OUTPUT_DIR:-$(pwd)/output}}"
if [ ! -d "$OUTPUT_DIR" ]; then
    echo "FAIL: output directory not found: $OUTPUT_DIR"
    exit 1
fi

errors=0

# STP markdown (required).
stp_files=$(find "$OUTPUT_DIR" -name "*_test_plan.md" 2>/dev/null | wc -l)
if [ "$stp_files" -eq 0 ]; then
    echo "FAIL: no *_test_plan.md file found in $OUTPUT_DIR"
    errors=$((errors + 1))
else
    echo "OK: found $stp_files STP file(s)"
fi

# STP review (required).
stp_review=$(find "$OUTPUT_DIR" -name "*_stp_review.md" 2>/dev/null | wc -l)
if [ "$stp_review" -eq 0 ]; then
    echo "FAIL: no *_stp_review.md file found in $OUTPUT_DIR"
    errors=$((errors + 1))
else
    echo "OK: found STP review"
fi

# STD YAML (required).
std_files=$(find "$OUTPUT_DIR" -name "*_test_description.yaml" 2>/dev/null | wc -l)
if [ "$std_files" -eq 0 ]; then
    echo "FAIL: no *_test_description.yaml file found in $OUTPUT_DIR"
    errors=$((errors + 1))
else
    echo "OK: found $std_files STD file(s)"
fi

# STD review (required).
std_review=$(find "$OUTPUT_DIR" -name "*_std_review.md" 2>/dev/null | wc -l)
if [ "$std_review" -eq 0 ]; then
    echo "FAIL: no *_std_review.md file found in $OUTPUT_DIR"
    errors=$((errors + 1))
else
    echo "OK: found STD review"
fi

# Summary JSON (required).
if [ -f "$OUTPUT_DIR/summary.json" ]; then
    echo "OK: summary.json found"
else
    echo "FAIL: summary.json not found"
    errors=$((errors + 1))
fi

# Test files committed to PR (informational — agent may determine no tests needed).
if [[ -n "${PR_CHECKOUT_PATH:-}" && -n "${PRE_AGENT_HEAD:-}" && -d "${PR_CHECKOUT_PATH}" ]]; then
    committed_tests=$(git -C "${PR_CHECKOUT_PATH}" diff --name-only "${PRE_AGENT_HEAD}..HEAD" -- '*.go' '*.py' 2>/dev/null | wc -l | tr -d ' ')
    echo "INFO: ${committed_tests} test file(s) committed to PR branch"
else
    # Fallback: check output dir for stubs/tests.
    go_stubs=$(find "$OUTPUT_DIR" -name "*_test.go" -o -name "*_stubs_test.go" 2>/dev/null | wc -l)
    py_stubs=$(find "$OUTPUT_DIR" -name "test_*.py" 2>/dev/null | wc -l)
    echo "INFO: ${go_stubs} Go test file(s), ${py_stubs} Python test file(s) in output"
fi

if [ "$errors" -gt 0 ]; then
    echo "FAIL: $errors validation error(s)"
    exit 1
fi

# Schema validation (if configured).
# validate-output-schema.sh expects CWD to be the iteration directory
# (parent of output/). The harness may set CWD elsewhere, so cd first.
if [[ -n "${FULLSEND_OUTPUT_SCHEMA:-}" ]]; then
    SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
    _iter_dir="$(cd "$(dirname "${OUTPUT_DIR}")" && pwd)"
    (cd "${_iter_dir}" && "${SCRIPT_DIR}/validate-output-schema.sh")
fi

echo "PASS: output validated"
