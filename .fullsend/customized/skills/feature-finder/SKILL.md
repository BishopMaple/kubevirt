---
name: feature-finder
description: Discover code entry points from Jira data when no PR data exists
model: claude-opus-4-6
---

# Feature Finder Skill

**Phase:** Pre-Processing
**User-Invocable:** false

## Purpose

Discover entry points from Jira data when no PR data exists. Extracts potential symbols and code locations from Jira ticket content for LSP validation.

## When to Use

Invoked by the **regression-analyzer** subagent when:
- No PRs are available for the ticket
- Additional entry points are needed beyond PR-discovered symbols
- Feature candidates from Jira need to be mapped to code locations

## Tools Required

- Grep
- Glob
- Read
- LSP (workspaceSymbol, documentSymbol)

## Input

```yaml
jira_data:
  summary: "Add platform support for node scheduling"
  description: "Enable resources to be scheduled on specific nodes..."
  components: [handler, labeller]
  labels: [platform, scheduling]
  acceptance_criteria:
    - Resources can be scheduled on target nodes
    - Node labels correctly identify platform
  feature_candidates:
    explicit_mentions: [Resource, NodeLabeller, Platform]
    component_hints:
      # package_path values come from components.yaml — actual paths vary by project
      - component: handler
        package_path: internal/handler/
      - component: labeller
        package_path: internal/handler/labeller/
    integration_points: [node-scheduling, platform-detection]
```

## What to Look For

From the Jira data, extract:

1. **Feature name and related terminology**
   - Technical terms from summary and description
   - Capitalized terms (likely type/function names)

2. **API types and fields mentioned**
   - Resource, Instance, DataObject, ObjectSpec
   - NodeLabeller, SchedulingPolicy, etc.

3. **Component names that map to packages**
   - controller, handler, api
   - storage, network, operation, etc.

4. **Function/action names from acceptance criteria**
   - "schedule" → Schedule*, Scheduling*
   - "process" → Process*, Processing*
   - "attach" → Attach*, Connect*

## Component-to-Package Mapping

**Reference:** Read `{project_context.config_dir}/components.yaml` for the complete mapping.

## Discovery Method

### Phase 1: Keyword Extraction

Extract keywords from Jira data:
1. **From Summary:** Technical terms, API types, feature names
2. **From Components:** Map to package paths per table above
3. **From Labels:** Often contain feature names (ARM, multiarch, etc.)
4. **From Acceptance Criteria:** Action verbs, type names
5. **From Description:** Capitalized terms, quoted identifiers

### Phase 2: Symbol Search

For each extracted keyword:

1. **Use Grep for text-based discovery:**
   ```
   # Use repo path from `repositories.yaml` and package paths from `components.yaml`
   Grep pattern="func.*NodeLabeller" path="<repo_path>/<component_package_path>"
   Grep pattern="type.*Platform" path="<repo_path>/"
   ```

2. **Use LSP workspaceSymbol for semantic search:**
   ```
   LSP operation="workspaceSymbol" query="NodeLabeller" filePath="<repo_path>/" line=1 character=1
   ```

### Phase 3: Package Exploration

For each component_hint:

1. **Glob the package for main files:**
   ```
   # Use repo path from `repositories.yaml` and package path from component_hints
   Glob pattern="<repo_path>/<component_package_path>/*.go"
   ```

2. **Use LSP documentSymbol to list exported functions:**
   ```
   LSP operation="documentSymbol" filePath="<repo_path>/<component_package_path>/labeller.go" line=1 character=1
   ```

3. **Filter for exported symbols (uppercase first letter in Go)**

## Output Format

```yaml
# Note: file paths below are illustrative — actual paths depend on project layout
discovered_entry_points:
  - name: ReconcileNode
    file: internal/handler/labeller/labeller.go
    line: 45
    character: 6
    source: component_hint
    discovery_method: documentSymbol
    original_keyword: labeller
  - name: IsPlatform
    file: internal/handler/labeller/util.go
    line: 28
    character: 6
    source: explicit_mention
    discovery_method: grep
    original_keyword: Platform
  - name: GetPlatformType
    file: internal/handler/labeller/labeller.go
    line: 120
    character: 6
    source: acceptance_criteria
    discovery_method: workspaceSymbol
    original_keyword: platform
  - ...

keywords_searched:
  - keyword: NodeLabeller
    found: true
    matches: 3
  - keyword: Platform
    found: true
    matches: 5
  - keyword: scheduling
    found: false
    matches: 0
  - ...

packages_explored:
  - package: internal/handler/labeller/
    files_found: 4
    exported_symbols: 12
  - ...

summary:
  total_keywords: 8
  keywords_found: 6
  total_entry_points: 15
  packages_explored: 2
```

## Integration with lsp-tracer

The output from this skill feeds directly into **lsp-tracer** (K4):

```yaml
# feature-finder output becomes lsp-tracer input
# Repo path from `repositories.yaml` in `project_context.config_dir`
symbols_to_trace:
  - name: ReconcileNode
    file: <repo_path>/internal/handler/labeller/labeller.go
    line: 45
    character: 6
  - name: IsPlatform
    file: <repo_path>/internal/handler/labeller/util.go
    line: 28
    character: 6
  - ...
```

## Example Usage

**Input Jira for EPIC-494 (no PRs):**
- Summary: "Platform scheduling support"
- Components: [handler]
- Labels: [platform, scheduling]

**Discovery Process:**
1. Extract keywords: platform, scheduling, handler
2. Map handler → package path from `components.yaml` (e.g., `internal/handler/labeller/`)
3. Grep for "Platform" in handler package → find IsPlatform, GetPlatformType
4. Glob handler package `*.go` → find main files
5. documentSymbol on each file → list exported functions
6. Return discovered_entry_points for lsp-tracer

**Result:** Even without PRs, we discover entry points like ReconcileNode, IsPlatform, GetPlatformType for call graph analysis.
