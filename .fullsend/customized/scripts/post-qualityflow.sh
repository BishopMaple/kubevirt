#!/usr/bin/env bash
# post-qualityflow.sh — Push test files to PR and post summary comment.
#
# Runs on the host after sandbox cleanup. Working directory is the fullsend
# run output directory (e.g., /tmp/fullsend/agent-qualityflow-<id>/).
#
# Follows the fix agent's zero-trust pattern:
#   1. Scope diff to agent's commits only (PRE_AGENT_HEAD..HEAD)
#   2. Protected-path check (block .github/, .claude/, etc.)
#   3. Gitleaks secret scan on agent commits
#   4. Push test files to PR branch
#   5. Post summary comment on PR
#
# Required env vars:
#   PR_NUMBER         — PR number
#   REPO_FULL_NAME    — owner/repo
#   GH_TOKEN          — GitHub token (read)
#   PUSH_TOKEN        — GitHub token (write, for pushing)
#   PRE_AGENT_HEAD    — SHA before agent ran (for diff scoping)
#
# The agent writes test files to the PR repo checkout and commits them
# locally. This script validates and pushes those commits.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/lib/common.sh"

: "${PR_NUMBER:?PR_NUMBER is required}"
: "${REPO_FULL_NAME:?REPO_FULL_NAME is required}"
: "${GH_TOKEN:?GH_TOKEN is required}"
: "${PUSH_TOKEN:?PUSH_TOKEN is required}"
echo "::add-mask::${GH_TOKEN}"
echo "::add-mask::${PUSH_TOKEN}"

# --- Locate results ---

RESULT_FILE=""
for dir in iteration-*/output; do
  if [[ -f "${dir}/summary.json" ]]; then
    RESULT_FILE="${dir}/summary.json"
  fi
done

if [[ -z "${RESULT_FILE}" ]]; then
  echo "ERROR: summary.json not found in any iteration output directory"
  exit 1
fi

OUTPUT_DIR="$(dirname "${RESULT_FILE}")"
echo "Reading QualityFlow result from: ${RESULT_FILE}"

if ! jq empty "${RESULT_FILE}" 2>/dev/null; then
  echo "ERROR: ${RESULT_FILE} is not valid JSON"
  exit 1
fi

STATUS=$(jq -r '.status' "${RESULT_FILE}")
JIRA_ID=$(jq -r '.jira_id // "unknown"' "${RESULT_FILE}")

echo "Status: ${STATUS}"
echo "Jira ID: ${JIRA_ID}"
echo "PR: ${REPO_FULL_NAME}#${PR_NUMBER}"

# --- Secret scan on output files (before posting anything) ---

scan_output_secrets "${OUTPUT_DIR}" || exit 1

# --- Push test files to PR branch ---

NO_PUSH=false
PR_CHECKOUT_PATH="${PR_CHECKOUT_PATH:-}"
PRE_AGENT_HEAD="${PRE_AGENT_HEAD:-}"

if [[ -z "${PR_CHECKOUT_PATH}" || ! -d "${PR_CHECKOUT_PATH}" ]]; then
  echo "::warning::PR_CHECKOUT_PATH not set or missing — skipping push"
  NO_PUSH=true
fi

if [[ -z "${PRE_AGENT_HEAD}" ]]; then
  echo "::warning::PRE_AGENT_HEAD not set — skipping push"
  NO_PUSH=true
fi

CHANGED_FILES=""
PUSH_RESULT="skipped"

if [[ "${NO_PUSH}" != "true" ]]; then
  BRANCH=$(git -C "${PR_CHECKOUT_PATH}" branch --show-current 2>/dev/null || true)

  if [[ -z "${BRANCH}" ]]; then
    echo "::warning::Detached HEAD — skipping push"
    NO_PUSH=true
  elif [[ "${BRANCH}" =~ ^(main|master)$ ]]; then
    echo "::warning::On default branch '${BRANCH}' — skipping push"
    NO_PUSH=true
  fi
fi

if [[ "${NO_PUSH}" != "true" ]]; then
  CHANGED_FILES=$(git -C "${PR_CHECKOUT_PATH}" diff --name-only "${PRE_AGENT_HEAD}..HEAD" 2>/dev/null || true)

  if [[ -z "${CHANGED_FILES}" ]]; then
    echo "::notice::No commits from agent — nothing to push"
    NO_PUSH=true
    PUSH_RESULT="no_changes"
  fi
fi

if [[ "${NO_PUSH}" != "true" ]]; then
  echo "Agent changed files:"
  printf '%s\n' "${CHANGED_FILES}" | sed 's/^/  /'

  # Protected-path check.
  if ! check_protected_paths "${PR_CHECKOUT_PATH}" "${PRE_AGENT_HEAD}"; then
    echo "::error::Protected paths modified — refusing to push"
    NO_PUSH=true
    PUSH_RESULT="blocked_protected_paths"
  fi
fi

if [[ "${NO_PUSH}" != "true" ]]; then
  # Gitleaks on agent commits.
  if ! scan_agent_commits "${PR_CHECKOUT_PATH}" "${PRE_AGENT_HEAD}"; then
    echo "::error::Secrets found in agent commits — refusing to push"
    NO_PUSH=true
    PUSH_RESULT="blocked_secrets"
  fi
fi

if [[ "${NO_PUSH}" != "true" ]]; then
  echo "Pushing to ${BRANCH}..."
  git -C "${PR_CHECKOUT_PATH}" remote set-url origin \
    "https://x-access-token:${PUSH_TOKEN}@github.com/${REPO_FULL_NAME}.git"
  if git -C "${PR_CHECKOUT_PATH}" push -u origin -- "${BRANCH}" 2>&1; then
    echo "::notice::Pushed test files to PR #${PR_NUMBER}"
    PUSH_RESULT="success"
  else
    echo "::error::Push failed"
    PUSH_RESULT="push_failed"
  fi
fi

# --- Build PR comment ---

COMMENT="## QualityFlow: ${JIRA_ID}"$'\n\n'

if [[ "${STATUS}" == "success" ]]; then
  STP_VERDICT=$(jq -r '.stp.review_verdict // "N/A"' "${RESULT_FILE}")
  STD_VERDICT=$(jq -r '.std.review_verdict // "N/A"' "${RESULT_FILE}")
  TOTAL=$(jq -r '.scenarios.total // 0' "${RESULT_FILE}")
  REQS=$(jq -r '.requirements_traced // 0' "${RESULT_FILE}")
  GO_FILES=$(jq -r '.test_files_committed | map(select(endswith(".go"))) | length' "${RESULT_FILE}" 2>/dev/null || echo "0")
  PY_FILES=$(jq -r '.test_files_committed | map(select(endswith(".py"))) | length' "${RESULT_FILE}" 2>/dev/null || echo "0")

  COMMENT+="| Metric | Value |"$'\n'
  COMMENT+="|--------|-------|"$'\n'
  COMMENT+="| STP Review | ${STP_VERDICT} |"$'\n'
  COMMENT+="| STD Review | ${STD_VERDICT} |"$'\n'
  COMMENT+="| Requirements Traced | ${REQS} |"$'\n'
  COMMENT+="| Test Scenarios | ${TOTAL} |"$'\n'
  COMMENT+="| Push Status | ${PUSH_RESULT} |"$'\n\n'

  # List committed test files.
  COMMITTED=$(jq -r '.test_files_committed[]? // empty' "${RESULT_FILE}" 2>/dev/null || true)
  if [[ -n "${COMMITTED}" ]]; then
    COMMENT+="### Test Files"$'\n\n'
    COMMENT+='```'$'\n'
    COMMENT+="${COMMITTED}"$'\n'
    COMMENT+='```'$'\n\n'
  fi

elif [[ "${STATUS}" == "partial" ]]; then
  COMMENT+="**Partial result** — some test artifacts were generated but test code may be incomplete."$'\n\n'
  COMMENT+="Push status: ${PUSH_RESULT}"$'\n\n'

elif [[ "${STATUS}" == "error" ]]; then
  REASON=$(jq -r '.reason // "unknown error"' "${RESULT_FILE}")
  COMMENT+="**Error:** ${REASON}"$'\n\n'
fi

COMMENT+="---"$'\n'
COMMENT+="_Generated by [QualityFlow](https://github.com/fullsend-ai/fullsend) test planning agent_"

# --- Post PR comment ---

echo "Posting PR comment..."
printf '%s' "${COMMENT}" | fullsend post-comment \
  --repo "${REPO_FULL_NAME}" \
  --number "${PR_NUMBER}" \
  --marker "<!-- fullsend:qualityflow-agent -->" \
  --token "${GH_TOKEN}" \
  --result - 2>/dev/null || {
  printf '%s' "${COMMENT}" | gh pr comment "${PR_NUMBER}" \
    --repo "${REPO_FULL_NAME}" \
    --body-file -
}

echo "Post-qualityflow complete. Push: ${PUSH_RESULT}"
