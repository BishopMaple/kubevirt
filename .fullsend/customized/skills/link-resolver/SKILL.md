---
name: link-resolver
description: Build a dependency graph from Jira issue links and identify key dependencies
model: claude-opus-4-6
---

# Link Resolver Skill

**Phase:** Pre-Processing
**User-Invocable:** false

## Purpose

Build a dependency graph from Jira issue links, categorizing relationships and identifying key dependencies.

## When to Use

Invoked by the **jira-collector** subagent after collecting issue links from the main issue.

## Input

```yaml
main_issue_key: MYPROJ-12345
issue_links:
  - outward_issue:
      key: MYPROJ-11111
      summary: <summary>
    link_type:
      name: Blocks
      outward: blocks
      inward: is blocked by
  - inward_issue:
      key: MYPROJ-22222
      summary: <summary>
    link_type:
      name: Relates
      outward: relates to
      inward: relates to
  - ...
subtasks:
  - key: MYPROJ-12345-1
    summary: <summary>
    status: <status>
  - ...
```

## Output Format

```yaml
dependency_graph:
  main_issue: MYPROJ-12345

  outward_links:
    - target: MYPROJ-11111
      relationship: blocks
      link_type: Blocks
      summary: <summary>
    - ...

  inward_links:
    - source: MYPROJ-22222
      relationship: is blocked by
      link_type: Blocks
      summary: <summary>
    - ...

  categorized_links:
    blocking:
      - MYPROJ-11111  # Issues this blocks
    blocked_by:
      - MYPROJ-22222  # Issues blocking this
    implements:
      - MYPROJ-33333  # Features/epics implemented
    implemented_by:
      - MYPROJ-44444  # Stories implementing this
    relates_to:
      - MYPROJ-55555  # Related issues
    parent:
      - MYPROJ-99000  # Parent epic/feature
    children:
      - MYPROJ-12345-1  # Subtasks
    depends_on:
      - MYPROJ-66666  # Issues this depends on
    depended_on_by:
      - MYPROJ-77777  # Issues depending on this
    triggers:
      - MYPROJ-88888  # Issues this triggers
    triggered_by:
      - MYPROJ-99999  # Issues triggering this
    split_to:
      - MYPROJ-10101  # Issues split from this
    split_from:
      - MYPROJ-20202  # Issues this was split from
    causes:
      - MYPROJ-30303  # Issues caused by this
    caused_by:
      - MYPROJ-40404  # Issues causing this
    problem_of:
      - MYPROJ-50505  # Problems of this issue
    incident_of:
      - MYPROJ-60606  # Incidents of this issue

  hierarchy:
    level: story  # epic | feature | story | task | subtask
    parent: MYPROJ-99000
    children:
      - MYPROJ-12345-1
      - MYPROJ-12345-2

  all_linked_issues:
    - key: MYPROJ-11111
      direction: outward
      type: Blocks
      pr_urls:
        - https://github.com/your-org/your-repo/pull/1234
    - key: MYPROJ-22222
      direction: inward
      type: Blocks
      pr_urls: []
    - ...

  aggregated_pr_urls:
    - url: https://github.com/your-org/your-repo/pull/1234
      source_issue: MYPROJ-11111
      source_type: custom_field  # custom_field | comment
      is_main_issue: false
    - ...
```

## Link Type Classification

### Standard Jira Link Types

| Link Type | Outward Description | Inward Description | Category |
|:----------|:-------------------|:-------------------|:---------|
| Blocks | blocks | is blocked by | blocking |
| Cloners | clones | is cloned by | clones |
| Duplicate | duplicates | is duplicated by | duplicates |
| Relates | relates to | relates to | relates |
| Parent/Child | is parent of | is child of | hierarchy |
| Epic-Story Link | has Epic | is Epic of | hierarchy |
| Implements | implements | is implemented by | implements |
| Dependency | depends on | is depended on by | dependency |
| Triggers | triggers | is triggered by | triggers |
| Split | split to | split from | split |
| Causes | causes | is caused by | causes |
| Problem/Incident | is problem of | is incident of | problem |

### Link Direction Rules

**Outward Links (Process These):**
- The main issue is the SOURCE of the link
- Example: "MYPROJ-12345 **blocks** MYPROJ-11111"
- These indicate downstream dependencies

**Inward Links (Collect for Reference):**
- The main issue is the TARGET of the link
- Example: "MYPROJ-22222 **blocks** MYPROJ-12345"
- These indicate upstream dependencies

## Hierarchy Detection

Determine the issue's position in the hierarchy:

1. **Epic/Feature**: Has children, no parent of same type
2. **Story**: Has parent epic/feature, may have subtasks
3. **Task**: Standalone work item
4. **Subtask**: Has parent task/story

## Critical Dependency Identification

Mark links as "critical" if:
- Link type is "Blocks" (blocking/blocked_by)
- Link is to a Feature or Epic (parent hierarchy)
- Link is marked as "Must Have" in requirements

## Example Processing

Input:
```yaml
main_issue_key: MYPROJ-12345
issue_links:
  - outward_issue:
      key: MYPROJ-11111
      summary: Storage refactoring
    link_type:
      name: Blocks
      outward: blocks
  - inward_issue:
      key: MYPROJ-22222
      summary: Migration improvements
    link_type:
      name: Relates
      outward: relates to
  - outward_issue:
      key: MYPROJ-99000
      summary: Config Update Feature
    link_type:
      name: Implements
      outward: implements
```

Output:
```yaml
dependency_graph:
  main_issue: MYPROJ-12345

  categorized_links:
    blocking:
      - MYPROJ-11111
    blocked_by: []
    implements:
      - MYPROJ-99000
    relates_to:
      - MYPROJ-22222
    parent:
      - MYPROJ-99000
    children: []

  critical_dependencies:
    - key: MYPROJ-11111
      type: Blocks
      reason: This issue blocks downstream work
    - key: MYPROJ-99000
      type: Implements
      reason: Parent feature dependency
```

## Usage Notes

1. **1 Level Depth Only**: Only process direct links from the main issue
2. **Do NOT Recurse**: Do not follow links from linked issues
3. **Collect Both Directions**: Track both outward and inward for context
4. **Prioritize Outward**: Outward links are primary for PR collection
