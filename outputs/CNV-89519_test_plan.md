# Openshift-virtualization-tests Test plan

## **VMI Domain Reconciliation During virt-handler Rolling Update - Quality Engineering Plan**

### **Metadata & Tracking**

| Field                  | Details                                                                                                     |
|:-----------------------|:------------------------------------------------------------------------------------------------------------|
| **Enhancement(s)**     | [CNV-89519](https://redhat.atlassian.net/browse/CNV-89519)                                                  |
| **Feature in Jira**    | [CNV-89519](https://redhat.atlassian.net/browse/CNV-89519)                                                  |
| **Jira Tracking**      | [CNV-89519](https://redhat.atlassian.net/browse/CNV-89519) (Tasks must be created to **block the epic**)    |
| **QE Owner(s)**        | TBD                                                                                                         |
| **Owning SIG**         | sig-compute                                                                                                 |
| **Participating SIGs** | sig-compute                                                                                                 |
| **Current Status**     | Draft                                                                                                       |

**Document Conventions:**
- **VMI** — VirtualMachineInstance
- **virt-handler** — KubeVirt DaemonSet component responsible for managing VMs on each node
- **Ghost record** — On-disk checkpoint storing VM identity (namespace/name/UID/socket) so virt-handler can reconnect to running domains after restart
- **Domain** — libvirt domain representing a running VM

---

### **Feature Overview**

During a CNV control plane update, the virt-handler DaemonSet undergoes a rolling update. When the new virt-handler pod starts on a node, it must discover all existing running libvirt domains via ghost record checkpoints and socket connections, then reconcile them against the Kubernetes API server. A bug was observed (CNV-89519) where the new virt-handler failed to discover 1 of 252 running domains during its initial scan, causing it to incorrectly transition the VMI from Running to Failed phase due to a "Domain does not exist" false negative. The domain was confirmed to still be running during the subsequent cleanup. This is a critical reliability issue for large-scale deployments during upgrade operations.

**Root Cause Analysis (from codebase):**
The `listAllKnownDomains()` function in `pkg/virt-handler/cache/domain-watcher.go` iterates ghost record sockets to retrieve domains via virt-launcher cmd-client connections. A transient socket connection failure during initial scan could cause a domain to be missed. The fix adds `getDomainWithRetry()` with configurable retries (3 attempts, 500ms exponential backoff) to mitigate transient failures (see `listDomainRetries`/`listDomainRetryBackoff` variables at domain-watcher.go:51-52). Additionally, the `calculateVmPhaseForStatusReason()` function in `vm.go:2196` transitions a Running VMI to Failed when the domain is nil and the launcher client is unresponsive, which is the code path that triggers the false-negative transition.

---

### **I. Motivation and Requirements Review (QE Review Guidelines)**

#### **1. Requirement & User Story Review Checklist**

| Check                                  | Done | Details/Notes                                                                                                                                                                                  | Comments                                                                               |
|:---------------------------------------|:-----|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:---------------------------------------------------------------------------------------|
| **Review Requirements**                | [x]  | Reviewed CNV-89519 bug report — VMI incorrectly marked Failed during virt-handler rolling update due to transient socket connection failure during initial domain scan.                         |                                                                                        |
| **Understand Value**                   | [x]  | Critical for customer workload continuity during CNV upgrades. 1 of 252 VMIs was disrupted — at scale this could affect multiple VMs per node.                                                 | U/S: As a cluster admin, I expect all running VMs to survive a CNV control plane update |
| **Customer Use Cases**                 | [x]  | Large-scale deployments (250+ VMs per node) performing CNV version upgrades with zero VM downtime requirement.                                                                                 |                                                                                        |
| **Testability**                        | [x]  | Testable via controlled virt-handler restart scenarios with running VMs. Retry mechanism is deterministic and observable.                                                                       |                                                                                        |
| **Acceptance Criteria**                | [x]  | All running VMIs must remain in Running phase throughout virt-handler DaemonSet rolling update. The initial domain scan must successfully reconnect to all running domains.                     |                                                                                        |
| **Non-Functional Requirements (NFRs)** | [x]  | **Performance:** Retry backoff should not significantly delay virt-handler startup. **Scalability:** Must work with 250+ VMs per node. **Monitoring:** VMI phase transitions during upgrade.    |                                                                                        |

#### **2. Technology and Design Review**

| Check                            | Done | Details/Notes                                                                                                                                                                                                       | Comments                                                                                              |
|:---------------------------------|:-----|:--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------------------------------------------------|
| **Developer Handoff/QE Kickoff** | [ ]  | TBD — Review fix implementation in `getDomainWithRetry()` and retry parameters.                                                                                                                                     | Coordinate with virt-handler component developers                                                     |
| **Technology Challenges**        | [x]  | Transient socket failures during virt-handler startup race condition. Libvirt domain scan timing depends on node load and number of running VMs. Ghost record checkpoint I/O on disk.                                | Reproducing the exact race condition may be challenging due to timing sensitivity                      |
| **Test Environment Needs**       | [x]  | Multi-node cluster with ability to run 50+ VMs per worker node. Must support controlled virt-handler pod restart/deletion.                                                                                          | Scale testing environment with adequate compute resources required                                    |
| **API Extensions**               | [x]  | No new APIs. Fix is internal to virt-handler domain watcher (`listAllKnownDomains`, `getDomainWithRetry`).                                                                                                          |                                                                                                       |
| **Topology Considerations**      | [x]  | Single-cluster topology. Fix is node-local — each virt-handler instance independently reconnects to domains on its node.                                                                                            |                                                                                                       |

---

### **II. Software Test Plan (STP)**

#### **1. Scope of Testing**

**Testing Goals**

- **[P0]** Verify all running VMIs survive a virt-handler DaemonSet rolling update without transitioning to Failed phase
- **[P0]** Verify the domain retry mechanism (`getDomainWithRetry`) successfully reconnects to virt-launcher sockets after transient failures
- **[P0]** Verify ghost record initialization correctly loads all pre-existing VM records from disk checkpoints during virt-handler startup
- **[P1]** Validate VMI phase stability under scale conditions (50+ VMs per node) during virt-handler restart
- **[P1]** Verify the `listAllKnownDomains()` function correctly discovers all running domains by iterating ghost record sockets with retry logic
- **[P2]** Validate that the exponential backoff in `getDomainWithRetry` does not excessively delay virt-handler startup readiness

**Out of Scope (Testing Scope Exclusions)**

| Out-of-Scope Item                                                       | Rationale                                                                                              | PM/Lead Agreement |
|:------------------------------------------------------------------------|:-------------------------------------------------------------------------------------------------------|:------------------|
| Testing virt-controller rolling update behavior                         | Separate component; this bug is specific to virt-handler domain scan                                   | [ ] TBD           |
| Testing with 250+ VMs per node (production-scale)                       | CI resource constraints; validated at smaller scale with representative conditions                     | [ ] TBD           |
| Testing across different CNV version upgrade paths (e.g., 4.20 → 4.21) | Fix applies uniformly regardless of version; functional testing covers the mechanism                   | [ ] TBD           |
| Testing libvirt internal domain state transitions                       | Out of KubeVirt scope — underlying platform behavior                                                   | [ ] TBD           |

#### **2. Test Strategy**

| Item                           | Description                                                                                                                                                  | Applicable (Y/N or N/A) | Comments                                                                                               |
|:-------------------------------|:-------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------|:-------------------------------------------------------------------------------------------------------|
| Functional Testing             | Validates that VMIs survive virt-handler rolling update and domain retry mechanism works correctly                                                            | Y                       | Core test focus                                                                                        |
| Automation Testing             | All test cases will be automated for CI/CD regression coverage                                                                                               | Y                       | Tier 1 and Tier 2 tests automated                                                                     |
| Performance Testing            | Validates that retry backoff does not significantly delay virt-handler readiness                                                                              | Y                       | Measured during scale scenarios                                                                        |
| Security Testing               | N/A — no security-related changes in this fix                                                                                                                | N/A                     |                                                                                                        |
| Usability Testing              | N/A — no UI changes                                                                                                                                          | N/A                     |                                                                                                        |
| Compatibility Testing          | Verify fix works with different VM workload types (Linux, Windows)                                                                                           | Y                       | Basic VM type coverage                                                                                 |
| Regression Testing             | Verify existing VM lifecycle operations are not affected by retry mechanism                                                                                   | Y                       | Standard VM create/start/stop/delete                                                                   |
| Upgrade Testing                | Core scenario — validate VMI stability during CNV upgrade triggering virt-handler rolling update                                                             | Y                       | Primary test scenario                                                                                  |
| Backward Compatibility Testing | N/A — internal implementation change, no API impact                                                                                                          | N/A                     |                                                                                                        |
| Dependencies                   | Depends on virt-launcher pod socket availability and libvirt domain state                                                                                    | Y                       | virt-launcher must be functional during virt-handler restart                                           |
| Cross Integrations             | virt-controller processes VMI phase transitions — verify it handles the fix correctly                                                                        | Y                       | Verify virt-controller does not interfere during virt-handler rolling update                           |
| Monitoring                     | Monitor VMI phase transition events during upgrade for unexpected Failed transitions                                                                         | Y                       | Kubernetes events and VMI status monitoring                                                            |
| Cloud Testing                  | N/A — node-local fix, platform-agnostic                                                                                                                     | N/A                     |                                                                                                        |

#### **3. Test Environment**

| Environment Component                         | Configuration                                             | Specification Examples                                                                |
|:----------------------------------------------|:----------------------------------------------------------|:--------------------------------------------------------------------------------------|
| **Cluster Topology**                          | Multi-node                                                | 3-master/3-worker bare-metal or virtual                                               |
| **OCP & OpenShift Virtualization Version(s)** | OCP 4.22+ with OpenShift Virtualization 4.22+             | OCP 4.22 with CNV 4.22                                                                |
| **CPU Virtualization**                        | Standard                                                  | Nodes with VT-x (Intel) or AMD-V (AMD) enabled in BIOS                               |
| **Compute Resources**                         | Sufficient for 50+ VMs per worker node                    | Minimum per worker node: 16 vCPUs, 64GB RAM                                          |
| **Special Hardware**                          | N/A                                                       | No special hardware required                                                          |
| **Storage**                                   | Default storage class                                     | ocs-storagecluster-ceph-rbd or hostpath-csi                                           |
| **Network**                                   | OVN-Kubernetes (default)                                  | Standard cluster networking                                                           |
| **Required Operators**                        | OpenShift Virtualization                                  | kubevirt-hyperconverged operator                                                      |
| **Platform**                                  | Bare metal or virtual                                     | Bare metal preferred for scale tests                                                  |
| **Special Configurations**                    | Ability to delete/restart virt-handler pods manually       | Cluster admin access required                                                         |

#### **3.1. Testing Tools & Frameworks**

| Category           | Tools/Frameworks                                                          |
|:-------------------|:--------------------------------------------------------------------------|
| **Test Framework** | Ginkgo/Gomega (Tier 1, upstream), pytest (Tier 2, downstream)             |
| **CI/CD**          | Standard Prow CI lanes                                                    |
| **Other Tools**    | `oc`, `kubectl`, `virtctl` for cluster interaction and VM management      |

#### **4. Entry Criteria**

The following conditions must be met before testing can begin:

- [x] Requirements and design documents are **approved and merged**
- [x] Test environment can be **set up and configured** (see Section II.3 - Test Environment)
- [ ] Fix for `getDomainWithRetry` is merged in KubeVirt upstream
- [ ] CNV build containing the fix is available for testing

#### **5. Risks**

| Risk                                            | Impact                                                                     | Likelihood | Mitigation Strategy                                                                      |
|:------------------------------------------------|:---------------------------------------------------------------------------|:-----------|:-----------------------------------------------------------------------------------------|
| Race condition is difficult to reproduce in CI  | Test may not trigger the exact failure scenario                            | High       | Use controlled virt-handler restart with running VMs; verify retry mechanism via unit tests |
| Scale testing resource constraints              | Cannot test at full production scale (250+ VMs/node)                       | Medium     | Test at reduced scale (50+ VMs/node); rely on unit tests for retry mechanism correctness  |
| Retry backoff may mask different failure modes   | New retry logic could hide other socket-related issues                     | Low        | Monitor virt-handler logs for retry attempts; alert on excessive retries                  |
| virt-launcher socket availability timing         | Socket may not be ready even after retries                                 | Low        | Configurable retry parameters allow tuning; fallback to ghost record stale domain path    |

#### **6. Limitations**

- The exact production scenario (252 VMs, specific node hardware, stress-ng workload) is not reproducible in standard CI environments
- Timing-dependent race conditions may not manifest deterministically in automated tests
- Retry parameters (`listDomainRetries=3`, `listDomainRetryBackoff=500ms`) are currently hardcoded; tests validate default values only

---

### **III. Test Case Descriptions & Traceability**

#### **Traceability Matrix**

| Test ID       | Requirement                                                            | Tier   | Test Scenario                                                                                             |
|:--------------|:-----------------------------------------------------------------------|:-------|:----------------------------------------------------------------------------------------------------------|
| TS-CNV-89519-001 | VMIs must survive virt-handler restart                              | Tier 1 | Unit test: `getDomainWithRetry` returns domain after transient socket failure                              |
| TS-CNV-89519-002 | VMIs must survive virt-handler restart                              | Tier 1 | Unit test: `getDomainWithRetry` exhausts retries and returns nil on persistent failure                     |
| TS-CNV-89519-003 | VMIs must survive virt-handler restart                              | Tier 1 | Unit test: `listAllKnownDomains` discovers all domains from ghost records with retry                       |
| TS-CNV-89519-004 | Ghost records load correctly on startup                              | Tier 1 | Unit test: `InitializeGhostRecordCache` loads all checkpoint records from disk                             |
| TS-CNV-89519-005 | VMIs must survive virt-handler restart                              | Tier 1 | Unit test: `calculateVmPhaseForStatusReason` returns correct phase when domain is nil vs present           |
| TS-CNV-89519-006 | VMI phase stability during virt-handler rolling update              | Tier 2 | E2E: Running VM survives virt-handler pod deletion and replacement                                         |
| TS-CNV-89519-007 | VMI phase stability at scale during virt-handler rolling update     | Tier 2 | E2E: Multiple running VMs (10+) survive virt-handler rolling update on same node                           |
| TS-CNV-89519-008 | VMI phase stability during CNV upgrade                              | Tier 2 | E2E: Running VMs survive full CNV control plane upgrade triggering virt-handler DaemonSet rolling update   |

#### **Test Scenario Descriptions**

**TS-CNV-89519-001: getDomainWithRetry succeeds after transient failure**
- **Preconditions:** Mock virt-launcher cmd-client that fails on first attempt, succeeds on retry
- **Steps:**
  1. Call `getDomainWithRetry` with a mock client factory that returns error on first call, valid domain on second
  2. Verify the domain is returned successfully
- **Expected:** Domain is returned after retry; no nil result

**TS-CNV-89519-002: getDomainWithRetry returns nil after exhausting retries**
- **Preconditions:** Mock virt-launcher cmd-client that always fails
- **Steps:**
  1. Call `getDomainWithRetry` with `maxRetries=3` and a client factory that always errors
  2. Verify nil is returned after all retries are exhausted
- **Expected:** Function returns nil; exactly `maxRetries+1` attempts are made

**TS-CNV-89519-003: listAllKnownDomains discovers all ghost record domains**
- **Preconditions:** Ghost record store populated with N records; all sockets exist and are responsive
- **Steps:**
  1. Populate `GhostRecordGlobalStore` with multiple ghost records
  2. Ensure corresponding socket files exist
  3. Call `listAllKnownDomains()`
  4. Verify all N domains are returned
- **Expected:** All N domains are discovered; no domains are missed

**TS-CNV-89519-004: InitializeGhostRecordCache loads all checkpoints**
- **Preconditions:** Checkpoint directory with N ghost record files on disk
- **Steps:**
  1. Create N checkpoint files in the ghost record directory
  2. Call `InitializeGhostRecordCache` with the checkpoint manager
  3. Verify `GhostRecordGlobalStore.Exists()` returns true for all records
- **Expected:** All N records are loaded into the in-memory cache

**TS-CNV-89519-005: calculateVmPhaseForStatusReason with nil domain**
- **Preconditions:** VMI in Running phase; domain is nil; launcher client is responsive
- **Steps:**
  1. Call `calculateVmPhaseForStatusReason(nil, vmi)` with a Running VMI
  2. Verify the returned phase when launcher is responsive vs unresponsive
- **Expected:** When launcher is responsive, phase remains Running-compatible (not Failed). When launcher is unresponsive, phase transitions to Failed.

**TS-CNV-89519-006: Running VM survives virt-handler pod deletion**
- **Preconditions:** Running VM on a worker node; virt-handler DaemonSet healthy
- **Steps:**
  1. Create and start a VirtualMachine on a worker node
  2. Verify VMI is in Running phase
  3. Delete the virt-handler pod on that node
  4. Wait for the replacement virt-handler pod to become Ready
  5. Verify the VMI remains in Running phase throughout
  6. Verify the VM is accessible (e.g., console or SSH)
- **Expected:** VMI remains Running; no Failed phase transition observed; VM is accessible after virt-handler replacement

**TS-CNV-89519-007: Multiple VMs survive virt-handler rolling update on same node**
- **Preconditions:** 10+ running VMs on a single worker node
- **Steps:**
  1. Create and start 10+ VirtualMachines, ensuring they are scheduled to the same worker node
  2. Verify all VMIs are in Running phase
  3. Trigger a virt-handler rolling update (e.g., by modifying the DaemonSet or deleting the pod)
  4. Wait for the replacement virt-handler pod to become Ready
  5. Verify all VMIs remain in Running phase
  6. Verify no VMI experienced a Failed phase transition (check Kubernetes events)
- **Expected:** All VMIs survive the virt-handler replacement; zero Failed transitions; all VMs remain accessible

**TS-CNV-89519-008: Running VMs survive CNV control plane upgrade**
- **Preconditions:** Multiple running VMs across worker nodes; CNV upgrade path available
- **Steps:**
  1. Create and start multiple VirtualMachines across worker nodes
  2. Verify all VMIs are in Running phase
  3. Initiate a CNV control plane upgrade (operator upgrade)
  4. Monitor VMI phase transitions during the upgrade
  5. Wait for upgrade to complete
  6. Verify all VMIs remain in Running phase
  7. Verify all VMs are accessible post-upgrade
- **Expected:** All VMIs survive the upgrade; no spurious Failed transitions; VM workloads continue uninterrupted

---

### **IV. Regression Impact Analysis (LSP-Traced)**

The following dependency chains were identified via LSP analysis of the KubeVirt source code:

#### **Critical Code Paths**

1. **Ghost Record Initialization Chain:**
   `cmd/virt-handler/virt-handler.go:276` → `InitializeGhostRecordCache()` → `GhostRecordGlobalStore` (global variable)
   - Used by: `domainWatcher.listAllKnownDomains()`, `domainWatcher.handleResync()`, `domainWatcher.handleStaleSocketConnections()`, `VirtualMachineController.execute()`
   - **Risk:** Any change to ghost record loading affects all domain discovery paths

2. **Domain Discovery Chain:**
   `domainWatcher.List()` → `listAllKnownDomains()` → `getDomainWithRetry()` → `cmdclient.NewClient()` → `client.GetDomain()`
   - The `getDomainWithRetry()` function (domain-watcher.go:349) is the fix for CNV-89519
   - **Risk:** Retry mechanism must not mask persistent failures

3. **VMI Phase Transition Chain:**
   `VirtualMachineController.Execute()` → `execute()` → `sync()` → `calculateVmPhaseForStatusReason()`
   - When domain is nil and launcher is unresponsive → VMI transitions to Failed (vm.go:2218)
   - When domain is nil and launcher is responsive → VMI stays in Scheduled phase (vm.go:2220)
   - **Risk:** False negative in domain scan leads to incorrect nil domain, triggering Failed transition

4. **Launcher Client References:**
   `GhostRecordGlobalStore` is referenced in `launcher-clients.go:90,139` for launcher client management
   - Ghost records are used to map VMI identity to launcher sockets
   - **Risk:** Missing ghost record → no launcher client → unresponsive detection → Failed transition

#### **Affected Components (14 references to GhostRecordGlobalStore)**

| File | Usage | Risk Level |
|:-----|:------|:-----------|
| `pkg/virt-handler/cache/domain-watcher.go` | Domain listing, resync, stale socket detection | High |
| `pkg/virt-handler/vm.go` | UID resolution fallback in `execute()` | Medium |
| `pkg/virt-handler/launcher-clients/launcher-clients.go` | Launcher client socket mapping | Medium |
| `cmd/virt-handler/virt-handler.go` | Initialization at startup | High |

---

### **V. Sign-off and Approval**

**Final Sign-off Checklist:**

- [ ] Tier 1 unit tests for `getDomainWithRetry` and `listAllKnownDomains` defined and merged
- [ ] Tier 2 E2E test for VMI survival during virt-handler rolling update defined and merged
- [ ] Automation merged and running in CI lanes
- [ ] Documentation reviewed
- [ ] Feature sign-off by QE

**Approvers:**

| Role               | Name | Date |
|:-------------------|:-----|:-----|
| QE Lead            | TBD  |      |
| Product Manager    | TBD  |      |
| Development Lead   | TBD  |      |
