# Openshift-virtualization-tests Test plan

## **Support Changing the VM Attached Network NAD Ref Using Hotplug - Quality Engineering Plan**

### **Metadata & Tracking**

| Field                  | Details                                                                                                    |
|:-----------------------|:-----------------------------------------------------------------------------------------------------------|
| **Enhancement(s)**     | [VIRTSTRAT-560](https://issues.redhat.com/browse/VIRTSTRAT-560) - Allow changing network VLAN on the fly   |
| **Feature in Jira**    | [CNV-60118](https://issues.redhat.com/browse/CNV-60118) - Reattach a VM interface to a different network   |
| **Jira Tracking**      | [CNV-72329](https://issues.redhat.com/browse/CNV-72329) (Epic)                                            |
| **QE Owner(s)**        | [To be assigned]                                                                                           |
| **Owning SIG**         | sig-network                                                                                                |
| **Participating SIGs** | sig-compute (VM lifecycle/migration)                                                                       |

**Document Conventions (if applicable):**
- **NAD** — Network Attachment Definition (Multus CRD for secondary network configuration)
- **NAD ref** — The `networkName` field in the VM spec that references a NAD
- **NAD hotplug** — Changing the NAD reference on a running VM without reboot (triggers live migration)
- **LiveUpdateNADRef** — KubeVirt feature gate (Beta) controlling NAD reference live update behavior

### **Feature Overview**

This feature allows customers to change the Network Attachment Definition (NAD) reference for a running VM's secondary network interface without rebooting the VM. When the `networkName` field in the VM spec is updated (e.g., to switch VLAN by pointing to a different NAD), KubeVirt automatically propagates the change to the VMI spec and triggers a live migration to apply the new network attachment on the target node. The guest interface name and MAC address are preserved throughout the operation, making the network switch transparent to the guest OS. This capability is controlled by the `LiveUpdateNADRef` feature gate, which is in Beta state (enabled by default).

---

### **I. Motivation and Requirements Review (QE Review Guidelines)**

This section documents the mandatory QE review process. The goal is to understand the feature's value, technology, and testability before formal test planning.

#### **1. Requirement & User Story Review Checklist**

| Check                                  | Done | Details/Notes                                                                                                                                                                           | Comments |
|:---------------------------------------|:-----|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:---------|
| **Review Requirements**                | [x]  | Reviewed CNV-72329 (Epic), VIRTSTRAT-560 (Feature), and CNV-60118 (Customer RFE). Feature allows changing VM secondary network NAD reference without reboot.                           |          |
| **Understand Value**                   | [x]  | Customer value: VM admins can swap guest uplinks between networks (e.g., change VLAN) without VM downtime. Addresses customer RFE CNV-60118 for switching NADs without restart.         |          |
| **Customer Use Cases**                 | [x]  | As a VM admin, swap guest uplink from one network to another without the VM noticing — for better/worse link, different VLAN, or isolated segment.                                      |          |
| **Testability**                        | [x]  | Feature is testable: patch VM spec `networkName` field, observe migration trigger, verify connectivity on target network, verify interface identity preservation.                        |          |
| **Acceptance Criteria**                | [ ]  | Acceptance criteria not explicitly defined in Jira. Derived from implementation: NAD change triggers migration, connectivity preserved, interface identity preserved.                    | Recommend adding explicit AC to CNV-72329 |
| **Non-Functional Requirements (NFRs)** | [ ]  | No explicit NFRs defined. Considerations: migration duration during NAD change, zero-downtime guarantee, monitoring/alerts for NAD change operations.                                   | Recommend defining performance baseline for NAD change latency |

#### **2. Technology and Design Review**

| Check                            | Done | Details/Notes                                                                                                                                           | Comments |
|:---------------------------------|:-----|:--------------------------------------------------------------------------------------------------------------------------------------------------------|:---------|
| **Developer Handoff/QE Kickoff** | [ ]  | Upstream design completed (CNV-72331 Closed). Implementation spans virt-controller (VM sync, migration evaluation) and network controllers.             | Schedule dev handoff to walk through migration evaluation flow |
| **Technology Challenges**        | [x]  | NAD change requires live migration to take effect. Migration evaluation must correctly compare namespace-qualified vs unqualified NAD names. Feature gate `LiveUpdateNADRef` (Beta) controls behavior. | |
| **Test Environment Needs**       | [x]  | Requires multi-node cluster (minimum 2 schedulable nodes) for migration. Requires Multus with bridge CNI plugin. Multiple NADs with different configurations needed. | |
| **API Extensions**               | [x]  | No new API fields. Uses existing `spec.template.spec.networks[].multus.networkName` field. Change is in controller behavior when `LiveUpdateNADRef` feature gate is enabled. | |
| **Topology Considerations**      | [x]  | Single-cluster only. Requires nodes with same network bridge availability for migration. No multi-cluster or HCP-specific considerations.                | |


### **II. Software Test Plan (STP)**

This STP serves as the **overall roadmap for testing**, detailing the scope, approach, resources, and schedule.

#### **1. Scope of Testing**

This STP covers testing of the NAD reference live update feature for secondary VM network interfaces. Testing will validate the end-to-end flow: NAD reference change on VM spec, automatic migration trigger, network connectivity on target NAD, and guest interface identity preservation. Both the feature-enabled and feature-disabled paths will be tested.

**Testing Goals**

- **[P0]** Verify NAD reference change on a running VM triggers live migration and establishes connectivity on the target network without VM reboot
- **[P0]** Verify guest interface properties (name, MAC address) are preserved after NAD hotplug and migration
- **[P0]** Verify VM connectivity on the new network after NAD change completes
- **[P1]** Verify `LiveUpdateNADRef` feature gate correctly controls live update vs restart behavior
- **[P1]** Verify non-NAD network changes still require VM restart even with feature gate enabled
- **[P1]** Verify NAD name resolution works with both namespace-qualified and unqualified references
- **[P1]** Verify graceful error handling when target NAD does not exist
- **[P1]** Verify existing VMs remain functional after cluster upgrade with LiveUpdateNADRef
- **[P2]** Verify pod-network-only VMs are unaffected by NAD live update feature

**Out of Scope (Testing Scope Exclusions)**

| Out-of-Scope Item                                                    | Rationale                                                                         | PM/Lead Agreement |
|:---------------------------------------------------------------------|:----------------------------------------------------------------------------------|:------------------|
| UI support for NAD reference change                                  | Separate epic CNV-82742 with its own test plan                                    | [ ] Name/Date     |
| Multus CNI network attachment internals                              | Platform-level — tested by network platform team                                  | [ ] Name/Date     |
| NAD CRD lifecycle management                                         | Platform-level — tested by Multus/network operator team                           | [ ] Name/Date     |
| SR-IOV NAD hotplug                                                   | SR-IOV hotplug uses immediate migration path; bridge-based NAD is primary scope   | [ ] Name/Date     |
| Performance benchmarking of migration latency                        | No explicit NFR defined; can be added as follow-up                                | [ ] Name/Date     |

#### **2. Test Strategy**

| Item                           | Description                                                                                                                                                  | Applicable (Y/N or N/A) | Comments |
|:-------------------------------|:-------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------|:---------|
| Functional Testing             | Validate NAD ref change triggers migration, connectivity preserved, interface identity preserved, feature gate behavior                                       | Y                       | Primary focus |
| Automation Testing             | All test scenarios will be automated in upstream kubevirt (Tier 1/Ginkgo) and downstream openshift-virtualization-tests (Tier 2/pytest)                       | Y                       | CNV-72335 tracks automation |
| Performance Testing            | No explicit performance NFRs defined for migration latency during NAD change                                                                                 | N/A                     | Recommend defining baseline in future |
| Security Testing               | No new RBAC or auth changes; uses existing VM patch permissions                                                                                              | N/A                     | Existing RBAC coverage sufficient |
| Usability Testing              | UI testing covered by separate epic CNV-82742                                                                                                                | N/A                     | Out of scope for this STP |
| Compatibility Testing          | Test with bridge CNI plugin on OVN-Kubernetes; verify feature gate Beta defaults                                                                             | Y                       |          |
| Regression Testing             | Verify existing network hotplug (add/remove interface) not broken by NAD ref change logic                                                                    | Y                       | Key regression area |
| Upgrade Testing                | Verify existing VMs with secondary networks stable after upgrade; NAD change works on upgraded cluster                                                       | Y                       | CNV-72333 tracks upgrade consideration |
| Backward Compatibility Testing | Feature gate Beta (enabled by default); verify VMs created before feature works correctly                                                                    | Y                       |          |
| Dependencies                   | Depends on Multus CNI and bridge plugin availability; depends on live migration infrastructure                                                               | Y                       | Migration infrastructure is prerequisite |
| Cross Integrations             | Interacts with VM live migration, network hotplug/unplug, workload updater                                                                                   | Y                       | Migration evaluation is shared with hotplug |
| Monitoring                     | No new metrics or alerts defined for NAD change operations                                                                                                   | N/A                     | Recommend adding NAD change event metrics |
| Cloud Testing                  | Feature relies on bridge CNI; cloud platforms may have different network plugins                                                                              | N/A                     | Bare-metal primary; cloud deferred |

#### **3. Test Environment**

| Environment Component                         | Configuration                                                                                              |
|:----------------------------------------------|:-----------------------------------------------------------------------------------------------------------|
| **Cluster Topology**                          | Multi-node: 3-master / 2+ worker bare-metal (minimum 2 schedulable worker nodes for migration)             |
| **OCP & OpenShift Virtualization Version(s)** | OCP 4.22+ with OpenShift Virtualization 4.22+                                                              |
| **CPU Virtualization**                        | Standard (VT-x / AMD-V enabled)                                                                           |
| **Compute Resources**                         | Minimum per worker node: 8 vCPUs, 32GB RAM                                                                |
| **Special Hardware**                          | None required (bridge CNI, no SR-IOV)                                                                      |
| **Storage**                                   | Default StorageClass available (ocs-storagecluster-ceph-rbd or equivalent)                                 |
| **Network**                                   | OVN-Kubernetes (default CNI), Multus enabled, two Linux bridge NADs with different configurations          |
| **Required Operators**                        | OpenShift Virtualization Operator, HyperConverged Cluster Operator                                         |
| **Platform**                                  | Bare metal (primary)                                                                                       |
| **Special Configurations**                    | `LiveUpdateNADRef` feature gate enabled (Beta default); `VMRolloutStrategy: LiveUpdate`; `WorkloadUpdateMethod: LiveMigrate` |

#### **3.1. Testing Tools & Frameworks**

| Category           | Tools/Frameworks                                        |
|:-------------------|:--------------------------------------------------------|
| **Test Framework** | Standard (Ginkgo for Tier 1, pytest for Tier 2)         |
| **CI/CD**          | Standard CI pipeline                                    |
| **Other Tools**    | None                                                    |

#### **4. Entry Criteria**

The following conditions must be met before testing can begin:

- [ ] Upstream KubeVirt implementation of NAD ref live update merged and available in build
- [ ] `LiveUpdateNADRef` feature gate available (Beta state)
- [ ] Test environment with 2+ schedulable worker nodes configured
- [ ] Two bridge-type NADs with different bridge configurations deployable
- [ ] Live migration infrastructure functional (workload update strategy configured)

#### **5. Risks**

| Risk Category        | Specific Risk for This Feature                                                                                  | Mitigation Strategy                                                                        | Status |
|:---------------------|:----------------------------------------------------------------------------------------------------------------|:-------------------------------------------------------------------------------------------|:-------|
| Timeline/Schedule    | Test automation (CNV-72335) still in 'New' status; feature flagged YELLOW for incomplete automation              | Prioritize P0 scenarios first; upstream test PR #17904 partially covers                    | [ ]    |
| Test Coverage        | No explicit acceptance criteria in Jira — test scenarios derived from implementation analysis                    | Document derived AC; get stakeholder review of STP scenarios                               | [ ]    |
| Test Environment     | Requires two schedulable nodes with bridge network availability for migration                                   | Use standard multi-node bare-metal lab environments                                        | [ ]    |
| Untestable Aspects   | Migration timing and network switch atomicity at packet level cannot be precisely measured                       | Verify connectivity before/after; accept brief connectivity gap during migration           | [ ]    |
| Resource Constraints | Upgrade testing (CNV-72333) not yet addressed                                                                   | Plan upgrade test scenarios as part of release cycle upgrade testing                        | [ ]    |
| Dependencies         | Feature depends on Multus CNI bridge plugin and live migration infrastructure                                   | Verify prerequisites in entry criteria; use standard lab with known-good Multus setup      | [ ]    |
| Other                | Feature gate is Beta (enabled by default) — may affect existing VMs if NAD names are inadvertently changed      | Test upgrade path to verify no unintended migrations triggered for existing VMs            | [ ]    |

#### **6. Known Limitations**

- Feature only supports secondary network interfaces (Multus); pod network (default) is not affected
- NAD change requires live migration to take effect — VM must be migratable
- SR-IOV interface NAD changes use immediate migration (different path from bridge-based NAD changes)
- No explicit performance SLA defined for migration duration during NAD change
- UI support is tracked separately under CNV-82742

---

### **III. Test Scenarios & Traceability**

| Requirement ID    | Requirement Summary                                                                    | Test Scenario(s)                                                                                                                                                                       | Tier       | Priority |
|:------------------|:---------------------------------------------------------------------------------------|:---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:-----------|:---------|
| [CNV-72329](https://issues.redhat.com/browse/CNV-72329) | VM network NAD reference can be changed on a running VM without reboot | Verify NAD reference change on running VM                                                                                                                             | Tier 1     | P0       |
|                   |                                                                                        | Verify VM remains running during NAD change                                                                                                                           | Tier 1     | P0       |
|                   |                                                                                        | [NEGATIVE] Verify error for NAD change to non-existent NAD                                                                                                            | Tier 1     | P0       |
|                   |                                                                                        | Verify NAD change during active workload                                                                                                                              | Tier 2     | P0       |
|                   | NAD reference change triggers automatic live migration                                 | Verify migration triggered after NAD change                                                                                                                           | Tier 1     | P0       |
|                   |                                                                                        | Verify VM migrates to different node after NAD change                                                                                                                 | Tier 1     | P0       |
|                   |                                                                                        | Verify MigrationRequired condition lifecycle                                                                                                                          | Tier 1     | P0       |
|                   | VM connectivity preserved on new network after NAD change                              | Verify connectivity on target network after NAD change                                                                                                                | Tier 2     | P0       |
|                   |                                                                                        | [NEGATIVE] Verify old network unreachable after NAD change                                                                                                            | Tier 2     | P0       |
|                   | Guest interface name and MAC address preserved after NAD change                        | Verify interface name preserved after NAD hotplug                                                                                                                     | Tier 1     | P0       |
|                   |                                                                                        | Verify MAC address preserved after NAD hotplug                                                                                                                        | Tier 1     | P0       |
|                   | LiveUpdateNADRef feature gate controls live update behavior                            | Verify live update when feature gate enabled                                                                                                                          | Tier 1     | P1       |
|                   |                                                                                        | [NEGATIVE] Verify restart required when feature gate disabled                                                                                                         | Tier 1     | P1       |
|                   | NAD name-only change does not require VM restart                                       | Verify no restart for NAD-only change                                                                                                                                 | Tier 1     | P1       |
|                   | Non-NAD network changes still require VM restart                                       | [NEGATIVE] Verify restart required for interface binding change                                                                                                       | Tier 1     | P1       |
|                   |                                                                                        | [NEGATIVE] Verify restart required for network removal                                                                                                                | Tier 1     | P1       |
|                   | NAD change works with namespace-qualified and unqualified references                   | Verify NAD change with namespace-qualified reference                                                                                                                  | Tier 1     | P1       |
|                   |                                                                                        | Verify NAD change with unqualified NAD name                                                                                                                           | Tier 1     | P1       |
|                   | NAD change to non-existent NAD handled gracefully                                      | [NEGATIVE] Verify graceful handling of invalid target NAD                                                                                                             | Tier 2     | P1       |
|                   |                                                                                        | [NEGATIVE] Verify VM operational after failed NAD change                                                                                                              | Tier 2     | P1       |
|                   | VM with no secondary interfaces unaffected by NAD live update                          | [NEGATIVE] Verify pod-network-only VM unaffected by feature                                                                                                           | Tier 1     | P2       |
| [CNV-72333](https://issues.redhat.com/browse/CNV-72333) | Existing VMs functional after upgrade with LiveUpdateNADRef               | Verify existing VMs stable after upgrade                                                                                                                              | Tier 2     | P1       |
|                   |                                                                                        | Verify NAD change works on upgraded cluster                                                                                                                           | Tier 2     | P1       |
|                   | Concurrent and sequential NAD changes handled correctly                                | Verify sequential NAD changes on same VM                                                                                                                              | Tier 2     | P1       |
|                   |                                                                                        | [NEGATIVE] Verify NAD change during ongoing migration                                                                                                                 | Tier 2     | P1       |

---

### **IV. Sign-off and Approval**

This Software Test Plan requires approval from the following stakeholders:

* **Reviewers:**
  - [Name / @github-username]
  - [Name / @github-username]
* **Approvers:**
  - [Name / @github-username]
  - [Name / @github-username]
