---
name: project-resolver
description: Resolve Jira ID to project configuration and load project context
---

# Project Resolver Skill

**Phase:** Pre-Processing (Step 0)
**User-Invocable:** false

## Purpose

Central config loader for QualityFlow's multi-project architecture. Every command
invokes this skill as Step 0 to resolve the Jira ID to a project and load its
configuration.

## When to Use

Invoked as the **first step** of every command (`stp-builder`, `std-builder`,
`generate-go-tests`, `generate-python-tests`) before any other processing.

## Tools Required

- Read
- Bash (gh CLI for repo_files fetch)

## Config Resolution

QualityFlow config is loaded from the `QF_CONFIG_DIR` environment variable.
If not set, it defaults to `config/` relative to the current working directory.

```
config_root = $QF_CONFIG_DIR or "config"
```

All config file reads below use `{config_root}/` as the base path.

## Input

```yaml
jira_input: "MYPROJ-12345"  # or "https://your-jira.atlassian.net/browse/MYPROJ-12345"
```

## Workflow

### Step 1: Parse Jira ID

Extract the Jira ID from the input. Handle both formats:
- Direct ID: `MYPROJ-12345` → extract prefix `MYPROJ`, ID `MYPROJ-12345`
- URL: `https://your-jira.atlassian.net/browse/MYPROJ-12345` → extract prefix `MYPROJ`, ID `MYPROJ-12345`

The prefix is the text before the first hyphen in the Jira key.

### Step 2: Read Routing Configuration

Read `{config_root}/routing.yaml`.

Extract the `routes` array and `default_project` value.

### Step 3: Resolve Project

Match the extracted prefix against `routes[].prefix`:

```
For each route in routes:
  if route.prefix == extracted_prefix:
    project_id = route.project
    break
```

If no match found:
- If `default_project` is not null: use `default_project`
- If `default_project` is null: **FAIL** with error:
  ```
  Unknown Jira prefix "{prefix}". No project configured for this prefix.
  Known prefixes: {list of configured prefixes}
  To add a new project, create {config_root}/projects/{name}/ and add a route in {config_root}/routing.yaml.
  ```

### Step 4: Validate Project Directory

Check that `{config_root}/projects/{project_id}/` exists and contains the required files.

Read `{config_root}/_schema.yaml` to get the `required_files` list.

For each required file, verify it exists at `{config_root}/projects/{project_id}/{file}`.

If any required file is missing: **FAIL** with error:
```
Project "{project_id}" is missing required config file: {file}
Expected at: {config_root}/projects/{project_id}/{file}
```

### Step 5: Load Defaults

Read `{config_root}/_defaults.yaml` and extract the `feature_toggles` defaults.

### Step 6: Load Project Config

Read `{config_root}/projects/{project_id}/project.yaml` and extract:
- `project_id`
- `display_name`
- `feature_toggles` (project-specific overrides)
- `stp_document.header`
- `versioning`

### Step 7: Merge Feature Toggles

Deep-merge project toggles over defaults:

```
merged_toggles = defaults.feature_toggles
for key, value in project.feature_toggles:
  merged_toggles[key] = value
```

### Step 8: Validate Toggle Consistency

Read `{config_root}/_schema.yaml` `toggle_consistency` rules.

For each rule:
- If `merged_toggles[rule.toggle]` is true, verify `{config_root}/projects/{project_id}/{rule.requires_file}` exists
- If the required file is missing: **WARN** (not fail):
  ```
  Warning: {rule.toggle} is enabled but {rule.requires_file} not found.
  ```

### Step 9: Fetch Repo Files (repo_rules)

**Guard:** Skip this step if `merged_toggles.repo_files_fetch` is false.

Read `{config_root}/projects/{project_id}/repositories.yaml` and check for a `repo_files` section.

If `repo_files` exists, fetch each declared file from its source repository:

```
repo_rules = {}

For each entry in repo_files:
  # Resolve the repo reference
  repo_ref = entry.repo  # e.g., "e2e_repo" or "design_docs_repo"
  repo_config = repositories_yaml[repo_ref]  # get org + name from the repo section

  # Fetch via gh CLI or MCP (whichever is available)
  Try:
    # Fetch via gh CLI:
    Run in Bash:
      gh api repos/{repo_config.org}/{repo_config.name}/contents/{entry.path} \
        --jq '.content' | base64 -d
    # Or if a specific branch is needed:
      gh api "repos/{repo_config.org}/{repo_config.name}/contents/{entry.path}?ref={branch}" \
        --jq '.content' | base64 -d

    repo_rules[entry_name] = <decoded content>
    Log: "Fetched {entry_name} from {repo_config.org}/{repo_config.name}/{entry.path}"

  On failure:
    If entry.fallback is not null:
      # Read local fallback from config_dir
      fallback_path = "{config_dir}/{entry.fallback}"
      content = Read(fallback_path)
      repo_rules[entry_name] = content
      Log: "Fallback: loaded {entry_name} from {fallback_path}"
    Else:
      repo_rules[entry_name] = null
      Log: "Warning: Could not fetch {entry_name}, no fallback configured"
```

**Parallel fetching:** All repo_files entries are independent — fetch them in parallel
(multiple `gh api` calls) for performance.

**Result:** `repo_rules` dictionary with raw file contents keyed by logical name.

### Step 10: Return Project Context

Return the resolved context:

```yaml
project_context:
  project_id: "{project_id}"
  display_name: "{display_name}"
  jira_id: "{JIRA_ID}"
  config_dir: "{config_root}/projects/{project_id}"
  feature_toggles:
    polarion: true/false
    unit_tests: true/false
    go_tests: true/false
    python_tests: true/false
    stp_generation: true/false
    std_generation: true/false
    lsp_analysis: true/false
    pii_sanitization: true/false
    repo_files_fetch: true/false
  stp_header: "{stp_document.header}"
  versioning:
    product_name: "{product_name}"
    platform_name: "{platform_name}"
    current_version: "{current_version}"
  repo_rules:
    agents_rules: "{raw content of AGENTS.md or null}"
    std_format: "{raw content of SOFTWARE_TEST_DESCRIPTION.md or null}"
    stp_template: "{raw content of STP template or null}"
    stp_guide: "{raw content of STP guide or null}"
    testing_tiers: "{raw content of testing tiers guide or null}"
```

## Output Format (Example)

```yaml
project_context:
  project_id: "example"
  display_name: "My Project"
  jira_id: "MYPROJ-12345"
  config_dir: "config/projects/example"
  feature_toggles:
    polarion: false
    unit_tests: false
    go_tests: true
    python_tests: true
    stp_generation: true
    std_generation: true
    lsp_analysis: true
    pii_sanitization: true
    repo_files_fetch: true
  stp_header: "{stp_document.header}"
  versioning:
    product_name: "My Product"
    platform_name: "Kubernetes"
    current_version: "1.0"
  repo_rules:
    agents_rules: "# AI Review and Development Standards\n..."  # or null if not configured
    std_format: "# Software Test Description\n..."               # or null
    stp_template: "# Test Plan Template\n..."                    # or null
    stp_guide: null
    testing_tiers: null
```

### repo_rules Usage by Skills

| Skill | Uses from repo_rules |
|:------|:--------------------|
| template-engine | `stp_template` — official STP template structure |
| stp-generator | `stp_template`, `stp_guide` — template + guide for generation |
| stp-reviewer | `stp_template`, `stp_guide`, `testing_tiers` — review against official docs |
| std-generator | `std_format`, `agents_rules` — STD format rules + coding standards |
| python-stub-generator | `std_format`, `agents_rules` — PSE format + stub conventions |
| python-test-generator | `agents_rules` — fixture, marker, and code pattern rules |
| go-stub-generator | `agents_rules` — coding standards |
| go-test-generator | `agents_rules` — coding standards |
| std-reviewer | `std_format`, `agents_rules` — validate stubs against repo rules |

## Error Handling

**Unknown prefix:**
- Error: "Unknown Jira prefix. No project configured."
- Action: List known prefixes and suggest adding a route
- Exit command

**Missing project directory:**
- Error: "Project config directory not found"
- Action: Suggest creating the directory structure
- Exit command

**Missing required config file:**
- Error: "Required config file missing"
- Action: List the missing file and expected location
- Exit command

**Malformed YAML:**
- Error: "Cannot parse config file"
- Action: Show the file path and suggest checking YAML syntax
- Exit command

## Usage by Commands

Each command uses project_context differently:

| Command | Uses from project_context |
|:--------|:--------------------------|
| stp-builder | Passes to stp-orchestrator for all subagents |
| std-builder | Checks go_tests/python_tests to decide which stubs to generate |
| generate-go-tests | Checks go_tests; blocks if false |
| generate-python-tests | Checks python_tests; blocks if false |

## Usage by Agents

Each agent reads additional config files on-demand from `config_dir`:

| Agent | Reads from config_dir |
|:------|:----------------------|
| jira-collector | `jira.yaml`, `components.yaml` |
| github-pr-fetcher | `repositories.yaml` (optional) |
| regression-analyzer | `repositories.yaml`, `components.yaml` |
| stp-generator | `project.yaml`, `environment.yaml`, `go.yaml`, `python.yaml` |
| document-formatter | `pii_exceptions.yaml` |
| ticket-context-analyzer | `repositories.yaml` |

## Feature Toggle Notes

The `unit_tests` toggle is informational only. It signals whether unit tests are in scope for a project configuration, but no QualityFlow command or skill gates on it. All other toggles (`polarion`, `go_tests`, `python_tests`, `stp_generation`, `std_generation`, `lsp_analysis`, `pii_sanitization`) are actively gated by commands, agents, or skills.
