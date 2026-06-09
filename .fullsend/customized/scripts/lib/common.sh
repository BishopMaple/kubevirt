#!/usr/bin/env bash
# common.sh — Shared functions for QualityFlow pre/post scripts.
#
# Source this file at the top of any QF script:
#   SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
#   source "${SCRIPT_DIR}/lib/common.sh"

# require_env — Fail if any listed env var is empty or unset.
# Usage: require_env JIRA_TICKET JIRA_BASE_URL
require_env() {
  local errors=0
  for var in "$@"; do
    if [[ -z "${!var:-}" ]]; then
      echo "::error::${var} is required but not set"
      errors=$((errors + 1))
    fi
  done
  if [[ "${errors}" -gt 0 ]]; then
    return 1
  fi
}

# require_jira_format — Validate JIRA_TICKET matches PROJECT-NUMBER.
require_jira_format() {
  local ticket="${1:-${JIRA_TICKET:-}}"
  if [[ ! "${ticket}" =~ ^[A-Z][A-Z0-9]+-[0-9]+$ ]]; then
    echo "::error::JIRA_TICKET must match PROJECT-NUMBER format, got: '${ticket}'"
    return 1
  fi
}

# require_config — Validate the QF config directory exists.
# On the runner (pre-script), check relative to FULLSEND_DIR or SCRIPT_DIR.
# In the sandbox (agent), config is at /tmp/workspace/agent-input.
require_config() {
  local config_dir="${QF_CONFIG_DIR:-}"
  if [[ -z "${config_dir}" ]]; then
    if [[ -n "${FULLSEND_DIR:-}" ]]; then
      config_dir="${FULLSEND_DIR}/config"
    else
      local script_dir
      script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
      config_dir="${script_dir}/config"
    fi
  fi
  if [[ ! -d "${config_dir}" ]]; then
    echo "::error::Config directory not found: ${config_dir}"
    return 1
  fi
  if [[ ! -f "${config_dir}/routing.yaml" ]]; then
    echo "::error::routing.yaml not found in ${config_dir}"
    return 1
  fi
  echo "OK: config directory at ${config_dir}"
}

# scan_output_secrets — Run gitleaks on the output directory.
# Uses gitleaks in --no-git mode (output is generated files, not a repo).
# Non-fatal if gitleaks is not installed (warning only).
scan_output_secrets() {
  local output_dir="$1"
  if [[ ! -d "${output_dir}" ]]; then
    echo "::warning::Output directory not found for secret scan: ${output_dir}"
    return 0
  fi

  if command -v gitleaks >/dev/null 2>&1; then
    echo "Running gitleaks scan on ${output_dir}..."
    if gitleaks detect --source="${output_dir}" --no-git 2>&1; then
      echo "OK: no secrets detected"
    else
      echo "::error::gitleaks detected potential secrets in output"
      return 1
    fi
  else
    echo "::warning::gitleaks not installed — skipping secret scan"
  fi
}

# --- PR helpers (for post-fix-style QualityFlow) ---

# require_pr_env — Validate PR context variables.
require_pr_env() {
  require_env PR_NUMBER REPO_FULL_NAME GH_TOKEN
}

# extract_jira_from_pr — Extract Jira ticket ID from PR title or body.
# Usage: JIRA_TICKET=$(extract_jira_from_pr "$PR_NUMBER" "$REPO_FULL_NAME")
extract_jira_from_pr() {
  local pr_number="$1"
  local repo="$2"
  local ticket=""

  local title
  title=$(gh api "repos/${repo}/pulls/${pr_number}" --jq '.title' 2>/dev/null || true)
  if [[ -n "${title}" ]]; then
    ticket=$(printf '%s\n' "${title}" | grep -oE '[A-Z][A-Z0-9]+-[0-9]+' | head -1 || true)
  fi

  if [[ -z "${ticket}" ]]; then
    local body
    body=$(gh api "repos/${repo}/pulls/${pr_number}" --jq '.body' 2>/dev/null || true)
    if [[ -n "${body}" ]]; then
      ticket=$(printf '%s\n' "${body}" | grep -oE '[A-Z][A-Z0-9]+-[0-9]+' | head -1 || true)
    fi
  fi

  echo "${ticket}"
}

# record_pre_agent_head — Capture HEAD SHA before agent runs (for diff scoping).
# Usage: PRE_AGENT_HEAD=$(record_pre_agent_head "$PR_CHECKOUT_PATH")
record_pre_agent_head() {
  local repo_path="$1"
  local head
  head=$(git -C "${repo_path}" rev-parse HEAD)
  echo "PRE_AGENT_HEAD=${head}" >> "${GITHUB_ENV:-/dev/null}"
  echo "${head}"
}

# check_protected_paths — Fail if agent touched protected files.
# Usage: check_protected_paths "$PR_CHECKOUT_PATH" "$PRE_AGENT_HEAD"
check_protected_paths() {
  local repo_path="$1"
  local base_sha="$2"
  local blocked=0

  local protected=(".github/" ".claude/" ".fullsend/" "agents/" "harness/"
                    "plugins/" "policies/" "api-servers/" "CODEOWNERS"
                    ".pre-commit-config.yaml" ".gitattributes")

  local changed_files
  changed_files=$(git -C "${repo_path}" diff --name-only "${base_sha}..HEAD" 2>/dev/null || true)
  if [[ -z "${changed_files}" ]]; then
    return 0
  fi

  for pattern in "${protected[@]}"; do
    if printf '%s\n' "${changed_files}" | grep -qE "^${pattern}"; then
      echo "::error::Agent modified protected path: ${pattern}"
      blocked=$((blocked + 1))
    fi
  done

  if [[ "${blocked}" -gt 0 ]]; then
    echo "::error::${blocked} protected path(s) modified — aborting push"
    return 1
  fi
}

# scan_agent_commits — Run gitleaks on agent commits only (git-aware mode).
# Usage: scan_agent_commits "$PR_CHECKOUT_PATH" "$PRE_AGENT_HEAD"
scan_agent_commits() {
  local repo_path="$1"
  local base_sha="$2"

  if ! command -v gitleaks >/dev/null 2>&1; then
    echo "::warning::gitleaks not installed — skipping commit scan"
    return 0
  fi

  echo "Running gitleaks on agent commits (${base_sha}..HEAD)..."
  if gitleaks detect --source="${repo_path}" --log-opts="${base_sha}..HEAD" 2>&1; then
    echo "OK: no secrets in agent commits"
  else
    echo "::error::gitleaks detected secrets in agent commits — aborting push"
    return 1
  fi
}

# find_last_output_dir — Locate the most recent iteration's output directory.
# FullSend creates iteration-N/output/ directories; we want the last one.
find_last_output_dir() {
  local output_dir=""
  for dir in iteration-*/output; do
    if [[ -d "${dir}" ]]; then
      output_dir="${dir}"
    fi
  done
  if [[ -z "${output_dir}" ]]; then
    local fallback="${FULLSEND_OUTPUT_DIR:-$(pwd)/output}"
    if [[ -d "${fallback}" ]]; then
      output_dir="${fallback}"
    fi
  fi
  echo "${output_dir}"
}
