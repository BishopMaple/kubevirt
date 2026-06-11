# Openshift-virtualization-tests Test plan

## **Live Update NAD Reference for VM Secondary Networks - Quality Engineering Plan**

### **Metadata & Tracking**

- **Enhancement(s):** [VEP 140 - Live Update of NAD Reference](https://github.com/kubevirt/enhancements/issues/140)
- **Feature in Jira:** [VIRTSTRAT-560 - Allow changing network VLAN on the fly](https://redhat.atlassian.net/browse/VIRTSTRAT-560)
- **Jira Tracking:** [CNV-72329 - Support changing the VM attached network NAD ref using hotplug](https://redhat.atlassian.net/browse/CNV-72329)
- **QE Owner(s):** [Name(s)]
- **Owning SIG:** sig-network
- **Participating SIGs:** sig-compute

**Document Conventions (if applicable):** NAD = Network Attachment Definition. VMI = VirtualMachineInstance. HCO = HyperConverged Operator. VLAN = Virtual LAN. LiveUpdateNADRef = feature gate controlling this feature.

### **Feature Overview**

This feature enables customers to change the network a running VM is connected to by updating the NAD (Network Attachment Definition) reference on a VM's secondary network interface, without requiring a VM restart. When the `LiveUpdateNADRef` feature gate is enabled and the VM rollout strategy is set to `LiveUpdate`, changing the NAD reference triggers an automatic live migration to apply the new network attachment. This allows VM administrators to swap a guest's uplink from one network to another (e.g., change VLAN) transparently, without the VM noticing any disruption.

---

### **I. Motivation and Requirements Review (QE Review Guidelines)**

This section documents the mandatory QE review process. The goal is to understand the feature's value, technology, and testability before formal test planning.

#### **1. Requirement & User Story Review Checklist**

- [ ] **Review Requirements** -- Reviewed the relevant requirements.
    - VEP 140 defines the LiveUpdateNADRef feature gate and the mechanism for updating NAD references via live migration.
    - CNV-72329 epic covers the downstream enablement with subtasks for upstream design, tests, and documentation.
    - Parent feature VIRTSTRAT-560 describes the high-level goal of changing network VLAN on the fly.

- [ ] **Understand Value** -- Confirmed clear user stories and understood. Understand the difference between U/S and D/S requirements. **What is the value of the feature for RH customers.**
    - User Story: "As a VM admin, I want to swap the guest's uplink from one network to another without the VM noticing, so they get better/worse link, different VLAN, or isolated segment."
    - Value: Eliminates VM downtime when changing network segments (e.g., VLAN reassignment), enabling seamless network infrastructure changes.

- [ ] **Testability** -- Confirmed requirements are **testable and unambiguous**.
    - Requirements are testable: NAD reference change can be verified via API patch, migration condition observation, and network connectivity checks.
    - Feature gate behavior is binary and verifiable.

- [ ] **Acceptance Criteria** -- Ensured acceptance criteria are **defined clearly** (clear user stories; D/S requirements clearly defined in Jira).
    - Acceptance criteria derived from VEP 140: NAD reference change on running VM triggers live migration, VM connectivity established on new network, no restart required.
    - Feature gate `LiveUpdateNADRef` controls behavior (Beta state).

- [ ] **Non-Functional Requirements (NFRs)** -- Confirmed coverage for NFRs, including Performance, Security, Usability, Downtime, Connectivity, Monitoring (alerts/metrics), Scalability, Portability (e.g., cloud support), and Docs.
    - Performance: Migration latency during NAD swap should be within standard live migration bounds.
    - Monitoring: MigrationRequired condition provides observability of the NAD swap process.
    - Docs: CNV-72336 tracks downstream documentation.

#### **2. Known Limitations**

- Feature is not tested with Multus Dynamic Networks Controller (as noted in PR #16412).
- Feature gate `LiveUpdateNADRef` is at Beta maturity; it is enabled by default but may be disabled.
- Only secondary (Multus) network interfaces support NAD reference live update; the pod network (default) interface does not support NAD swapping.
- SR-IOV interfaces trigger immediate migration (no grace period), which may differ from bridge-based behavior.
- The feature was at risk due to upstream freeze (per Jira comment) and a discovered bug blocking the HCO feature gate (resolved as of 2026/04/30).

#### **3. Technology and Design Review**

- [ ] **Developer Handoff/QE Kickoff** -- A meeting where Dev/Arch walked QE through the design, architecture, and implementation details. **Critical for identifying untestable aspects early.**
    - VEP 140 documents the architecture: VM controller detects NAD name change, migration evaluator triggers auto-migration, syncNetworks updates VMI spec post-migration.

- [ ] **Technology Challenges** -- Identified potential testing challenges related to the underlying technology.
    - Requires multi-node cluster with Multus CNI and bridge plugin for bridge-based NAD testing.
    - Migration must complete successfully for the NAD change to take effect; migration failures leave the VM on the old network.
    - Grace period logic in migration evaluator (`DynamicNetworkControllerGracePeriod`) affects timing of migration triggers.

- [ ] **Test Environment Needs** -- Determined necessary **test environment setups and tools**.
    - Minimum 2 schedulable nodes for live migration.
    - Multiple bridge-based NADs on different bridges/VLANs.
    - LiveUpdateNADRef feature gate must be configurable (enable/disable).

- [ ] **API Extensions** -- Reviewed new or modified APIs and their impact on testing.
    - No new API fields; existing `spec.template.spec.networks[].multus.networkName` field is used.
    - New feature gate `LiveUpdateNADRef` added to KubeVirt configuration.
    - VM rollout strategy `LiveUpdate` and workload update method `LiveMigrate` required.

- [ ] **Topology Considerations** -- Evaluated multi-cluster, network topology, and architectural impacts.
    - Single cluster topology sufficient for testing.
    - Network topology requires separate bridges/VLANs for source and target NADs.

### **II. Software Test Plan (STP)**

This STP serves as the **overall roadmap for testing**, detailing the scope, approach, resources, and schedule.

#### **1. Scope of Testing**

This STP covers testing of the Live Update NAD Reference feature, which allows changing the NAD reference on a running VM's secondary network interface without restart. Testing validates the feature gate behavior, auto-migration trigger, network connectivity after swap, interface property preservation, and integration with existing hotplug operations.

**Testing Goals**

- **[P0]** Verify NAD reference can be changed on a running VM's secondary interface without requiring restart
- **[P0]** Verify NAD reference change triggers automatic live migration and VM establishes connectivity on the new network
- **[P0]** Verify auto-injected pod network is preserved when secondary interface NAD reference is updated
- **[P1]** Verify guest interface name and MAC address are preserved after NAD reference swap
- **[P1]** Verify multiple NAD references can be updated simultaneously with single migration
- **[P1]** Verify feature gate correctly controls restart vs. migration behavior
- **[P1]** Verify non-NAD network field changes still require restart
- **[P1]** Verify error handling for invalid NAD references
- **[P2]** Verify NAD update behavior during concurrent migration
- **[P2]** Verify integration with hotplug/unplug operations

**Out of Scope (Testing Scope Exclusions)**

- [ ] **Multus CNI bridge plugin functionality** -- Platform-level: bridge creation and CNI attachment tested by network platform team. [ ] PM/Lead Agreement
- [ ] **Multus Dynamic Networks Controller integration** -- Explicitly not tested per VEP 140 implementation notes. [ ] PM/Lead Agreement
- [ ] **SR-IOV NAD reference live update** -- SR-IOV uses immediate migration path; separate testing scope. [ ] PM/Lead Agreement
- [ ] **Performance benchmarking of migration latency** -- Migration performance is tracked separately by the migration team. [ ] PM/Lead Agreement

#### **2. Test Strategy**

**Functional**

- [x] **Functional Testing** -- Validates NAD reference update, restart/migration behavior, feature gate control, interface property preservation, and error handling. Applicable: Y
- [x] **Automation Testing** -- All test cases will be automated as Ginkgo e2e tests (Tier 1) and pytest tests (Tier 2). Applicable: Y
- [x] **Regression Testing** -- Verifies that NAD reference changes do not break existing hotplug, hotunplug, or link state management features. Applicable: Y

**Non-Functional**

- [ ] **Performance Testing** -- Migration latency during NAD swap is expected to match standard live migration. No dedicated perf tests planned. Applicable: N/A
- [ ] **Scale Testing** -- Multi-interface NAD swap covered but large-scale (100+ VMs) not in scope. Applicable: N/A
- [ ] **Security Testing** -- No new RBAC or auth changes; existing VM edit permissions apply. Applicable: N/A
- [ ] **Usability Testing** -- No UI changes in this epic (UI tracked separately under CNV-82742). Applicable: N/A
- [ ] **Monitoring** -- MigrationRequired condition provides observability; no new metrics or alerts introduced. Applicable: N/A

**Integration & Compatibility**

- [x] **Compatibility Testing** -- Feature gate behavior validated in both enabled and disabled states. Applicable: Y
- [ ] **Upgrade Testing** -- Feature gate is Beta (enabled by default); upgrade path does not require special handling. Applicable: N/A
- [x] **Dependencies** -- Depends on Multus CNI for secondary network attachment. Bridge plugin required for bridge-based NADs. Applicable: Y
- [x] **Cross Integrations** -- Integration with hotplug/unplug operations and existing live migration infrastructure. Applicable: Y

**Infrastructure**

- [ ] **Cloud Testing** -- Feature uses standard bridge-based networking; no cloud-specific considerations. Applicable: N/A

#### **3. Test Environment**

- **Cluster Topology:** Multi-node cluster with at least 2 schedulable worker nodes (required for live migration)
- **OCP & OpenShift Virtualization Version(s):** OCP 4.22+ with OpenShift Virtualization 4.22+
- **CPU Virtualization:** Standard (VT-x or AMD-V enabled)
- **Compute Resources:** Minimum per worker node: 8 vCPUs, 32GB RAM
- **Special Hardware:** None required (bridge-based networking only)
- **Storage:** Default StorageClass with shared storage for live migration
- **Network:** OVN-Kubernetes (default CNI), Multus CNI with bridge plugin, minimum 2 bridge-based NADs on separate bridges
- **Required Operators:** OpenShift Virtualization Operator, HyperConverged Cluster Operator
- **Platform:** Bare metal (preferred for bridge networking) or cloud with bridge support
- **Special Configurations:** LiveUpdateNADRef feature gate enabled; VMRolloutStrategy set to LiveUpdate; WorkloadUpdateMethods includes LiveMigrate

#### **3.1. Testing Tools & Frameworks**

No new or special tools required beyond standard testing infrastructure.

#### **4. Entry Criteria**

- [ ] Requirements and design documents are **approved and merged** (VEP 140 merged)
- [ ] Test environment can be **set up and configured** with multi-node cluster and Multus bridge NADs
- [ ] LiveUpdateNADRef feature gate is available and configurable in HCO/KubeVirt configuration
- [ ] Core implementation PRs merged upstream (PR #16412, PR #17315)

#### **5. Risks**

- [ ] **Timeline/Schedule**
    - Risk: Feature gate was at risk due to upstream freeze and HCO bug blocking enablement
    - Mitigation: Bug resolved (GREEN status as of 2026/04/30); prioritize P0 scenarios first
    - Status: [ ]

- [ ] **Test Coverage**
    - Risk: Multus Dynamic Networks Controller integration not tested
    - Mitigation: Document as known limitation; test with standard Multus bridge plugin
    - Status: [ ]

- [ ] **Test Environment**
    - Risk: Requires multi-node cluster with specific bridge networking configuration
    - Mitigation: Use standard CI cluster topology with bridge NADs; document setup requirements
    - Status: [ ]

- [ ] **Untestable Aspects**
    - Risk: Cannot test production-scale VLAN changes across large VM fleets
    - Mitigation: Test with representative scenarios (single VM, multi-interface); document scale limitation
    - Status: [ ]

- [ ] **Resource Constraints**
    - Risk: Feature spans network controller, migration evaluator, and VM controller components
    - Mitigation: Focus automation on critical paths; leverage existing e2e test patterns
    - Status: [ ]

- [ ] **Dependencies**
    - Risk: Depends on Multus CNI and bridge plugin availability in test environment
    - Mitigation: Use standard OpenShift networking stack; bridge plugin is widely available
    - Status: [ ]

- [ ] **Other**
    - Risk: SR-IOV NAD swap uses different migration path (immediate vs. pending) and may require separate testing
    - Mitigation: Document SR-IOV as out of scope for initial release; track separately
    - Status: [ ]

---

### **III. Test Scenarios & Traceability**

- **Requirement ID:** CNV-72329
  **Requirement Summary:** NAD reference can be changed on a running VM's secondary network interface without requiring VM restart
  **Test Scenario(s):**
    - Verify NAD reference update on running VM without restart
    - Verify VM remains running during NAD reference change
    - Verify RestartRequired condition not set after NAD change
  **Tier:** Tier 1
  **Priority:** P0

- **Requirement ID:**
  **Requirement Summary:** NAD reference change triggers automatic live migration to apply the new network attachment
  **Test Scenario(s):**
    - Verify auto-migration triggered after NAD reference change
    - Verify MigrationRequired condition appears and resolves
    - Verify VM lands on different node after migration
  **Tier:** Tier 1
  **Priority:** P0

- **Requirement ID:**
  **Requirement Summary:** VM network connectivity is established on the new network after NAD reference live update completes
  **Test Scenario(s):**
    - Verify VM connectivity on new network after NAD swap
    - Verify VM unreachable on old network after NAD swap
  **Tier:** Tier 2
  **Priority:** P0

- **Requirement ID:**
  **Requirement Summary:** Guest interface name and MAC address are preserved after NAD reference live update
  **Test Scenario(s):**
    - Verify guest interface name preserved after NAD swap
    - Verify MAC address unchanged after NAD swap
  **Tier:** Tier 1
  **Priority:** P1

- **Requirement ID:**
  **Requirement Summary:** Multiple NAD references can be updated simultaneously on a VM with multiple secondary interfaces
  **Test Scenario(s):**
    - Verify simultaneous NAD updates on multiple interfaces
    - Verify single migration for multiple NAD changes
    - Verify all interfaces connect to new networks
  **Tier:** Tier 2
  **Priority:** P1

- **Requirement ID:**
  **Requirement Summary:** Auto-injected pod network is preserved when NAD reference live update is performed on secondary interfaces
  **Test Scenario(s):**
    - Verify pod network preserved during secondary NAD update
    - Verify pod network connectivity after NAD swap
  **Tier:** Tier 1 / Tier 2
  **Priority:** P0

- **Requirement ID:**
  **Requirement Summary:** LiveUpdateNADRef feature gate controls whether NAD reference changes trigger restart or live migration
  **Test Scenario(s):**
    - Verify NAD change requires restart when gate disabled
    - Verify NAD change triggers migration when gate enabled
  **Tier:** Tier 1
  **Priority:** P1

- **Requirement ID:**
  **Requirement Summary:** Non-NAD network field changes still require VM restart even when LiveUpdateNADRef is enabled
  **Test Scenario(s):**
    - Verify non-NAD network change still requires restart
    - Verify interface binding change requires restart
  **Tier:** Tier 1
  **Priority:** P1

- **Requirement ID:**
  **Requirement Summary:** NAD reference live update works correctly with VM rollout strategy set to LiveUpdate
  **Test Scenario(s):**
    - Verify NAD update with LiveUpdate rollout strategy
  **Tier:** Tier 1
  **Priority:** P1

- **Requirement ID:**
  **Requirement Summary:** [NEGATIVE] NAD reference change to a non-existent NAD is handled gracefully
  **Test Scenario(s):**
    - Verify error handling for non-existent target NAD
    - Verify VM stability after invalid NAD reference
  **Tier:** Tier 1
  **Priority:** P1

- **Requirement ID:**
  **Requirement Summary:** [NEGATIVE] NAD reference live update behavior during an ongoing migration
  **Test Scenario(s):**
    - Verify NAD update behavior during active migration
  **Tier:** Tier 2
  **Priority:** P2

- **Requirement ID:**
  **Requirement Summary:** NAD reference live update integrates correctly with existing hotplug/unplug operations
  **Test Scenario(s):**
    - Verify NAD swap after interface hotplug
    - Verify interface hotplug after NAD swap
  **Tier:** Tier 2
  **Priority:** P1

---

### **IV. Sign-off and Approval**

This Software Test Plan requires approval from the following stakeholders:

* **Reviewers:**
  - [Name / @github-username]
  - [Name / @github-username]
* **Approvers:**
  - [Name / @github-username]
  - [Name / @github-username]
