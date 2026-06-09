---
name: go-test-generator
description: Generate working go Go/Ginkgo test code from STD YAML (full implementation)
model: claude-opus-4-6
---

# Go Test Generator Skill (Functional)

## Purpose

Generates **working go Go/Ginkgo test code** from STD YAML specifications.

**Output:** Working Go test files that compile with the project's build system

**Note:** For test stubs (design review), use `go-stub-generator` instead.

---

## Input Required

- `jira_id`: Jira ticket ID (e.g., "MYPROJ-12345")

**Prerequisites:**
- STD YAML file must exist at `outputs/std/{JIRA_ID}/{JIRA_ID}_test_description.yaml`
- STD must contain test scenarios

---

## Output

**Generated Files:**
```
outputs/go-tests/{JIRA_ID}/
├── {feature_name}_test.go           (working implementation)
├── {another_feature}_test.go
└── ... (one file per feature group)
```

**File Characteristics:**
- **Language:** Go (Ginkgo v2 + Gomega)
- **Size:** 200-500 lines per file
- **Status:** Working code (compiles with the project build system)
- **Format:** Follows project-specific go test patterns from reference examples

---

## CRITICAL REQUIREMENT

**Generate ONE test case per STD scenario. No exceptions.**

- ✅ CORRECT: 19 STD scenarios → 19 generated `It()` or `PendingIt()` blocks
- ❌ WRONG: 19 STD scenarios → 7 test files (grouped without covering all scenarios)

**Pattern-based file grouping is allowed**, but **EVERY scenario must get a test case**.

---

## Polarion Toggle

If `project_context.feature_toggles.polarion` is false, omit Polarion test case ID markers from generated test code.

## Workflow

### Step 1: Read STD YAML

Load `outputs/std/{JIRA_ID}/{JIRA_ID}_test_description.yaml`

Expected structure:
```yaml
document_metadata:
  jira_id: MYPROJ-12345
  title: "Feature title"
  tier: go

scenarios:
  - test_id: TS-MYPROJ12345-001
    description: "Test scenario description"
    steps:
      - "Step 1"
      - "Step 2"
    assertions:
      - "Expected outcome"
```

**Extract:**
- Total scenario count: `len(scenarios)`
- All go scenarios (filter by `tier: "Functional"`)

### Step 2: Load Pattern Rules

**The pattern IDs and resource mappings below are examples. At runtime, read patterns from `{project_context.config_dir}/patterns/go_patterns.yaml`. If no patterns are configured, use the scenario description and test_objective to infer test structure directly.**

Read `{project_context.config_dir}/patterns/go_patterns.yaml` for:
- Network resource type detection rules
- Resource type detection rules
- Connectivity pattern rules
- Template selection logic

### Step 3: Group Scenarios by Pattern

**CRITICAL: This step is for file organization only. ALL scenarios must still get tests.**

For each scenario:
1. Detect patterns (network type, resource type, connectivity type)
2. Select template based on priority rules
3. Assign scenario to a file group

Result: Map of `{file_name: [scenario1, scenario2, ...]}

Result: Map of `{file_name: [scenario_ids...]}` grouping related scenarios into test files.

### Step 4: For Each File Group

Generate the test file with ALL scenarios in the group.

**For each scenario in the group:**

1. **Detect Patterns** (from `{project_context.config_dir}/patterns/go_patterns.yaml`)
   - Network type: user-defined, bridge, advanced-network, standard
   - Resource type: Generic, Specialized, Standard
   - Connectivity: ping, TCP, HTTP
   - Special: IPv4/IPv6, migration

2. **Generate Test Case Code**
   - Create an `It()` block with `[test_id:{scenario.test_id}]` label
   - Use scenario description as test description
   - Populate test body based on detected patterns
   - Include all steps from STD scenario
   - Include all assertions from STD scenario

3. **Generate Connectivity Test Code** (if needed)
   - Based on detected connectivity patterns (ping, TCP, HTTP)
   - Generate appropriate test code snippets

**After all scenarios in group processed:**

4. **Select Template** (priority-based from `{project_context.config_dir}/patterns/go_patterns.yaml`)
   - Priority 1: `{project_context.config_dir}/templates/go/parametric_ipv4_ipv6_test.go.template` (if dual stack)
   - Priority 2: `{project_context.config_dir}/templates/go/migration_test.go.template` (if migration + connectivity)
   - Priority 3: `{project_context.config_dir}/templates/go/network_connectivity_test.go.template` (if network + connectivity)
   - Priority 4: `{project_context.config_dir}/templates/go/basic_resource_test.go.template` (default)

5. **Read Template**
   - Load selected template from `{project_context.config_dir}/templates/go/` directory
   - Reference tests are available at `{project_context.config_dir}/reference/go/`

6. **Populate File-Level Placeholders**
   - `{{PACKAGE_NAME}}` → "network"
   - `{{TEST_SUITE_NAME}}` → derived from file group name
   - `{{IMPORTS}}` → all required imports (collected from all scenarios)
   - `{{SETUP_CODE}}` → shared BeforeEach/BeforeAll setup
   - `{{TEST_CASES}}` → ALL generated It() blocks from step 2

7. **Validate Generated Code**
   - Check all imports correct
   - Verify wait functions have proper parameters
   - Ensure no syntax errors (pattern-based validation)
   - **CRITICAL:** Verify number of `It()` blocks equals number of scenarios in group

8. **Save File**
   - Derive filename from file group name (snake_case + _test.go)
   - Save to `outputs/go-tests/{JIRA_ID}/{feature_slug}_test.go`

### Step 5: Validate Complete Coverage

**CRITICAL VALIDATION - This step is MANDATORY**

After all files generated:

1. **Count STD scenarios:**
   - Count all Functional scenarios in STD: `N_std`

2. **Count generated test cases:**
   - Count all `It()` blocks with `[test_id:` in generated files: `N_tests`

3. **Verify completeness:**
   - If `N_tests < N_std`:
     - ERROR: "Incomplete coverage - {N_std - N_tests} scenarios missing"
     - List missing scenario IDs
     - FAIL generation
   - If `N_tests == N_std`:
     - SUCCESS: "Complete coverage - all {N_std} scenarios have tests"

4. **Scenario ID mapping:**
   - For each STD scenario ID, verify it appears in generated code
   - Report any missing IDs

### Step 6: Report Results

Generate summary report with:
- Scenarios processed count
- Go test files generated count
- Total lines of code
- List of generated files with line counts
- Any errors or warnings

---

## Pattern Detection

Read pattern detection rules from `{project_context.config_dir}/patterns/go_patterns.yaml`. Each pattern defines:
- **Keyword conditions** (`match_any`, `match_all`) to detect the scenario type
- **Resource factory mappings** for creating test resources
- **Template selection** for choosing the right code template

The pattern file is project-specific — teams define their own resource types, factory functions, and templates.

---

## Connectivity Test Code Generation

Code templates for connectivity tests (ping, TCP, etc.) are project-specific. Read from `{project_context.config_dir}/patterns/go_patterns.yaml` under the connectivity pattern's `code_templates` section. Generated code should use the project's exec helpers and assertion patterns.

---

## Critical Validation Rules

**ALWAYS validate:**
1. ✅ Wait functions include proper parameters
   - ❌ WRONG: `wait.WaitUntilResourceReady(resource)`
   - ✅ CORRECT: `wait.WaitUntilResourceReady(resource, console.LoginToResource)`

2. ✅ All imports are correct and minimal
3. ✅ Package declaration matches the `default_package` from `go.yaml` or the SIG-derived package name from `project_context.sig_mappings`
4. ✅ Test structure follows Ginkgo v2 patterns

---

## Success Criteria

Code generation succeeds when:
- ✅ **All STD scenarios processed (1:1 mapping: scenario → test case)**
- ✅ **Count of `It()` blocks equals count of STD Functional scenarios**
- ✅ **Every scenario ID appears in generated code with `[test_id:TS-XXX]` label**
- ✅ Valid Go files generated (proper syntax)
- ✅ All imports correct and minimal
- ✅ Wait functions always have proper parameters
- ✅ Files saved to `outputs/go-tests/{JIRA_ID}/`
- ✅ Summary report generated with coverage validation

---

## Error Handling

**If STD file not found:**
- Error: "STD file not found at outputs/std/{JIRA_ID}/{JIRA_ID}_test_description.yaml"
- Suggestion: "Run `/std-builder {JIRA_ID}` first"
- Exit

**If scenario pattern not recognized:**
- Warning: "Could not detect pattern for scenario {id}"
- Fallback: Use basic_resource_test.go.template
- Continue with next scenario

**If validation fails:**
- Error: "Generated code has validation errors"
- Action: Save to `.go.invalid` file for review
- Show validation error details
- Continue with remaining scenarios

---

## Files Structure

Pattern rules, templates, and reference tests are loaded from project config:

```
{project_context.config_dir}/
├── patterns/
│   └── go_patterns.yaml                       # Pattern detection rules
├── templates/
│   └── go/
│       ├── network_connectivity_test.go.template  # Network connectivity test
│       ├── basic_resource_test.go.template        # Basic resource lifecycle test
│       ├── parametric_ipv4_ipv6_test.go.template  # Dual stack parametrized test
│       └── migration_test.go.template              # Migration with connectivity
└── reference/
    └── go/                                      # Reference test implementations
```

---

## Example: Multiple Scenarios in One File

**STD Input (4 scenarios):**
```yaml
scenarios:
  - test_id: "TS-MYPROJ12345-001"
    tier: "Functional"
    test_objective: "Verify ICMP connectivity same node same subnet"

  - test_id: "TS-MYPROJ12345-002"
    tier: "Functional"
    test_objective: "Verify TCP connectivity via external router"

  - test_id: "TS-MYPROJ12345-005"
    tier: "Functional"
    test_objective: "Validate connectivity from worker node to resource"

  - test_id: "TS-MYPROJ12345-006"
    tier: "Functional"
    test_objective: "Validate resource-initiated connections to pods"
```

**Generated Go File (ALL 4 scenarios included):**
```go
package network

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	// ... other imports
)

var _ = Describe("[MYPROJ-12345] Overlay network connectivity", {domain_decorator}, func() {
	var ctx context.Context
	var namespace string
	// ... shared variables

	BeforeEach(func() {
		ctx = context.Background()
		namespace = framework.GetTestNamespace(nil)
	})

	Context("Same-node connectivity tests", func() {
		// ✅ Scenario 1: MUST be included
		It("[test_id:TS-MYPROJ12345-001] should allow ICMP from pod to resource on same subnet", func() {
			// Test implementation for TS-MYPROJ12345-001
		})

		// ✅ Scenario 2: MUST be included
		It("[test_id:TS-MYPROJ12345-002] should allow TCP via external router", func() {
			// Test implementation for TS-MYPROJ12345-002
		})

		// ✅ Scenario 5: MUST be included (was previously missing!)
		It("[test_id:TS-MYPROJ12345-005] should allow connectivity from worker node to resource", func() {
			// Test implementation for TS-MYPROJ12345-005
		})

		// ✅ Scenario 6: MUST be included (was previously missing!)
		It("[test_id:TS-MYPROJ12345-006] should allow resource-initiated connections to pods", func() {
			// Test implementation for TS-MYPROJ12345-006
		})
	})
})
```

**Validation:**
```bash
# Count scenarios in STD
yq '.scenarios[] | select(.tier == "Functional") | .test_id' {JIRA_ID}_test_description.yaml | wc -l

# Count test cases in generated code
grep -c '\[test_id:TS-' outputs/go-tests/{JIRA_ID}/*_test.go

# These counts MUST match (100% coverage)
```

---

**End of Go Test Generator Skill**
