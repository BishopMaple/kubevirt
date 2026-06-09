---
name: scenario-builder
description: Build concise test scenario descriptions for STP requirements mapping
model: claude-opus-4-6
---

# Scenario Builder Skill

**Phase:** Core Processing
**User-Invocable:** false

## Purpose

Build concise test scenario descriptions for the STP Requirements-to-Tests Mapping.

## When to Use

Invoked by the **stp-generator** subagent for each validated requirement.

## Input

```yaml
requirement:
  requirement_id: MYPROJ-12345
  requirement_summary: Config can be updated on running resource
  source: regression_analysis
  evidence: HandleFeatureToggle entry point added
```

## Output Format

```yaml
scenario:
  requirement_id: MYPROJ-12345
  requirement_summary: Config can be updated on running resource
  test_scenarios:
    - description: Verify config update on running resource
      type: positive
    - description: Verify error for invalid config value
      type: negative
  suggested_tier: Functional
  suggested_priority: P0
```

## Scenario Description Rules

### Keep It Brief

| GOOD (Brief) | BAD (Verbose) |
|:-------------|:--------------|
| Verify config update on running resource | Verify that when a user requests to update the configuration of a running resource, the operation completes successfully |
| Test API backward compatibility | Test that the API maintains backward compatibility with previous versions |
| Validate RBAC permissions | Validate that RBAC permissions are properly enforced for the operation |

### Format

- Start with action verb: Verify, Test, Validate, Confirm
- One short phrase (5-10 words)
- NO test steps, preconditions, or expected results
- NO specific commands, API calls, or values
- Describes WHAT is tested, not HOW

### Positive Scenarios

Cover:
- Primary functionality (happy path)
- All supported operations
- Configuration variations
- State transitions

### Negative Scenarios

Every requirement should have at least one negative scenario:

| Positive Scenario | Corresponding Negative |
|:------------------|:-----------------------|
| Verify config update succeeds | Verify error for invalid config value |
| Verify migration completes | Verify graceful failure when target unavailable |
| Verify snapshot creation | Verify error when insufficient storage |
| Verify API accepts valid input | Verify API rejects malformed request |

### Scenario Categories

For each requirement, consider:

| Category | Example |
|:---------|:--------|
| **Basic Operation** | Verify config update on running resource |
| **Error Handling** | Verify error for exceeding config limit |
| **State Validation** | Verify config reflected in resource status |
| **Permission Check** | Verify non-admin cannot update config |
| **Recovery** | Verify cleanup after failed update |
| **Persistence** | Verify config persists after restart |

## Exclusions

DO NOT include:

| Exclude | Why |
|:--------|:----|
| Generic meta-tests | "Verify tests pass in CI" is not a feature test |
| Platform-level tests | We test the product, not the platform |
| Trivial atomic steps | "Start resource" is a prerequisite, not a test |
| Detailed procedures | Steps belong in STD, not STP |
| Irrelevant topologies | No specialized topologies unless feature requires |

## Priority Assignment

| Priority | Criteria |
|:---------|:---------|
| P0 | Core functionality, data integrity, security |
| P1 | Important functionality, error handling, API validation |
| P2 | Edge cases, minor features, optimization |

## Example Transformations

Input:
```yaml
requirement_summary: Rolling update works with config changes
evidence: UpdateResource calls modified ApplyResourceSpec
```

Output:
```yaml
test_scenarios:
  - description: Verify update after config change
    type: positive
  - description: Verify update with pending config change
    type: positive
  - description: Verify error for update during config change
    type: negative
```

Input:
```yaml
requirement_summary: API rejects invalid CPU specifications
evidence: ValidateCPUChange added to API path
```

Output:
```yaml
test_scenarios:
  - description: Verify API rejects zero value
    type: negative
  - description: Verify API rejects negative value
    type: negative
  - description: Verify API rejects exceeding max limit
    type: negative
  - description: Verify descriptive error message returned
    type: negative
```

## Consolidation

If multiple similar scenarios, consolidate:

| Before (Fragmented) | After (Consolidated) |
|:--------------------|:---------------------|
| Test add 1 replica, Test add 2 replicas, Test add 4 replicas | Verify scaling with various counts |
| Check status after update, Check events after update | Verify status and events after update |

## End-to-End Workflow Scenarios

**IMPORTANT:** For each requirement, also consider if an end-to-end workflow scenario is appropriate.

### When to Generate E2E Scenarios

Generate an End-to-End (E2E) scenario if the feature:
- Interacts with other features (migration, storage, networking)
- Has state that should persist across operations
- Is part of a larger user workflow
- Could be affected by upgrades or version changes

### E2E Scenario Patterns

| Feature Type | E2E Scenario to Add |
|:-------------|:--------------------|
| Config update | Verify config state preserved through rolling update |
| Storage operations | Verify storage lifecycle (attach -> snapshot -> restore) |
| Network configuration | Verify network survives resource lifecycle operations |
| Any resource modification | Verify modification persists through restart/migration |
| API changes | Verify backward compatibility across upgrade |

### E2E Scenario Examples

| Atomic (Functional) | E2E Workflow (End-to-End) |
|:----------------|:----------------------|
| Verify config update succeeds | Verify config preserved after rolling update |
| Verify snapshot creation | Verify snapshot -> restore -> verify data workflow |
| Verify network attachment | Verify network connectivity after rolling update |
| Verify disk attachment | Verify disk data persists through restart |
| Verify API accepts input | Verify API behavior consistent after upgrade |

## Output per Requirement

For each requirement, produce 2-7 test scenarios:
- 1 primary positive scenario (Functional - always)
- 1-2 additional positive variations (Functional - if applicable)
- 1 negative scenario (Functional - always)
- 1 end-to-end workflow scenario (End-to-End - when applicable, see above)
- 0-2 dimensional probing scenarios (from the 12-dimension system below, when applicable)

Bias toward the lower end (2-3) for simple features and the upper end (5-7) for complex
features with many applicable dimensions.

---

## Dimensional Probing (Comprehensive)

The 6 categories above (Basic Operation through Persistence) serve as a quick reference.
The following 12-dimension system provides comprehensive probing for systematic edge
case discovery. Use it after generating scenarios from the quick-reference categories.

### 12 Exploration Dimensions

| # | Dimension | Probing Question | Example Scenario |
|:--|:----------|:-----------------|:-----------------|
| 1 | Happy Path | Does the primary operation succeed? | Verify config update on running resource |
| 2 | Error | What happens when the operation fails? | Verify error for invalid config value |
| 3 | Edge Case | What happens at boundaries (0, 1, max, empty, nil)? | Verify behavior with maximum allowed resource count |
| 4 | Abuse | What if input is malicious or wildly unexpected? | Verify rejection of injection in resource name |
| 5 | Scale | What happens with many resources or large payloads? | Verify operation with 100+ concurrent resources |
| 6 | Concurrent | What if two operations happen simultaneously? | Verify conflict handling for parallel modifications |
| 7 | Temporal | What if timing or ordering matters? | Verify operation during ongoing migration |
| 8 | Data Variation | What if data format or encoding varies? | Verify handling of unicode in resource names |
| 9 | Permission | Who can and cannot perform this? | Verify non-admin cannot modify resource |
| 10 | Integration | How does this interact with other features? | Verify feature works after rolling update |
| 11 | Recovery | What happens after failure or crash? | Verify state restored after node crash |
| 12 | State Transition | What happens across lifecycle transitions? | Verify state preserved through restart |

### Mapping to Quick-Reference Categories

| Quick-Reference Category | Maps to Dimension(s) |
|:-------------------------|:---------------------|
| Basic Operation | 1 (Happy Path) |
| Error Handling | 2 (Error) |
| State Validation | 12 (State Transition) — expanded to cover full lifecycle |
| Permission Check | 9 (Permission) |
| Recovery | 11 (Recovery) |
| Persistence | 12 (State Transition) — subsumed |
| *(new)* | 3 (Edge Case), 4 (Abuse), 5 (Scale), 6 (Concurrent), 7 (Temporal), 8 (Data Variation), 10 (Integration) |

### Feature-Type Weighting

Not all dimensions apply equally. Use this lookup table to determine which dimensions
to probe heavily based on feature keywords from the Jira component, labels, or
requirement description.

| Feature Keywords | Probe Heavily | Probe Lightly |
|:-----------------|:--------------|:--------------|
| network, connectivity, interface, bridge | Concurrent, Scale, Integration, Temporal | Abuse, Data Variation |
| storage, volume, disk, snapshot, PVC | Recovery, State Transition, Scale, Edge Case | Abuse, Temporal |
| migration, rolling-update, evacuate | Temporal, Concurrent, State Transition, Integration | Abuse, Data Variation |
| API, RBAC, auth, permission, webhook | Abuse, Permission, Edge Case, Data Variation | Scale, Temporal |
| upgrade, update, version, lifecycle | State Transition, Integration, Recovery, Edge Case | Abuse, Concurrent |
| CPU, memory, config-update, topology | Concurrent, Edge Case, State Transition | Abuse, Data Variation |
| UI, console, VNC | Data Variation, Permission, Edge Case | Scale, Concurrent |

The weighting table is a heuristic guide, not a hard gate. If a "probe lightly"
dimension yields a clearly valuable scenario, include it.

### Probing Execution Flow

For each requirement:

1. **Generate base scenarios** using the existing quick-reference categories
   (positive, negative, E2E) as described above
2. **Determine feature keywords** from `requirement_summary` and `evidence`
3. **Look up high-priority dimensions** from the weighting table
4. **Probe each high-priority dimension** not already covered by step 1:
   - Ask the probing question against this specific requirement
   - If the answer yields a meaningful, non-duplicate scenario: add it
   - If redundant with an existing scenario: skip
5. **Apply consolidation rules** (existing — no duplicates, no platform-level tests)
6. **Cap total scenarios** at 2-7 per requirement (bias toward lower end for simple
   features, upper end for complex features with many applicable dimensions)

Probed scenarios follow all existing format rules:

- 5-10 word descriptions
- Action verb prefix (Verify, Test, Validate, Confirm)
- No test steps, preconditions, or expected results
- No specific commands, API calls, or values
- Describes WHAT is tested, not HOW

Do NOT generate scenarios for dimensions that produce trivial or platform-level tests
(existing exclusion rules still apply).

### Probing Examples

These examples show how dimensional probing produces scenarios the quick-reference
categories alone would miss.

#### Example 1: Network Interface Attachment Feature

**Requirement:** Network interface can be attached to active resource

**Base scenarios (from quick-reference categories):**

- Verify interface attachment to active resource (Happy Path)
- Verify error for invalid interface spec (Error)
- Verify interface state after resource restart (State Transition)
- Verify non-admin cannot attach interface (Permission)

**Feature keywords:** network, interface

**High-priority dimensions:** Concurrent, Scale, Integration, Temporal

**Dimensional probing adds:**

- Verify behavior when two interfaces attached simultaneously (Concurrent)
- Verify attachment during resource migration (Temporal)
- Verify attachment with maximum interfaces present (Edge Case/Scale)

#### Example 2: Storage Backup Feature

**Requirement:** Backup can be created from active resource

**Base scenarios (from quick-reference categories):**

- Verify backup creation from active resource (Happy Path)
- Verify error on insufficient storage quota (Error)
- Verify backup data integrity after restore (Recovery)
- Verify non-admin cannot create backup (Permission)

**Feature keywords:** storage, backup, volume

**High-priority dimensions:** Recovery, State Transition, Scale, Edge Case

**Dimensional probing adds:**

- Verify backup during active I/O workload (Concurrent)
- Verify backup of maximum-size volume (Scale)
- Verify backup restore after node failure (Recovery + State Transition)

#### Example 3: RBAC Webhook Feature

**Requirement:** Webhook validates RBAC permissions for API operations

**Base scenarios (from quick-reference categories):**

- Verify webhook permits authorized operation (Happy Path)
- Verify webhook rejects unauthorized operation (Error)
- Verify webhook state after API server restart (Recovery)

**Feature keywords:** RBAC, webhook, permission

**High-priority dimensions:** Abuse, Permission, Edge Case, Data Variation

**Dimensional probing adds:**

- Verify webhook rejects malformed permission payload (Abuse)
- Verify behavior with empty role binding list (Edge Case)
- Verify handling of special characters in role names (Data Variation)
