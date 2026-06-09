---
name: requirement-mapper
description: Map Jira requirements to testable scenarios with validation gates
---

# Requirement Mapper Skill

**Phase:** Core Processing
**User-Invocable:** false

## Purpose

Map Jira requirements to testable scenarios, applying validation gates and project scope boundaries.

## When to Use

Invoked by the **stp-generator** subagent to transform regression analysis into validated requirements.

## Input

```yaml
jira_data:
  main_issue:
    key: MYPROJ-12345
    summary: Add config update support for resources
    description: ...
    acceptance_criteria: ...
  linked_issues: [...]

regression_data:
  impacted_features:
    - feature_name: Resource Update
      relationship: Direct caller
      why_might_break: ...
    - ...
  recommended_tests:
    - requirement: Rolling update works with config changes
      test_scenario: Verify update succeeds after config change
      priority: P1
    - ...
```

## Output Format

```yaml
validated_requirements:
  - requirement_id: MYPROJ-12345  # Jira issue key — NEVER invent IDs
    requirement_summary: Rolling update completes successfully after config change
    source: regression_analysis
    evidence: UpdateResource calls ApplyResourceSpec which was modified
    validation_passed: true
    test_scenario: Verify update succeeds after config change
    priority: P1
  - requirement_id: ""  # Leave blank for subsequent rows under the same epic
    requirement_summary: Config can be updated on running resource
    source: regression_analysis
    evidence: HandleFeatureToggle is new entry point
    validation_passed: true
    test_scenario: Verify CPU addition to running resource
    priority: P0
  - ...

rejected_requirements:
  - requirement_summary: Kubernetes scheduler places resource pods correctly
    reason: Platform-level test - Kubernetes scheduler is tested by platform team
    gate_failed: Requirement Level Validation
  - requirement_summary: PVC binds to PV correctly
    reason: Platform-level test - CSI/storage tested by storage team
    gate_failed: Requirement Level Validation
  - ...

coverage_summary:
  total_from_regression: 15
  validated: 12
  rejected: 3
  functional_count: 8
  e2e_count: 4
```

## Requirement Level Validation Gate

### Step 1: Identify Testing Level

| Level | Description | Action |
|:------|:------------|:-------|
| Kubernetes Platform | Core K8s (scheduling, storage primitives, RBAC engine) | REJECT |
| Platform Infrastructure | Platform features (routes, service mesh, OAuth) | REJECT |
| Project Product | Product-specific features (resources, operations, workflows) | ACCEPT |
| Integration | Project product using platform capabilities | ACCEPT |

### Step 2: "Who Tests This?" Question

| Answer | Action |
|:-------|:-------|
| Kubernetes upstream QE | REJECT |
| Platform Infrastructure QE | REJECT |
| Storage/Network/Security platform team | REJECT |
| Project Product QE | ACCEPT |

### Step 3: Project Scope Context Check

Read `{project_context.config_dir}/project.yaml` `scope_boundaries` for in-scope and out-of-scope resources.

The following is an example of scope boundaries:

**Accept if involves:**
- Product-specific custom resources
- Product-managed workloads
- Product data objects
- Product instance types
- Product operators and controllers
- Product-specific components

**Reject if involves only:**
- Pod, Deployment, StatefulSet (raw)
- PersistentVolumeClaim (raw platform storage)
- Node, Namespace (raw)
- ConfigMap, Secret (raw, not product-specific)
- Service, Ingress, Route (raw)

### Step 4: Final Check

Read the `scope_boundaries.validation_gate` question from `{project_context.config_dir}/project.yaml`. For example: "Would removing the product make this test meaningless?"
- YES → ACCEPT
- NO → REJECT

## Requirement ID Rules

### Jira Issue Keys Only

Requirement IDs MUST be Jira issue keys (e.g., `MYPROJ-72329`). Never invent IDs
like `REQ-xxx-001`, `REQ-EPIC-001`, or any other synthetic ID format.

- Use the **epic key** for the first row under that epic
- Leave the Requirement ID **blank** for subsequent rows under the same epic
  (avoids redundant repetition of the same key)
- If a linked sub-issue has its own Jira key, use that key instead

| BAD (Invented) | GOOD (Jira Key) |
|:----------------|:-----------------|
| REQ-EPIC-001 | MYPROJ-72329 |
| REQ-CPU-001 | MYPROJ-12345 |
| REQ-OP-001 | MYPROJ-67890 |

## Requirement Quality Rules

### STP Level Requirements

Requirements must be HIGH-LEVEL capabilities:

| BAD (Too Low-Level) | GOOD (STP Level) |
|:--------------------|:-----------------|
| Create resource with 2 replicas, start it, add 2 more | Config can be updated on running resource |
| Run `kubectl get resource` and check status | Resource status is accurately reported via API |
| Create PVC, attach, write file, verify | Data persists across disk attach/detach |

### Avoid Trivial Atomic Requirements

Consolidate into feature capabilities:

| BAD (Fragmented) | GOOD (Consolidated) |
|:-----------------|:--------------------|
| Start resource, Stop resource, Restart resource | Resource lifecycle operations function correctly |
| Create disk, Attach, Detach, Delete | Disk lifecycle operations complete successfully |
| Add config, Remove config, Update config | Resource config changes preserve stability |

### Target Count

**5-15 high-level requirements per feature** - not 30-50 trivial operations.

## Source Priority

**EXCLUSIVE source for test scenarios:** Regression Analysis

DO NOT derive test scenarios from:
- Jira ticket descriptions
- Acceptance criteria
- PR descriptions or review comments
- Web search results
- General assumptions

## Negative Scenario Coverage

Include negative test scenarios for:
- Invalid input handling
- Resource constraints
- Permission denied
- Invalid state
- Conflict handling
- Recovery/interruption
- Boundary conditions
- Missing dependencies

## Example Mapping

Input (from regression analysis):
```yaml
impacted_features:
  - feature_name: Resource Update
    relationship: Direct caller
    why_might_break: Update calls resource apply which was modified
```

Output:
```yaml
validated_requirements:
  - requirement_id: MYPROJ-12345
    requirement_summary: Rolling update completes successfully after config changes
    source: regression_analysis
    evidence: UpdateResource → ApplyResourceSpec (modified)
    validation_passed: true
    test_scenario: Verify resource update succeeds with modified config
    priority: P1
```

## Coverage Checklist

Before finalizing, verify:
- [ ] All operations covered (every action the feature supports)
- [ ] All configuration options covered
- [ ] All API fields covered
- [ ] All states covered
- [ ] All integration points covered
- [ ] Positive AND negative scenarios included
- [ ] No gaps between regression findings and test scenarios
