#!/usr/bin/env bash
# pre-qualityflow.sh — Validate PR context and extract Jira ticket.
#
# Runs on the RUNNER (not in sandbox). Has access to GitHub secrets
# and runner environment.
#
# Triggered after the fix loop completes (review bot approves PR)
# or by /fs-qualityflow command on a PR.
#
# Extracts JIRA_TICKET from:
#   1. JIRA_TICKET env var (set by workflow or local --env-file)
#   2. COMMENT_BODY (e.g., "/fs-qualityflow CNV-12345")
#   3. PR title (e.g., "[CNV-12345] Fix disk offline")
#   4. PR body
#
# Required env vars:
#   PR_NUMBER       — PR to add tests to
#   REPO_FULL_NAME  — owner/repo
#   GH_TOKEN        — GitHub token
#   JIRA_BASE_URL   — Jira instance URL
#   JIRA_API_TOKEN  — Jira API token
#   JIRA_USER_EMAIL — Jira user email
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/lib/common.sh"

# --- Extract Jira ticket ---

# Source 1: already set in env (e.g., from workflow or --env-file).
if [[ -z "${JIRA_TICKET:-}" && -n "${COMMENT_BODY:-}" ]]; then
  JIRA_TICKET=$(printf '%s\n' "${COMMENT_BODY}" | grep -oE '[A-Z][A-Z0-9]+-[0-9]+' | head -1 || true)
  if [[ -n "${JIRA_TICKET}" ]]; then
    echo "JIRA_TICKET=${JIRA_TICKET}" >> "${GITHUB_ENV:-/dev/null}"
    echo "::notice::Extracted Jira ticket from command: ${JIRA_TICKET}"
  fi
fi

# Source 2: extract from PR title/body.
if [[ -z "${JIRA_TICKET:-}" && -n "${PR_NUMBER:-}" && -n "${REPO_FULL_NAME:-}" && -n "${GH_TOKEN:-}" ]]; then
  JIRA_TICKET=$(extract_jira_from_pr "${PR_NUMBER}" "${REPO_FULL_NAME}")
  if [[ -n "${JIRA_TICKET}" ]]; then
    echo "JIRA_TICKET=${JIRA_TICKET}" >> "${GITHUB_ENV:-/dev/null}"
    echo "::notice::Extracted Jira ticket from PR #${PR_NUMBER}: ${JIRA_TICKET}"
  fi
fi

if [[ -z "${JIRA_TICKET:-}" ]]; then
  echo "::error::No Jira ticket ID found in command, PR title, or PR body"
  echo "::error::Usage: /fs-qualityflow CNV-12345 (or include Jira ID in PR title)"
  exit 1
fi

echo "::notice::QualityFlow: ${JIRA_TICKET} on PR #${PR_NUMBER:-unknown}"

# --- Validate environment ---

errors=0
require_pr_env || errors=$((errors + $?))
require_env JIRA_TICKET JIRA_BASE_URL JIRA_API_TOKEN JIRA_USER_EMAIL || errors=$((errors + $?))
require_jira_format || errors=$((errors + $?))
require_config || errors=$((errors + $?))

# Safety: refuse to run on default branch.
if [[ -n "${TARGET_BRANCH:-}" ]]; then
  if [[ "${TARGET_BRANCH}" =~ ^(main|master)$ ]]; then
    echo "::error::Cannot run QualityFlow on the default branch (${TARGET_BRANCH})"
    errors=$((errors + 1))
  fi
fi

if [[ "${errors}" -gt 0 ]]; then
  echo "::error::Pre-script failed with ${errors} error(s). Aborting."
  exit 1
fi

# --- Record PRE_AGENT_HEAD ---

if [[ -n "${PR_CHECKOUT_PATH:-}" && -d "${PR_CHECKOUT_PATH}" ]]; then
  PRE_AGENT_HEAD=$(record_pre_agent_head "${PR_CHECKOUT_PATH}")
  echo "::notice::PRE_AGENT_HEAD=${PRE_AGENT_HEAD}"
fi

echo "Input validation passed: ${JIRA_TICKET} on PR #${PR_NUMBER}"
