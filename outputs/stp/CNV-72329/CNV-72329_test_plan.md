# Openshift-virtualization-tests Test plan

## **NAD Reference Live Update for Secondary VM Networks - Quality Engineering Plan**

### **Metadata & Tracking**

| Field                  | Details                                                                                                  |
|:-----------------------|:---------------------------------------------------------------------------------------------------------|
| **Enhancement(s)**     | [VEP 140](https://github.com/kubevirt/enhancements/issues/140)                                           |
| **Feature in Jira**    | [VIRTSTRAT-560](https://redhat.atlassian.net/browse/VIRTSTRAT-560) - Allow changing network VLAN on the fly |
| **Jira Tracking**      | [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) (Epic)                                       |
| **QE Owner(s)**        | [QE Owner TBD]                                                                                           |
| **Owning SIG**         | sig-network                                                                                              |
| **Participating SIGs** | sig-compute                                                                                              |
| **Current Status**     | Draft                                                                                                    |

**Document Conventions:**
- **NAD** - NetworkAttachmentDefinition (Multus CRD defining secondary network attachment)
- **VMI** - VirtualMachineInstance (the running instance of a VM)
- **LiveUpdateNADRef** - Feature gate controlling live NAD reference update capability
- **NAD swap / NAD live update** - Changing the NAD reference on a running VM's secondary network interface

### **Feature Overview**

This feature allows VM administrators to change the network a running VM is connected to by updating the NAD (NetworkAttachmentDefinition) reference on a secondary interface, without requiring a VM reboot. When the `LiveUpdateNADRef` feature gate is enabled, changing the NAD reference on a VM spec triggers an automatic live migration to apply the new network attachment, rather than setting the `RestartRequired` condition. This enables seamless VLAN or network segment changes for running workloads, preserving guest uptime and reducing operational disruption.

The implementation spans:
- **`pkg/network/vmliveupdate/restart.go`** - Logic to determine if a network change requires restart vs. live update (when feature gate is enabled, NAD name changes are excluded from restart requirement)
- **`pkg/network/controllers/vm.go`** - VM controller that syncs NAD references from VM spec to VMI spec when feature gate is enabled
- **`pkg/network/migration/evaluator.go`** - Migration evaluator that detects NAD name mismatches between VMI spec and pod network status, triggering automatic migration
- **`pkg/virt-config/featuregate/active.go`** - `LiveUpdateNADRef` feature gate (Beta status)

**Related upstream PRs:**
- [kubevirt/kubevirt#16412](https://github.com/kubevirt/kubevirt/pull/16412) - Core implementation: Live Update of NAD Reference
- [kubevirt/kubevirt#14602](https://github.com/kubevirt/kubevirt/pull/14602) - Allow live changes to network fields (RestartRequired logic)
- [kubevirt/kubevirt#17904](https://github.com/kubevirt/kubevirt/pull/17904) - E2E: Verify iface name and mac remain same after NAD hotplug

---

### **I. Motivation and Requirements Review (QE Review Guidelines)**

#### **1. Requirement & User Story Review Checklist**

| Check                                  | Done | Details/Notes                                                                                                                                                                                                                          | Comments                                                     |
|:---------------------------------------|:-----|:---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:-------------------------------------------------------------|
| **Review Requirements**                | [x]  | Reviewed CNV-72329, VIRTSTRAT-560, CNV-60118, and VEP 140. Feature allows changing NAD reference on running VM secondary interfaces via live migration.                                                                                |                                                              |
| **Understand Value**                   | [x]  | **Customer value:** VM admins can swap a guest's uplink from one network/VLAN to another without the VM noticing, avoiding downtime. D/S: maps to VIRTSTRAT-560. U/S: VM admin seamless network switching.                             |                                                              |
| **Customer Use Cases**                 | [x]  | RFE CNV-60118: Customer request to switch NADs without VM restart/downtime. Use cases: VLAN migration, network segment isolation changes, link quality upgrades.                                                                       |                                                              |
| **Testability**                        | [x]  | Feature is testable: create VM with secondary Multus network, patch NAD reference, verify migration occurs and connectivity to new network is established.                                                                              |                                                              |
| **Acceptance Criteria**                | [x]  | 1) NAD ref change triggers live migration (not restart). 2) VM connectivity on new network after migration. 3) Interface name and MAC preserved. 4) Feature gated by `LiveUpdateNADRef`. 5) RestartRequired NOT set for NAD-only changes. |                                                              |
| **Non-Functional Requirements (NFRs)** | [x]  | Performance: Migration should complete within standard timeframes. Monitoring: Standard VMI migration conditions used. Security: No new RBAC changes (uses existing VM edit permissions).                                                | No special NFRs beyond standard migration performance bounds |

#### **2. Technology and Design Review**

| Check                            | Done | Details/Notes                                                                                                                                                                                       | Comments                                                        |
|:---------------------------------|:-----|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:----------------------------------------------------------------|
| **Developer Handoff/QE Kickoff** | [ ]  | Review VEP 140 design document and upstream PRs #16412, #14602. Key architectural decision: NAD ref changes are applied via migration (not in-place) because pod network annotations must be updated. | Schedule kickoff with sig-network dev lead                      |
| **Technology Challenges**        | [x]  | Migration-based approach means network change is not instantaneous. Guest network reconfiguration (IP assignment) may need cloud-init or guest agent. Bridge-based NADs only (not SR-IOV for NAD swap). | Verify behavior with different bridge plugins                   |
| **Test Environment Needs**       | [x]  | Multi-node cluster (minimum 2 schedulable nodes). Multus CNI with bridge plugin. Multiple bridge-based NADs with different configurations.                                                          |                                                                 |
| **API Extensions**               | [x]  | No new API fields. Existing `spec.networks[].multus.networkName` field is now live-updatable when feature gate is enabled. `LiveUpdateNADRef` feature gate added (Beta).                             |                                                                 |
| **Topology Considerations**      | [x]  | Requires multi-node for migration. Single-node clusters cannot use this feature (migration target node needed).                                                                                      | SNO explicitly out of scope                                     |


### **II. Software Test Plan (STP)**

#### **1. Scope of Testing**

**Testing Goals**

- **[P0]** Verify NAD reference change on a running VM triggers live migration and establishes connectivity on the new network (core feature validation)
- **[P0]** Verify `RestartRequired` condition is NOT set when NAD reference changes with `LiveUpdateNADRef` feature gate enabled
- **[P0]** Verify VM interface name and MAC address are preserved after NAD reference live update
- **[P0]** Verify feature gate behavior: `LiveUpdateNADRef` disabled causes `RestartRequired` condition instead of migration
- **[P1]** Verify multiple NAD references can be updated simultaneously on a VM with multiple secondary interfaces
- **[P1]** Verify NAD reference live update interacts correctly with existing NIC hotplug/hotunplug flows
- **[P1]** Verify error handling for invalid NAD references and edge cases
- **[P2]** Verify NAD reference live update with various network plugins and configurations

**Out of Scope (Testing Scope Exclusions)**

| Out-of-Scope Item                                                       | Rationale                                                                 | PM/Lead Agreement |
|:------------------------------------------------------------------------|:--------------------------------------------------------------------------|:------------------|
| SR-IOV NAD reference changes                                            | Feature only supports bridge-based secondary networks for NAD swap        | [ ] Name/Date     |
| Pod network (default) NAD changes                                       | Feature applies to secondary (Multus) networks only                       | [ ] Name/Date     |
| Multus Dynamic Networks Controller integration                          | Explicitly noted as not tested in VEP 140 implementation                  | [ ] Name/Date     |
| Single-node (SNO) deployments                                           | Feature requires migration, which requires multiple nodes                 | [ ] Name/Date     |
| Guest-internal network reconfiguration (DHCP/cloud-init)                | Guest IP assignment is an application-level concern, not a KubeVirt concern | [ ] Name/Date     |

#### **2. Test Strategy**

| Item                           | Description                                                                                                                           | Applicable (Y/N or N/A) | Comments                                                              |
|:-------------------------------|:--------------------------------------------------------------------------------------------------------------------------------------|:------------------------|:----------------------------------------------------------------------|
| Functional Testing             | Validates NAD ref live update triggers migration and connectivity                                                                     | Y                       | Core test scenarios                                                   |
| Automation Testing             | All test cases automated in upstream (kubevirt/kubevirt) and downstream (openshift-virtualization-tests)                               | Y                       | Tier 1 upstream, Tier 2 downstream                                    |
| Performance Testing            | Migration completion time with NAD ref changes                                                                                        | N/A                     | Standard migration performance; no feature-specific perf requirements |
| Security Testing               | RBAC for VM spec patching                                                                                                             | N/A                     | Uses existing VM edit permissions; no new RBAC surfaces               |
| Usability Testing              | No UI changes in scope for this epic                                                                                                  | N/A                     | UI covered by CNV-82742 (separate epic)                               |
| Compatibility Testing          | Bridge-based NADs with different configurations                                                                                       | Y                       | Multiple bridge plugins                                               |
| Regression Testing             | Existing hotplug/hotunplug flows, link state management                                                                               | Y                       | Must not break existing network hotplug                               |
| Upgrade Testing                | Feature gate transition across upgrades                                                                                               | Y                       | Verify feature gate preserved after upgrade                           |
| Backward Compatibility Testing | VMs created before feature gate enabled                                                                                               | Y                       | Verify no impact on existing VMs                                      |
| Dependencies                   | Multus CNI, bridge CNI plugin, KubeVirt virt-controller, virt-handler                                                                 | Y                       | Standard CNV network stack                                            |
| Cross Integrations             | NIC hotplug/hotunplug, link state management, live migration                                                                          | Y                       | Feature builds on existing network hotplug infrastructure             |
| Monitoring                     | Standard VMI conditions: `MigrationRequired`, `RestartRequired`                                                                       | Y                       | No new metrics/alerts                                                 |
| Cloud Testing                  | Not applicable - feature requires bridge-based secondary networks                                                                     | N/A                     | Bridge-based NADs not typical in cloud environments                   |

#### **3. Test Environment**

| Environment Component                         | Configuration                                                                   |
|:----------------------------------------------|:--------------------------------------------------------------------------------|
| **Cluster Topology**                          | Multi-node: 3-master / 2+ worker bare-metal                                    |
| **OCP & OpenShift Virtualization Version(s)** | OCP 4.22 with OpenShift Virtualization 4.22                                     |
| **CPU Virtualization**                        | Nodes with VT-x (Intel) or AMD-V (AMD) enabled                                 |
| **Compute Resources**                         | Minimum per worker node: 8 vCPUs, 32GB RAM                                     |
| **Special Hardware**                          | N/A                                                                             |
| **Storage**                                   | Default StorageClass (ocs-storagecluster-ceph-rbd or hostpath-csi)              |
| **Network**                                   | OVN-Kubernetes (default CNI), Multus with bridge CNI plugin for secondary networks |
| **Required Operators**                        | OpenShift Virtualization Operator                                               |
| **Platform**                                  | Bare metal                                                                      |
| **Special Configurations**                    | `LiveUpdateNADRef` feature gate enabled on KubeVirt CR; `VMRolloutStrategy: LiveUpdate`; `WorkloadUpdateMethod: LiveMigrate` |

#### **3.1. Testing Tools & Frameworks**

| Category           | Tools/Frameworks                                                  |
|:-------------------|:------------------------------------------------------------------|
| **Test Framework** | Tier 1: Ginkgo/Gomega (upstream kubevirt); Tier 2: pytest (downstream openshift-virtualization-tests) |
| **CI/CD**          | Standard Prow CI lanes                                            |
| **Other Tools**    | virtctl, oc, kubectl                                              |

#### **4. Entry Criteria**

The following conditions must be met before testing can begin:

- [x] VEP 140 design document approved and merged
- [x] Core implementation PR merged ([kubevirt/kubevirt#16412](https://github.com/kubevirt/kubevirt/pull/16412))
- [x] `LiveUpdateNADRef` feature gate registered (Beta status)
- [ ] HCO feature gate opened for downstream testing
- [ ] Test environment with multi-node cluster and Multus bridge plugin available
- [ ] Upstream e2e tests passing in CI

#### **5. Risks**

| Risk Category        | Specific Risk for This Feature                                                                                                         | Mitigation Strategy                                                                         | Status |
|:---------------------|:---------------------------------------------------------------------------------------------------------------------------------------|:--------------------------------------------------------------------------------------------|:-------|
| Timeline/Schedule    | Test automation not finalized yet (per Jira status update 2026-06-01); HCO feature gate was blocked by discovered bug                   | Prioritize P0 scenarios, coordinate with dev on HCO gate status                             | [ ]    |
| Test Coverage        | Migration-based NAD swap behavior under network instability not easily testable                                                         | Focus on functional correctness; document untestable edge cases                             | [ ]    |
| Test Environment     | Requires multi-node cluster with specific Multus bridge configuration                                                                   | Use standard QE lab environment with pre-configured bridge NADs                             | [ ]    |
| Untestable Aspects   | Multus Dynamic Networks Controller interaction (explicitly out of scope per implementation PR)                                           | Document as known limitation; test only with standard Multus                                | [ ]    |
| Dependencies         | Feature gate in HCO must be opened; was delayed due to discovered bug (per 2026-04-13 Jira comment)                                     | Monitor HCO status; test upstream with feature gate enabled directly on KubeVirt CR         | [ ]    |
| Regression           | Changes to `IsRestartRequired` and `syncVMIInterfaces` may affect existing hotplug/hotunplug and link state management flows             | Run full network regression suite; LSP analysis confirms callers at `syncRestartRequired` and `VMController.Sync` | [ ]    |

#### **6. Known Limitations**

- Feature only supports bridge-based secondary networks (not SR-IOV) for NAD reference live update
- NAD swap triggers a live migration, introducing a brief period of network reconfiguration on the guest side
- Pod network (default) cannot be changed via this mechanism
- Guest IP reconfiguration after NAD swap is the responsibility of guest-side tooling (cloud-init, NetworkManager, etc.)
- Not tested with Multus Dynamic Networks Controller
- Single-node (SNO) clusters cannot use this feature (requires migration target node)

---

### **III. Test Scenarios & Traceability**

| Requirement ID | Requirement Summary                                                                                | Test Scenario(s)                                                                   | Tier   | Priority |
|:---------------|:---------------------------------------------------------------------------------------------------|:-----------------------------------------------------------------------------------|:-------|:---------|
| CNV-72329      | NAD reference can be live-updated on a running VM's secondary network interface                     | Verify NAD reference change triggers live migration                                | Tier 1 | P0       |
|                |                                                                                                    | Verify VM connectivity on new network after NAD swap                               | Tier 1 | P0       |
|                |                                                                                                    | Verify interface name and MAC preserved after NAD swap                             | Tier 1 | P0       |
|                |                                                                                                    | Verify error when target NAD does not exist                                        | Tier 1 | P1       |
|                |                                                                                                    | Verify NAD swap with network connectivity end-to-end                               | Tier 2 | P0       |
| CNV-72329      | RestartRequired condition behavior depends on LiveUpdateNADRef feature gate state                  | Verify RestartRequired NOT set when feature gate enabled                           | Tier 1 | P0       |
|                |                                                                                                    | Verify RestartRequired IS set when feature gate disabled                           | Tier 1 | P0       |
|                |                                                                                                    | Verify VMI spec retains old NAD when feature gate disabled                         | Tier 1 | P1       |
| CNV-72329      | Multiple NAD references can be updated simultaneously on a multi-interface VM                      | Verify simultaneous update of multiple NAD references triggers single migration    | Tier 1 | P1       |
|                |                                                                                                    | Verify partial NAD update (some interfaces changed, others unchanged)              | Tier 1 | P1       |
|                |                                                                                                    | Verify multi-interface NAD swap end-to-end connectivity                            | Tier 2 | P1       |
| CNV-72329      | NAD live update does not interfere with existing NIC hotplug/hotunplug and link state management   | Verify NIC hotplug still works after NAD reference change                          | Tier 1 | P1       |
|                |                                                                                                    | Verify NIC hotunplug still works after NAD reference change                        | Tier 1 | P1       |
|                |                                                                                                    | Verify interface link state changes still work after NAD swap                      | Tier 1 | P1       |
|                |                                                                                                    | Verify NAD swap during ongoing NIC hotplug operation                               | Tier 2 | P2       |
| CNV-72329      | Migration evaluator correctly detects NAD name mismatch and triggers migration                     | Verify migration condition set when VMI NAD differs from pod network status        | Tier 1 | P0       |
|                |                                                                                                    | Verify migration condition cleared after successful migration                      | Tier 1 | P1       |
|                |                                                                                                    | Verify NAD comparison handles namespace-qualified and unqualified names            | Tier 1 | P1       |
| CNV-72329      | VM controller syncs NAD references from VM spec to VMI spec when feature gate is enabled           | Verify VMI spec networks updated to match VM spec after NAD change                 | Tier 1 | P0       |
|                |                                                                                                    | Verify pod network (default) is not affected by NAD sync logic                     | Tier 1 | P1       |
| CNV-72329      | [NEGATIVE] Invalid and edge-case NAD reference changes are handled gracefully                      | Verify behavior when changing NAD to same value (no-op)                            | Tier 1 | P2       |
|                |                                                                                                    | Verify behavior when migration target node unavailable                             | Tier 2 | P2       |
|                |                                                                                                    | Verify NAD swap with VM that has no secondary interfaces                           | Tier 1 | P2       |

---

### **IV. Sign-off and Approval**

This Software Test Plan requires approval from the following stakeholders:

* **Reviewers:**
  - [QE Reviewer / @github-username]
  - [sig-network QE Lead / @github-username]
* **Approvers:**
  - [QE Lead / @github-username]
  - [Product Manager / @github-username]
