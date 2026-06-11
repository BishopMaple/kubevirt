# Openshift-virtualization-tests Test plan

## **Support Changing the VM Attached Network NAD Ref Using Hotplug - Quality Engineering Plan**

### **Metadata & Tracking**

- **Enhancement(s):** [VEP 140 - Live Update NAD Reference](https://github.com/kubevirt/enhancements/issues/140)
- **Feature in Jira:** [VIRTSTRAT-560 - Allow changing network VLAN on the fly](https://redhat.atlassian.net/browse/VIRTSTRAT-560)
- **Jira Tracking:** [CNV-72329 - Support changing the VM attached network NAD ref using hotplug](https://redhat.atlassian.net/browse/CNV-72329)
- **QE Owner(s):** Ananya Banerjee
- **Owning SIG:** sig-network
- **Participating SIGs:** sig-compute (migration integration)

**Document Conventions:** NAD = Network Attachment Definition; VMI = VirtualMachineInstance; FG = Feature Gate; LiveUpdateNADRef = the feature gate controlling this feature (Beta in v1.8).

### **Feature Overview**

This feature allows customers to change the Network Attachment Definition (NAD) reference on a running VM's secondary network interface without requiring a VM reboot. When a user updates the `networkName` field in the VM spec (e.g., to switch VLANs), the change is propagated to the VMI spec and applied via an automatic live migration. The feature is gated behind the `LiveUpdateNADRef` feature gate (Beta) and requires the VM rollout strategy to be set to `LiveUpdate` with `LiveMigrate` as the workload update method.

---

### **I. Motivation and Requirements Review (QE Review Guidelines)**

#### **1. Requirement & User Story Review Checklist**

- [ ] **Review Requirements** -- Reviewed the relevant requirements.
  - CNV-72329 epic describes the goal: change NAD reference on running VM without reboot.
  - Parent feature VIRTSTRAT-560 provides value context: allow changing VLAN without reboot.
  - VEP 140 upstream enhancement defines technical design.
  - UI counterpart CNV-82742 covers console-based NAD editing.

- [ ] **Understand Value** -- Confirmed clear user stories and understood. Understand the value and customer use cases.
  - User story: "As a VM admin, I want to swap the guest's uplink from one network to another without the VM noticing, so they get better/worse link, different VLAN, or isolated segment."
  - Value: eliminates downtime for network reconfiguration, enables dynamic VLAN management.
  - Upstream and downstream requirements are aligned.

- [ ] **Testability** -- Confirmed requirements are **testable and unambiguous**.
  - Core behavior is testable: patch VM spec NAD reference, observe migration, verify connectivity.
  - Feature gate provides clear toggle for feature-on/off testing.
  - Upstream e2e tests in `tests/network/nad_live_update.go` validate the approach.
  - Note: upstream tests are currently quarantined due to flaking console login and ping (PR #17084, issue #17086).

- [ ] **Acceptance Criteria** -- Ensured acceptance criteria are **defined clearly**.
  - No explicit acceptance criteria in Jira; derived from VEP 140 and implementation PRs.
  - Implicit criteria: NAD ref change triggers migration, connectivity on new network, no restart required when FG enabled.

- [ ] **Non-Functional Requirements (NFRs)** -- Confirmed coverage for NFRs.
  - Performance: migration duration during NAD swap is bounded by standard migration SLAs.
  - Security: RBAC controls who can edit VM network spec (existing VM RBAC applies).
  - Monitoring: MigrationRequired condition provides observability.
  - No specific usability, scalability, or portability NFRs identified beyond standard CNV requirements.

#### **2. Known Limitations**

- Feature is not tested with Multus Dynamic Networks Controller (noted in PR #16412).
- Only secondary (Multus) networks support NAD reference live update; the default pod network is not affected.
- The VM rollout strategy must be set to `LiveUpdate` and workload update method to `LiveMigrate`; other strategies require restart.
- Upstream e2e tests are currently quarantined due to test stability issues (console login and ping flaking, not product bugs -- see #17086).
- SR-IOV interface NAD swap behavior is not explicitly tested in the current upstream implementation.

#### **3. Technology and Design Review**

- [ ] **Developer Handoff/QE Kickoff** -- A meeting where Dev/Arch walked QE through the design, architecture, and implementation details.
  - Implementation spans: VM controller (`pkg/network/controllers/vm.go`), restart evaluator (`pkg/network/vmliveupdate/restart.go`), migration evaluator (`pkg/network/migration/evaluator.go`), and feature gate registration.
  - Key PRs: #14602 (allow live changes to network fields), #16412 (implement live update of NAD reference).

- [ ] **Technology Challenges** -- Identified potential testing challenges related to the underlying technology.
  - Live migration timing and flakiness: upstream tests quarantined due to console login and ping instability.
  - Multi-node cluster requirement: at least 2 schedulable nodes needed for migration.
  - Network bridge setup: test requires creating two separate bridge-based NADs with distinct subnets.

- [ ] **Test Environment Needs** -- Determined necessary test environment setups and tools.
  - Multi-node cluster with at least 2 schedulable worker nodes.
  - Bridge CNI plugin for creating test NADs.
  - Network isolation between test bridge domains.

- [ ] **API Extensions** -- Reviewed new or modified APIs and their impact on testing.
  - No new API fields; feature uses existing `spec.template.spec.networks[].multus.networkName` field.
  - New feature gate: `LiveUpdateNADRef` (Beta in v1.8, enabled by default).
  - New cluster config method: `LiveUpdateNADRefEnabled()` on `clusterConfigurer` interface.

- [ ] **Topology Considerations** -- Evaluated multi-cluster, network topology, and architectural impacts.
  - Single-cluster feature; no multi-cluster considerations.
  - Requires bridge-based secondary networks with distinct bridge names and subnets.
  - Migration target node must have the target NAD's bridge available.

### **II. Software Test Plan (STP)**

#### **1. Scope of Testing**

This STP covers testing of the NAD reference live update feature for OpenShift Virtualization 4.22. Testing validates that secondary network NAD references can be changed on running VMs, triggering automatic live migration to apply the change, while preserving interface identity and establishing connectivity on the new network.

**Testing Goals**

- **[P0]** Verify NAD reference change on a running VM triggers automatic live migration and establishes connectivity on the target network
- **[P0]** Verify MigrationRequired condition lifecycle (appears on NAD change, clears after migration)
- **[P0]** Verify VM can communicate with peers on the new network after NAD swap completes
- **[P1]** Verify interface name and MAC address are preserved after NAD swap and migration
- **[P1]** Verify feature gate controls: NAD swap requires restart when `LiveUpdateNADRef` is disabled
- **[P1]** Verify multiple secondary NAD references can be updated simultaneously
- **[P1]** Verify NAD swap works correctly alongside interface hotplug/hotunplug operations
- **[P1]** Verify rollout strategy requirements (LiveUpdate vs Staging)
- **[P2]** Verify NAD swap functionality persists through OCP/CNV upgrade
- **[P2]** Verify UI supports NAD reference editing (CNV-82742)

**Out of Scope (Testing Scope Exclusions)**

- [ ] **Multus Dynamic Networks Controller integration** -- Feature explicitly not tested with Dynamic Networks Controller per PR #16412; out of scope for initial release.
- [ ] **SR-IOV NAD swap** -- SR-IOV interface NAD swap is not covered by upstream implementation tests; requires separate investigation.
- [ ] **Performance benchmarking** -- Migration performance during NAD swap follows standard migration SLAs; no dedicated performance testing planned.
- [ ] **Multi-cluster scenarios** -- Feature is single-cluster; multi-cluster testing not applicable.

#### **2. Test Strategy**

**Functional**

- [x] **Functional Testing** -- Validates NAD reference live update works according to specified requirements and user stories. Covers feature gate behavior, rollout strategy validation, condition lifecycle, and interface identity preservation.
- [x] **Automation Testing** -- All test cases will be automated. Tier 1 tests in Go (Ginkgo) in the kubevirt/kubevirt repo; Tier 2 tests in Python (pytest) in the openshift-virtualization-tests repo.
- [x] **Regression Testing** -- Existing network hotplug, hotunplug, and link state tests must continue to pass with the new feature gate enabled.

**Non-Functional**

- [ ] **Performance Testing** -- N/A. Migration performance follows standard SLAs; no feature-specific performance requirements.
- [ ] **Scale Testing** -- N/A. Feature operates on individual VM network interfaces; standard scale coverage applies.
- [ ] **Security Testing** -- N/A. Feature uses existing VM RBAC permissions; no new security surface.
- [ ] **Usability Testing** -- Covered by UI epic CNV-82742. UI should show pending changes label when NAD reference is edited.
- [ ] **Monitoring** -- MigrationRequired condition provides observability. No new metrics or alerts required.

**Integration & Compatibility**

- [x] **Compatibility Testing** -- Verify feature works across supported OCP versions (4.22+) and network CNI configurations (OVN-Kubernetes with bridge secondary networks).
- [x] **Upgrade Testing** -- Verify VMs with changed NAD references continue to function after OCP/CNV upgrade (CNV-72333).
- [x] **Dependencies** -- Depends on Multus CNI (network attachment), virt-controller (VM sync), and migration controller (auto-migration). All are within CNV scope.
- [x] **Cross Integrations** -- Feature interacts with NIC hotplug/hotunplug (same sync cycle in VM controller) and live migration (migration evaluator). Both tested.

**Infrastructure**

- [ ] **Cloud Testing** -- N/A. Feature requires bridge-based secondary networks; standard bare-metal testing applies.

#### **3. Test Environment**

- **Cluster Topology:** Multi-node cluster with minimum 3 masters and 2+ schedulable worker nodes
- **OCP & OpenShift Virtualization Version(s):** OCP 4.22 with OpenShift Virtualization 4.22
- **CPU Virtualization:** Nodes with VT-x (Intel) or AMD-V (AMD) enabled in BIOS
- **Compute Resources:** Minimum per worker node: 8 vCPUs, 32GB RAM
- **Special Hardware:** None required; bridge CNI plugin must be available
- **Storage:** Default StorageClass available for VM boot disks
- **Network:** OVN-Kubernetes (default CNI); bridge CNI plugin for secondary networks; two distinct bridge-based NADs required per test
- **Required Operators:** OpenShift Virtualization Operator (openshift-cnv namespace)
- **Platform:** Bare metal (bridge CNI requirement)
- **Special Configurations:** `LiveUpdateNADRef` feature gate enabled (Beta, default on); VM rollout strategy set to `LiveUpdate`; workload update method set to `LiveMigrate`

#### **3.1. Testing Tools & Frameworks**

No new or additional testing tools required beyond standard infrastructure.

#### **4. Entry Criteria**

- [ ] Requirements and design documents are **approved and merged** (VEP 140 merged upstream)
- [ ] Test environment can be **set up and configured** with multi-node cluster and bridge CNI
- [ ] Core implementation PRs merged upstream (#14602, #16412)
- [ ] `LiveUpdateNADRef` feature gate registered and available in cluster config
- [ ] Upstream e2e test quarantine issues resolved (#17086)

#### **5. Risks**

- [ ] **Timeline/Schedule**
  - Risk: Test automation not complete yet (status YELLOW per QE lead as of 2026-06-01)
  - Mitigation: Prioritize P0 scenarios, leverage upstream test patterns from `nad_live_update.go`
  - Status: [ ] Open

- [ ] **Test Coverage**
  - Risk: SR-IOV NAD swap and Multus Dynamic Networks Controller not covered
  - Mitigation: Document as known limitation; address in future release if customer demand exists
  - Status: [ ] Open

- [ ] **Test Environment**
  - Risk: Bridge CNI availability varies across test environments
  - Mitigation: Use standard bare-metal test clusters with pre-configured bridge CNI
  - Status: [ ] Open

- [ ] **Untestable Aspects**
  - Risk: Multus Dynamic Networks Controller interaction explicitly untested per upstream
  - Mitigation: Document limitation; monitor for customer-reported issues
  - Status: [ ] Open

- [ ] **Resource Constraints**
  - Risk: Single QE engineer assigned; feature spans networking, migration, and VM controller components
  - Mitigation: Leverage upstream test patterns; coordinate with sig-network peers
  - Status: [ ] Open

- [ ] **Dependencies**
  - Risk: Upstream test stability (quarantined tests due to console login/ping flaking)
  - Mitigation: PR #17087 fixes flaking; PR #17904 adds additional verification; monitor CI stability
  - Status: [ ] Open

- [ ] **Other**
  - Risk: Feature gate is Beta (enabled by default); unexpected interactions with existing network features
  - Mitigation: Run full network regression suite with feature gate enabled
  - Status: [ ] Open

---

### **III. Test Scenarios & Traceability**

- **Requirement:** [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) -- NAD reference can be changed on a running VM's secondary network without reboot
  - Verify NAD reference change on running VM triggers live migration -- **Tier 2** -- P0
  - Verify VM connectivity on new network after NAD swap -- **Tier 2** -- P0
  - [NEGATIVE] Verify error when target NAD does not exist -- **Tier 1** -- P0
  - Verify NAD swap during active network workload -- **Tier 2** -- P0

- **Requirement:** [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) -- Live migration is triggered automatically when NAD reference changes
  - Verify MigrationRequired condition appears after NAD update -- **Tier 1** -- P0
  - Verify MigrationRequired condition clears after migration completes -- **Tier 1** -- P0
  - [NEGATIVE] Verify migration not triggered when NAD ref unchanged -- **Tier 1** -- P0

- **Requirement:** [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) -- VM network connectivity established on new network after NAD swap
  - Verify ping to peer VM on target network after swap -- **Tier 2** -- P0
  - [NEGATIVE] Verify no connectivity to old network peers after swap -- **Tier 2** -- P0

- **Requirement:** [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) -- Interface name and MAC address preserved after NAD hotplug
  - Verify guest interface name unchanged after NAD swap -- **Tier 1** -- P1
  - Verify MAC address preserved after NAD swap and migration -- **Tier 1** -- P1

- **Requirement:** [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) -- Multiple secondary network NAD references updated simultaneously
  - Verify concurrent NAD reference updates on multiple interfaces -- **Tier 2** -- P1
  - [NEGATIVE] Verify partial update when one target NAD invalid -- **Tier 1** -- P1

- **Requirement:** [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) -- NAD change has no effect when LiveUpdateNADRef feature gate disabled
  - Verify RestartRequired condition when feature gate disabled -- **Tier 1** -- P1
  - Verify NAD swap works after enabling feature gate at runtime -- **Tier 1** -- P1

- **Requirement:** [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) -- VM rollout strategy must be LiveUpdate for NAD swap without restart
  - Verify NAD swap with LiveUpdate rollout strategy -- **Tier 1** -- P1
  - Verify RestartRequired with Staging rollout strategy -- **Tier 1** -- P1

- **Requirement:** [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) -- Non-Multus (pod) networks unaffected by NAD reference updates
  - Verify default pod network unchanged during NAD swap -- **Tier 1** -- P1

- **Requirement:** [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) -- NAD swap coexists with hotplug/hotunplug operations
  - Verify NAD swap with concurrent interface hotplug -- **Tier 2** -- P1
  - Verify NAD swap with concurrent interface hotunplug -- **Tier 2** -- P1

- **Requirement:** [CNV-82742](https://redhat.atlassian.net/browse/CNV-82742) -- UI supports changing NAD reference on VM network interfaces
  - Verify UI NAD reference edit shows pending changes -- **Tier 2** -- P2

- **Requirement:** [CNV-72333](https://redhat.atlassian.net/browse/CNV-72333) -- NAD reference live update survives OCP/CNV upgrade
  - Verify NAD swap functionality after cluster upgrade -- **Tier 2** -- P2
  - Verify VMs with changed NADs persist through upgrade -- **Tier 2** -- P2

---

### **IV. Sign-off and Approval**

* **Reviewers:**
  - [QE Reviewer / @github-username]
  - [QE Reviewer / @github-username]
* **Approvers:**
  - [QE Lead / @github-username]
  - [SIG Network Lead / @github-username]
