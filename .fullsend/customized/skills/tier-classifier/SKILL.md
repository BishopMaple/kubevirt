---
name: tier-classifier
description: Classify test scenarios as Unit Tests, Functional, or End-to-End
model: claude-opus-4-6
---

# Tier Classifier Skill

**Phase:** Core Processing
**User-Invocable:** false

## Purpose

Classify test scenarios as Unit Tests, Functional, or End-to-End.

## When to Use

Invoked by the **stp-generator** subagent for each test scenario.

## Input

```yaml
scenario:
  requirement_id: MYPROJ-12345
  requirement_summary: Config can be updated on running resource
  test_description: Verify config update on running resource
  type: positive
  priority: P0
  fix_scope:                          # optional, from github_data
    files_changed: 1
    functions_changed: ["validateCPUModel"]
    packages_changed: ["internal/controller/feature"]
    requires_cluster_interaction: false
    issue_type: bug                   # bug vs feature
```

## Output Format

```yaml
classification:
  requirement_id: MYPROJ-12345
  test_description: Verify config update on running resource
  test_type: Functional
  reasoning: Tests single feature (config update) in isolation
```

## Valid Test Types

**ONLY these three values are valid:**

| Test Type | Description |
|:----------|:------------|
| `Unit Tests` | Isolated components with mocks; validates individual functions/modules. **Note:** Unit tests are classified for tracking in the STP but are developer-responsibility -- no auto-generation pipeline exists for this tier. |
| `Functional` | Single feature in real cluster; API contracts; basic workflows |
| `End-to-End` | Complete user workflows; multi-feature integrations; **user-scenario focused** |

## Key Principle: User-Scenario Focus for End-to-End

**End-to-End tests are strictly user-scenario focused.** They validate what end users experience and interact with, not internal system behavior, implementation details, or diagnostic information.

**Key Principle:** Tests should only verify observable user outcomes, not internal system state or logs.

## Decision Matrix

| Question | Unit | Functional | End-to-End |
|:---------|:-----|:-------|:-------|
| Tests isolated functions with mocks? | YES | no | no |
| Tests single feature in real cluster? | no | YES | no |
| Requires multiple features working together? | no | no | YES |
| Tests basic API or component functionality? | no | YES | no |
| Validates complete user workflow? | no | no | YES |
| Can run without cluster (mocked dependencies)? | YES | no | no |
| Requires minimal test cluster? | no | YES | no |
| Requires production-like environment? | no | no | YES |
| Tests upgrade or migration paths? | no | no | YES |
| Tests at scale (100+ resources)? | no | no | YES |
| Involves multiple resources interacting? | no | no | YES |
| Tests data persistence across operations? | no | no | YES |

## Classification Flow (Updated)

```
0. Fix-Scope Demotion Check (optional)
   SKIP if fix_scope is absent OR issue_type is feature/enhancement.
   ONLY activate when fix_scope is present AND issue_type is bug/customer_case/defect.

   a. Single function changed AND requires_cluster_interaction is false?
      YES -> Unit Tests
             reasoning: "Fix modifies single function {name} with no cluster
             interaction. Unit test provides equivalent coverage at lower cost."
      NO  -> Continue

   b. Single package changed AND single resource type?
      YES -> Functional (Functional)
             reasoning: "Fix is scoped to {package}, single resource operation.
             Functional provides equivalent coverage."
      NO  -> Continue to Step 1 (no demotion)

1. Does it require a cluster?
   NO  -> Unit Tests
   YES -> Continue

2. Check End-to-End PROMOTION triggers first (see below)
   ANY trigger matches -> End-to-End (End-to-End)
   NO triggers match -> Continue

3. Does it test a single feature in isolation?
   YES -> Functional (Functional)
   NO  -> End-to-End (End-to-End)
```

**IMPORTANT:** Check End-to-End triggers BEFORE defaulting to Functional.

## End-to-End Promotion Triggers

**Qualifying Rule:** A trigger matches only when the **test itself** exercises that
workflow as its primary action — not when the feature merely uses that mechanism
internally. Classify based on what the **test** does, not what the **feature** does
under the hood.

Example: A feature that uses a rolling update internally to apply a config change does NOT
make a test "Rolling update with workload validation" — unless the test's primary
action is to perform and validate a rolling update. If the test patches a spec field and
checks a resource condition, it's Functional regardless of whether a rolling update happens behind
the scenes.

**If ANY of these are true for what the test exercises, classify as End-to-End:**

| Trigger | Example |
|:--------|:--------|
| Involves multiple resources interacting | Multi-tier app deployment |
| Tests complete user story/workflow | Create resource -> Run workload -> Migrate -> Verify |
| Resources must survive across operations | Resource state preserved through migration |
| Validates data/state persistence across operations | Snapshot -> Restore -> Verify data |
| Tests upgrade or version compatibility | Upgrade from v1.x to v1.y |
| Requires external systems | External router, load balancer |
| Simulates production deployment | Full application stack |
| Tests disaster recovery or failover | Node failure recovery |
| RBAC across multiple resources/operations | User permissions through resource lifecycle |
| Storage lifecycle with multiple steps | Provision -> Attach -> Snapshot -> Restore |
| Rolling update with workload validation | Update while workload running, verify continuity |

## What's NOT in Functional

**Classify as End-to-End (not Functional) if the scenario involves:**

- Multi-feature integration scenarios
- Complex end-to-end user workflows and user stories
- Performance and scale testing
- Upgrade scenarios
- Disaster recovery scenarios
- Multi-step workflows (create -> operate -> verify persistence)
- Cross-component interactions

## What End-to-End Does NOT Test

**Do NOT classify as End-to-End if testing:**

- Internal debug logs validation (not user-facing)
- Internal component implementation details
- Code-level unit behaviors
- Low-level API internals not exposed to users
- Developer debugging workflows
- Kubernetes platform features (not product-specific)
- System metrics users don't interact with
- Internal error messages or stack traces

**Note:** Tests may verify user-observable Kubernetes Events (user-facing API) but should not parse internal pod logs.

## Functional (Functional) Indicators

Classify as Functional if:
- Tests a single feature in isolation
- Validates API contracts
- Basic CRUD operations
- Single resource lifecycle
- Error handling for single feature
- Basic configuration validation
- **AND** no End-to-End promotion triggers apply

**Examples:**
- Create resource with storage volume
- Attach network interface via API
- Create resource snapshot (single operation)
- Stop running resource
- Attach single disk

## End-to-End (End-to-End) Indicators

Classify as End-to-End if:
- Requires multiple features working together
- Tests complete user workflow
- Involves cross-component interaction
- Requires production-like environment
- Tests upgrade/migration paths
- Tests at scale (100+ resources)
- Involves multi-step scenarios with state verification

**Examples:**
- Deploy multi-tier app with resources
- Rolling update with workload validation
- Create -> Snapshot -> Restore -> Verify workflow
- Upgrade from version X to Y
- RBAC workflow across resource lifecycle
- Storage lifecycle (provision -> attach -> snapshot -> restore)
- Multi-resource network communication
- Feature toggle followed by rolling update and state verification

## Unit Test Indicators

Classify as Unit Tests if:
- Tests individual function/method
- Uses mocks for dependencies
- No cluster required
- Developer responsibility typically

**Examples:**
- Validate input parsing function
- Test error message formatting
- Test configuration parsing

## Common Misclassifications

| Scenario | Wrong | Correct | Reason |
|:---------|:------|:--------|:-------|
| Deploy 3-tier app | Functional | End-to-End | Multi-resource workflow |
| Migration (single) | End-to-End | Functional | Single feature operation |
| API validation | Unit | Functional | Requires cluster |
| Upgrade with running resources | Functional | End-to-End | Multi-step, cross-version |
| Attach single disk | End-to-End | Functional | Single feature |
| Migrate then verify workload | Functional | End-to-End | Multi-step with state verification |
| Snapshot and restore | Functional | End-to-End | Multi-step workflow |
| Resource survives node drain | Functional | End-to-End | Cross-component, DR scenario |
| Scale test with 100 resources | Functional | End-to-End | Scale testing |
| Config change + rolling update | Functional | End-to-End | Multi-feature integration |

## Priority Influence

Priority doesn't determine tier:
- P0 can be Functional or End-to-End
- P2 can be Functional or End-to-End

Tier is based on **scope and complexity**, not importance.

## Output Examples

Input:
```yaml
test_description: Verify config update on running resource
```

Output:
```yaml
test_type: Functional (Functional)
reasoning: Tests single feature (config update) in real cluster, no multi-step workflow
```

Input:
```yaml
test_description: Verify resource state preserved through snapshot and restore
```

Output:
```yaml
test_type: End-to-End (End-to-End)
reasoning: Multi-step workflow (create -> snapshot -> restore -> verify state)
```

Input:
```yaml
test_description: Verify upgrade preserves resource configuration
```

Output:
```yaml
test_type: End-to-End (End-to-End)
reasoning: Cross-version testing, requires upgrade scenario
```

Input:
```yaml
test_description: Verify config change followed by rolling update preserves resource state
```

Output:
```yaml
test_type: End-to-End (End-to-End)
reasoning: Multi-feature integration (config change + rolling update), state verification across operations
```

Input:
```yaml
test_description: Verify resource can be created with 216 cores
```

Output:
```yaml
test_type: Functional (Functional)
reasoning: Single feature (resource creation), single operation, no multi-step workflow
```
