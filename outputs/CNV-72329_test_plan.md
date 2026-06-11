# Openshift-virtualization-tests Test plan

## **Live Update NAD Reference (Network Hotswap) - Quality Engineering Plan**

### **Metadata & Tracking**

| Field                  | Details                                                                                                          |
|:-----------------------|:-----------------------------------------------------------------------------------------------------------------|
| **Enhancement(s)**     | [VIRTSTRAT-560](https://redhat.atlassian.net/browse/VIRTSTRAT-560) - Allow changing network VLAN on the fly      |
| **Feature in Jira**    | [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) - Support changing the VM attached network NAD ref using hotplug |
| **Jira Tracking**      | [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) (Tasks must be created to **block the epic**)         |
| **QE Owner(s)**        | [Name(s)]                                                                                                        |
| **Owning SIG**         | sig-network                                                                                                      |
| **Participating SIGs** | sig-compute                                                                                                      |
| **Current Status**     | Draft                                                                                                            |

**Document Conventions:**
- **NAD** - NetworkAttachmentDefinition (Multus CRD defining a secondary network)
- **VLAN** - Virtual LAN segment
- **VMI** - VirtualMachineInstance (the running representation of a VM)
- **FG** - Feature Gate (`LiveUpdateNADRef`)
- **HCO** - HyperConverged Operator

### **Feature Overview**

This feature enables customers to change the network a running VM is connected to by updating the Multus NetworkAttachmentDefinition (NAD) reference on a VM's secondary network interface — without requiring a VM reboot. For example, a VM admin can change the VLAN ID by pointing a VM network to a different NAD that specifies the new VLAN. When the NAD reference is updated on the VM spec, the system automatically synchronizes the change to the VMI spec and triggers a live migration to apply the new network configuration transparently to the guest OS.

The feature is gated behind the `LiveUpdateNADRef` feature gate and involves changes to the VM controller sync logic (`syncNetworks`), the restart-required evaluator (`vmliveupdate`), and the migration evaluator (`migration.Evaluator`).

---

### **I. Motivation and Requirements Review (QE Review Guidelines)**

#### **1. Requirement & User Story Review Checklist**

| Check                                  | Done | Details/Notes                                                                                                                                                 | Comments |
|:---------------------------------------|:-----|:--------------------------------------------------------------------------------------------------------------------------------------------------------------|:---------|
| **Review Requirements**                | [x]  | Reviewed CNV-72329 and parent VIRTSTRAT-560. Feature allows changing network VLAN on the fly via NAD reference swap.                                          |          |
| **Understand Value**                   | [x]  | **User Story:** As a VM admin, I want to swap the guest's uplink from one network to another without the VM noticing, so they get a better/worse link, different VLAN, or isolated segment. This is a D/S feature enabling network agility for VM workloads. |          |
| **Customer Use Cases**                 | [x]  | Network segment migration (VLAN change), network isolation changes, network quality-of-service adjustments — all without VM downtime.                         |          |
| **Testability**                        | [x]  | Feature is testable: NAD reference can be changed via API, result is observable through VMI spec sync and automatic migration.                                |          |
| **Acceptance Criteria**                | [x]  | VM network NAD ref updated on VM spec is synced to VMI without restart; migration is triggered to apply the change; guest connectivity is preserved.          |          |
| **Non-Functional Requirements (NFRs)** | [x]  | Performance: migration should complete within standard live migration timeframes. Monitoring: existing migration metrics apply. No new UI requirements.        |          |

#### **2. Technology and Design Review**

| Check                            | Done | Details/Notes                                                                                                                                                                 | Comments |
|:---------------------------------|:-----|:------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:---------|
| **Developer Handoff/QE Kickoff** | [ ]  | Upstream design completed (CNV-72331 - Closed). Feature gate `LiveUpdateNADRef` controls the behavior. Key code paths: `syncNetworks()`, `IsRestartRequired()`, migration `Evaluator`. |          |
| **Technology Challenges**        | [x]  | Live migration is required to apply the NAD change at the pod level. Guest OS should not notice the change. NAD namespace resolution must be handled correctly.                |          |
| **Test Environment Needs**       | [x]  | Requires Multus with multiple NADs (different VLANs), multi-node cluster for migration.                                                                                       |          |
| **API Extensions**               | [x]  | No new API fields. Existing VM spec `networks[].multus.networkName` field is used. New feature gate `LiveUpdateNADRef` added.                                                  |          |
| **Topology Considerations**      | [x]  | Multi-node cluster required for live migration. NAD must be available on target node.                                                                                          |          |

---

### **II. Software Test Plan (STP)**

#### **1. Scope of Testing**

**Testing Goals**

- **[P0]** Verify that changing the NAD reference on a running VM's secondary network interface triggers automatic VMI spec synchronization and live migration without VM restart
- **[P0]** Verify that the `LiveUpdateNADRef` feature gate correctly enables/disables the live NAD update behavior
- **[P0]** Verify guest network connectivity is preserved after NAD reference change and migration completes
- **[P1]** Verify that NAD reference changes work correctly with bridge-binding network interfaces
- **[P1]** Verify that NAD reference changes interact correctly with existing network hotplug (add/remove) operations
- **[P1]** Verify migration evaluator triggers immediate migration when NAD reference differs between VMI spec and pod network status
- **[P2]** Verify NAD reference change behavior during concurrent VM operations (snapshot, other hotplug)
- **[P2]** Verify NAD namespace resolution handles both namespaced and non-namespaced NAD references

**Out of Scope (Testing Scope Exclusions)**

| Out-of-Scope Item                                                           | Rationale                                                                 | PM/Lead Agreement |
|:----------------------------------------------------------------------------|:--------------------------------------------------------------------------|:------------------|
| SR-IOV network NAD reference changes                                        | SR-IOV interfaces have different hotplug mechanics; separate feature work | [ ] Name/Date     |
| Pod network (default) NAD changes                                           | Feature only applies to secondary (Multus) networks                       | [ ] Name/Date     |
| NAD reference changes on stopped VMs                                        | Stopped VMs apply changes on next boot; no live update needed             | [ ] Name/Date     |
| Performance/scale testing (100+ concurrent NAD changes)                     | Standard migration performance applies; no new performance requirements   | [ ] Name/Date     |
| UI testing for NAD reference changes                                        | Covered by CNV-82742 (separate UI epic)                                   | [ ] Name/Date     |

#### **2. Test Strategy**

| Item                           | Description                                                                                                                   | Applicable (Y/N or N/A) | Comments                                                                  |
|:-------------------------------|:------------------------------------------------------------------------------------------------------------------------------|:------------------------|:--------------------------------------------------------------------------|
| Functional Testing             | Validates NAD ref change triggers sync + migration, FG gating, namespace resolution                                           | Y                       | Core focus                                                                |
| Automation Testing             | All test cases will be automated in openshift-virtualization-tests                                                             | Y                       | Tier 1 and Tier 2                                                         |
| Performance Testing            | Migration latency with NAD change                                                                                             | N/A                     | Standard migration perf applies                                           |
| Security Testing               | RBAC for VM spec modification                                                                                                 | N/A                     | Existing RBAC model applies                                               |
| Usability Testing              | No UI changes in this epic                                                                                                    | N/A                     | UI tracked under CNV-82742                                                |
| Compatibility Testing          | Bridge-binding interfaces, different CNI plugins                                                                              | Y                       | OVN-Kubernetes primary                                                    |
| Regression Testing             | Existing network hotplug (add/remove) must not regress                                                                        | Y                       | Critical                                                                  |
| Upgrade Testing                | FG behavior after upgrade from version without LiveUpdateNADRef                                                               | Y                       | Verify FG default state post-upgrade                                      |
| Backward Compatibility Testing | VMs created before FG enablement                                                                                              | Y                       | Existing VMs should work when FG is enabled                               |
| Dependencies                   | Multus CNI, OVN-Kubernetes, HCO (for FG management)                                                                           | Y                       | HCO feature gate noted as risk (bug discovered per Jira comments)         |
| Cross Integrations             | Live migration, network hotplug, VM lifecycle                                                                                  | Y                       | Migration evaluator integration                                           |
| Monitoring                     | Existing migration metrics and alerts                                                                                          | N/A                     | No new metrics required                                                   |
| Cloud Testing                  | Bare metal primary; cloud platforms secondary                                                                                  | N/A                     | Secondary networks are primarily bare-metal                               |

#### **3. Test Environment**

| Environment Component                         | Configuration                                                                       |
|:----------------------------------------------|:------------------------------------------------------------------------------------|
| **Cluster Topology**                          | Multi-node (minimum 3-master / 2-worker bare-metal)                                 |
| **OCP & OpenShift Virtualization Version(s)** | OCP 4.22+ with OpenShift Virtualization 4.22+                                       |
| **CPU Virtualization**                        | Nodes with VT-x (Intel) or AMD-V (AMD) enabled in BIOS                             |
| **Compute Resources**                         | Minimum per worker node: 8 vCPUs, 32GB RAM                                         |
| **Special Hardware**                          | N/A                                                                                 |
| **Storage**                                   | Default StorageClass (ocs-storagecluster-ceph-rbd or equivalent)                    |
| **Network**                                   | OVN-Kubernetes (default), Multus with multiple NADs (different VLANs), Linux Bridge |
| **Required Operators**                        | OpenShift Virtualization Operator, NMState Operator (for NAD/VLAN configuration)    |
| **Platform**                                  | Bare metal                                                                          |
| **Special Configurations**                    | `LiveUpdateNADRef` feature gate enabled via HCO CR                                  |

#### **3.1. Testing Tools & Frameworks**

| Category           | Tools/Frameworks                       |
|:-------------------|:---------------------------------------|
| **Test Framework** | pytest (openshift-virtualization-tests) |
| **CI/CD**          | Standard Prow CI lanes                 |
| **Other Tools**    | virtctl, oc                            |

#### **4. Entry Criteria**

The following conditions must be met before testing can begin:

- [x] Requirements and design documents are **approved and merged** (CNV-72331 Closed)
- [ ] `LiveUpdateNADRef` feature gate is available and functional in HCO
- [ ] Test environment can be **set up and configured** with multiple NADs on different VLANs
- [ ] Upstream unit tests for `syncNetworks`, `IsRestartRequired`, and migration evaluator are passing

#### **5. Risks**

| Risk Category        | Specific Risk for This Feature                                                                                        | Mitigation Strategy                                                                         | Status |
|:---------------------|:----------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------------------------------|:-------|
| Timeline/Schedule    | HCO feature gate was blocked by a discovered bug (per Jira status update 2026-04-13)                                  | Monitor HCO fix progress; test with direct KubeVirt FG as fallback                          | [x]    |
| Test Coverage        | Cannot test all possible NAD/VLAN combinations                                                                        | Focus on representative VLAN swap scenarios; test namespace edge cases                       | [ ]    |
| Test Environment     | Requires multiple pre-configured NADs with different VLANs on same cluster                                            | Automate NAD creation in test fixtures                                                       | [ ]    |
| Untestable Aspects   | Guest OS kernel-level network stack behavior during migration                                                          | Verify connectivity post-migration; rely on upstream kernel/QEMU testing                     | [ ]    |
| Resource Constraints | Multi-node cluster required for live migration                                                                         | Use shared CI infrastructure with multi-node topology                                        | [ ]    |
| Dependencies         | Depends on Multus CNI and OVN-Kubernetes for secondary network management                                             | Ensure test environment has correct CNI configuration                                        | [ ]    |
| Other                | Feature gate default state may change between releases                                                                 | Test both FG enabled and disabled scenarios                                                  | [ ]    |

#### **6. Known Limitations**

- Feature only supports secondary (Multus) network interfaces; the pod (default) network cannot be changed
- NAD reference change requires live migration to take effect at the pod level; the VM will experience a brief migration window
- SR-IOV interfaces are not supported for NAD reference swap in this release
- The feature is gated behind `LiveUpdateNADRef` and must be explicitly enabled

---

### **III. Test Scenarios & Traceability**

| Requirement ID | Requirement Summary                                                                                | Test Scenario(s)                                                                                                    | Tier   | Priority |
|:---------------|:---------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------------------------------------------------------|:-------|:---------|
| CNV-72329      | As a VM admin, I want to swap the guest's uplink from one network to another without the VM noticing | TS-CNV-72329-001: Verify NAD reference change on running VM triggers VMI sync and migration                         | Tier 1 | P0       |
| CNV-72329      | Feature gate controls live NAD update behavior                                                      | TS-CNV-72329-002: Verify `LiveUpdateNADRef` FG enabled allows NAD change without restart                            | Tier 1 | P0       |
| CNV-72329      | Feature gate controls live NAD update behavior                                                      | TS-CNV-72329-003: [NEGATIVE] Verify `LiveUpdateNADRef` FG disabled requires VM restart for NAD change               | Tier 1 | P0       |
| CNV-72329      | Guest connectivity preserved after NAD swap                                                         | TS-CNV-72329-004: Verify guest network connectivity is maintained after NAD reference change and migration completes | Tier 2 | P0       |
| CNV-72329      | NAD swap to different VLAN                                                                          | TS-CNV-72329-005: Verify VM can communicate on new VLAN after NAD reference change                                  | Tier 2 | P0       |
| CNV-72329      | VMI spec synchronization                                                                            | TS-CNV-72329-006: Verify VMI spec networks are updated to reflect new NAD reference from VM spec                    | Tier 1 | P0       |
| CNV-72329      | Migration evaluator detects NAD mismatch                                                            | TS-CNV-72329-007: Verify migration evaluator triggers immediate migration when NAD reference differs between VMI and pod | Tier 1 | P1   |
| CNV-72329      | NAD namespace resolution                                                                            | TS-CNV-72329-008: Verify NAD reference change works with fully-qualified (namespace/name) NAD references            | Tier 1 | P1       |
| CNV-72329      | NAD namespace resolution                                                                            | TS-CNV-72329-009: Verify NAD reference change works with short (name-only) NAD references in same namespace         | Tier 1 | P1       |
| CNV-72329      | Multiple secondary interfaces                                                                       | TS-CNV-72329-010: Verify NAD reference change on one interface does not affect other secondary interfaces           | Tier 2 | P1       |
| CNV-72329      | NAD change with bridge binding                                                                      | TS-CNV-72329-011: Verify NAD reference change works correctly with bridge-binding interfaces                        | Tier 2 | P1       |
| CNV-72329      | Interaction with network hotplug                                                                    | TS-CNV-72329-012: Verify NAD reference change after hotplugging a new network interface                             | Tier 2 | P1       |
| CNV-72329      | Interaction with network hotunplug                                                                  | TS-CNV-72329-013: Verify NAD reference change is rejected/ignored for interfaces marked as absent (hot-unplugged)   | Tier 2 | P1       |
| CNV-72329      | Restart not required when FG enabled                                                                | TS-CNV-72329-014: Verify `IsRestartRequired` returns false for NAD-only changes when FG is enabled                  | Tier 1 | P1       |
| CNV-72329      | Non-Multus networks unchanged                                                                       | TS-CNV-72329-015: Verify pod network (non-Multus) is not affected by syncNetworks                                   | Tier 1 | P1       |
| CNV-72329      | Upgrade scenario                                                                                    | TS-CNV-72329-016: Verify existing VMs work correctly after upgrading to version with `LiveUpdateNADRef` FG          | Tier 2 | P2       |
| CNV-72329      | [NEGATIVE] Invalid NAD reference                                                                    | TS-CNV-72329-017: [NEGATIVE] Verify changing NAD reference to non-existent NAD is handled gracefully                | Tier 2 | P2       |
| CNV-72329      | [NEGATIVE] NAD change during migration                                                              | TS-CNV-72329-018: [NEGATIVE] Verify NAD reference change during an in-progress migration is handled correctly       | Tier 2 | P2       |
| CNV-72329      | Concurrent operations                                                                               | TS-CNV-72329-019: Verify NAD reference change does not interfere with concurrent disk hotplug                       | Tier 2 | P2       |
| CNV-72329      | VM with ordinal network naming                                                                      | TS-CNV-72329-020: Verify NAD reference change works correctly with ordinal interface naming scheme                  | Tier 1 | P2       |

---

### **IV. Sign-off and Approval**

This Software Test Plan requires approval from the following stakeholders:

* **Reviewers:**
  - [Name / @github-username]
  - [Name / @github-username]
* **Approvers:**
  - [Name / @github-username]
  - [Name / @github-username]
