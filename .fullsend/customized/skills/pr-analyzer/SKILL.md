---
name: pr-analyzer
description: Analyze GitHub PR diffs and extract meaningful changes for STP generation
model: claude-opus-4-6
---

# PR Analyzer Skill

**Phase:** Pre-Processing
**User-Invocable:** false

## Purpose

Analyze GitHub PR diffs and extract meaningful changes for STP generation.

## When to Use

Invoked by the **github-pr-fetcher** subagent after fetching PR details, diffs, and review comments.

## Input

```yaml
pr_data:
  url: https://github.com/your-org/your-repo/pull/1234
  owner: your-repo
  repo: your-repo
  pull_number: 1234
  title: Add feature operation support
  description: |
    This PR implements feature operation functionality...
  state: merged
  author: developer
  base_branch: main
  head_branch: feature/resource-operation
  diff: |
    diff --git a/internal/controller/resource.go b/internal/controller/resource.go
    index abc123..def456 100644
    --- a/internal/controller/resource.go
    +++ b/internal/controller/resource.go
    @@ -100,6 +100,20 @@ func (c *ResourceController) Reconcile() {
    +func (c *ResourceController) HandleFeatureOperation(resource *v1.Resource) error {
    ...
  files:
    - filename: internal/controller/resource.go
      status: modified
      additions: 50
      deletions: 10
    - filename: internal/controller/operation.go
      status: added
      additions: 200
      deletions: 0
    - ...
  review_comments:
    - user: reviewer1
      body: "Consider edge case when resource is in transition"
      path: internal/controller/operation.go
      line: 45
    - ...
```

## Output Format

```yaml
analysis:
  pr_url: https://github.com/your-org/your-repo/pull/1234
  summary: Implements feature operation functionality for resources

  key_changes:
    functions:
      - name: HandleFeatureOperation
        file: internal/controller/resource.go
        action: added
        purpose: Main entry point for feature operations
      - name: ValidateFeatureChange
        file: internal/controller/operation.go
        action: added
        purpose: Validates feature changes before applying
      - ...

    types:
      - name: FeatureOperationSpec
        file: api/v1/types.go
        action: added
        fields_changed:
          - MaxInstances
          - CurrentInstances
      - ...

    apis:
      - endpoint: /resources/{name}/operation
        method: PATCH
        action: added
        purpose: Apply feature operation to resource
      - ...

    configurations:
      - name: EnableFeatureOperation
        type: feature_gate
        location: Operator CR
        default: false
      - ...

  files_by_category:
    controllers:
      - internal/controller/resource.go
      - internal/controller/operation.go
    handlers:
      - internal/handler/operation.go
    api:
      - api/v1/types.go
      - api/v1/types_swagger_generated.go
    tests:
      - tests/operation_test.go
    other:
      - ...

  review_insights:
    edge_cases:
      - "Resource state transition during operation needs handling"
      - "Consider maximum instance limit validation"
    concerns:
      - "Performance impact of frequent operations"
    suggestions:
      - "Add metrics for operation success/failure rate"

  impact_assessment:
    components_affected:
      - controller
      - handler
      - api
    features_potentially_impacted:
      - Resource lifecycle
      - State management
      - Resource quotas
    breaking_changes: false
    api_changes: true
    config_changes: true
```

## Analysis Rules

### Function Detection

Parse diff for:
- `func (receiver) FunctionName(` - Go methods
- `func FunctionName(` - Go functions
- Added/Modified/Deleted based on diff markers (+/-)

### Type Detection

Parse diff for:
- `type TypeName struct {`
- `type TypeName interface {`
- Field additions/removals within structs

### API Detection

Look for:
- Route registrations (e.g., `router.Handle`, `http.HandleFunc`)
- OpenAPI/Swagger annotations
- CRD changes (in `api/` or `config/` directories)

### Configuration Detection

Look for:
- Feature gates
- Environment variables
- ConfigMap references
- Operator CR fields

### Review Insight Extraction

From review comments, extract:
- **Edge cases**: Comments mentioning "edge case", "corner case", "what if"
- **Concerns**: Comments with "concern", "worry", "problem", "issue"
- **Suggestions**: Comments with "suggest", "should", "consider", "might want"

## File Categorization

Read `{project_context.config_dir}/components.yaml` for project-specific file categorization rules. The directory-to-category mapping varies by project — do not assume a fixed layout like `internal/controller/` or `src/handlers/`.

Common categories include: controllers, handlers, api, runtime, tests, config, cmd, and util. The actual directory paths for each category are defined in the project's `components.yaml`.

**Example paths shown in output format (e.g., `internal/controller/resource.go`) are illustrative only — actual paths are project-specific.**

## Usage Notes

1. **Focus on Behavioral Changes**: Identify what the PR changes functionally
2. **Ignore Noise**: Skip formatting-only changes, comment updates
3. **Highlight Test Implications**: Note what new tests should cover
4. **Extract Edge Cases**: Review comments often reveal test scenarios
