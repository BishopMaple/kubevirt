# Openshift-virtualization-tests Test plan

## **Support Changing the VM Attached Network NAD Ref Using Hotplug - Quality Engineering Plan**

### **Metadata & Tracking**

| Field                  | Details                                                                                                          |
|:-----------------------|:-----------------------------------------------------------------------------------------------------------------|
| **Enhancement(s)**     | [VEP 140](https://github.com/kubevirt/enhancements/issues/140)                                                   |
| **Feature in Jira**    | [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329)                                                       |
| **Jira Tracking**      | [CNV-72329](https://redhat.atlassian.net/browse/CNV-72329) (Epic)                                                |
| **Parent Feature**     | [VIRTSTRAT-560](https://redhat.atlassian.net/browse/VIRTSTRAT-560) - Allow changing network VLAN on the fly      |
| **QE Owner(s)**        | TBD                                                                                                              |
| **Owning SIG**         | sig-network                                                                                                      |
| **Participating SIGs** | sig-compute                                                                                                      |
| **Current Status**     | Draft                                                                                                            |

**Document Conventions:**
- **NAD** - NetworkAttachmentDefinition (Multus CRD defining a secondary network)
- **VMI** - VirtualMachineInstance (the running VM representation)
- **FG** - Feature Gate
- **LiveUpdateNADRef** - Feature gate controlling this feature (Beta state)
- **NAD swap** - Shorthand for changing the NAD reference on a running VM

### **Feature Overview**

This feature allows customers to change the network a VM is connected to (e.g., change VLAN ID) by updating the NetworkAttachmentDefinition (NAD) reference on a running VM, without requiring a reboot. When the NAD reference is updated on the VM spec, the system automatically triggers a live migration to apply the network change seamlessly. This is gated behind the `LiveUpdateNADRef` feature gate (currently Beta). The feature benefits customers who need to move VMs between VLANs or network segments for maintenance, security isolation, or network topology changes without disrupting workloads.

- [VEP 140 - Live Update of NAD Reference](https://github.com/kubevirt/enhancements/issues/140)
- [Primary Implementation PR #16412](https://github.com/kubevirt/kubevirt/pull/16412)

---

### **I. Motivation and Requirements Review (QE Review Guidelines)**

#### **1. Requirement & User Story Review Checklist**

| Check                                  | Done | Details/Notes                                                                                                                                                                                                  | Comments                                                    |
|:---------------------------------------|:-----|:---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------------------------------------------|
| **Review Requirements**                | [x]  | Reviewed VEP 140, CNV-72329 epic, and parent feature VIRTSTRAT-560. Requirements specify live NAD reference update via migration.                                                                              |                                                             |
| **Understand Value**                   | [x]  | As a VM admin, I want to swap the guest's uplink from one network to another without the VM noticing, so they get better/worse link, different VLAN, or isolated segment. Value: zero-downtime network changes. |                                                             |
| **Customer Use Cases**                 | [x]  | VLAN migration, network segment isolation, network maintenance, network policy updates - all without VM reboot.                                                                                                |                                                             |
| **Testability**                        | [x]  | Feature is testable: update NAD ref on VM spec, verify migration triggers, verify network connectivity on new NAD post-migration.                                                                              |                                                             |
| **Acceptance Criteria**                | [x]  | D/S: VM network reference updated without reboot. U/S: Live migration triggered automatically. Network connectivity verified post-migration.                                                                   |                                                             |
| **Non-Functional Requirements (NFRs)** | [x]  | Performance: migration should complete in reasonable time. Security: RBAC for VM network updates. Monitoring: migration conditions visible in VMI status.                                                       | No explicit performance SLAs defined in requirements.       |

#### **2. Technology and Design Review**

| Check                            | Done | Details/Notes                                                                                                                                                                                                                                              | Comments                                                                                                        |
|:---------------------------------|:-----|:-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:----------------------------------------------------------------------------------------------------------------|
| **Developer Handoff/QE Kickoff** | [ ]  | Design documented in VEP 140. Implementation in PR #16412 introduces `LiveUpdateNADRef` feature gate, modifies VM controller sync logic, migration evaluator, and live update restart logic.                                                               | Recommend scheduling walkthrough with developer (frenzyfriday).                                                 |
| **Technology Challenges**        | [x]  | Live migration with network swap requires Multus annotation updates. Known bug found and fixed: `syncNetworks` dropped auto-injected pod network when FG enabled and VM had no explicit interfaces/networks (PR #17315/17373).                              | Feature is not tested with Multus Dynamic Networks Controller (noted in PR).                                    |
| **Test Environment Needs**       | [x]  | Requires multi-node cluster (minimum 2 schedulable nodes for live migration), Multus CNI, bridge CNI plugin, at least 2 NADs with different bridge configurations.                                                                                         |                                                                                                                 |
| **API Extensions**               | [x]  | No new API fields. Feature uses existing `spec.template.spec.networks[].multus.networkName` field. New feature gate `LiveUpdateNADRef` added to `featuregate/active.go` (Beta state). `clusterConfigurer` interface extended with `LiveUpdateNADRefEnabled()`. |                                                                                                                 |
| **Topology Considerations**      | [x]  | Multi-node required for migration. Single-node (SNO) cannot support this feature as live migration requires target node.                                                                                                                                   |                                                                                                                 |


### **II. Software Test Plan (STP)**

#### **1. Scope of Testing**

**Testing Goals**

- **[P0]** Verify that updating the NAD reference on a running VM's secondary network triggers an automatic live migration and the VM connects to the new network post-migration
- **[P0]** Verify that the `LiveUpdateNADRef` feature gate controls the feature behavior: enabled = live migration on NAD change; disabled = restart required
- **[P0]** Verify that the VM interface name and MAC address are preserved after NAD swap via live migration
- **[P1]** Verify that multiple NAD references can be updated simultaneously on a VM with multiple secondary networks
- **[P1]** Verify that the pod network (masquerade binding) is preserved when `LiveUpdateNADRef` FG is enabled and NAD references are updated (regression from PR #17315)
- **[P1]** Verify that network connectivity is functional on the new NAD after migration completes (ping test between VMs)
- **[P1]** Verify that the `VirtualMachineInstanceMigrationRequired` condition appears and clears after successful migration
- **[P2]** Verify that the feature works with different binding types (bridge, SR-IOV)
- **[P2]** Verify upgrade path: VMs created before feature gate enablement continue to work correctly after enabling `LiveUpdateNADRef`

**Out of Scope (Testing Scope Exclusions)**

| Out-of-Scope Item                                                              | Rationale                                                                       | PM/Lead Agreement |
|:-------------------------------------------------------------------------------|:--------------------------------------------------------------------------------|:------------------|
| Testing with Multus Dynamic Networks Controller                                | Explicitly not tested per PR #16412 notes; separate feature integration         | [ ] TBD           |
| Performance benchmarking of migration duration during NAD swap                  | No explicit performance SLAs defined in requirements                            | [ ] TBD           |
| Testing on Single Node OpenShift (SNO)                                         | Live migration requires multi-node topology; SNO cannot support this feature    | [ ] TBD           |
| UI testing for NAD reference changes                                           | Covered separately under CNV-82742 (UI Support)                                 | [ ] TBD           |
| Testing with non-Multus secondary network implementations                      | Feature is Multus-specific (uses `spec.networks[].multus.networkName`)          | [ ] TBD           |

#### **2. Test Strategy**

| Item                           | Description                                                                                                                                | Applicable (Y/N or N/A) | Comments                                                                                               |
|:-------------------------------|:-------------------------------------------------------------------------------------------------------------------------------------------|:------------------------|:-------------------------------------------------------------------------------------------------------|
| Functional Testing             | Validate NAD reference live update triggers migration and network connectivity is established on new NAD                                   | Y                       | Core test scenarios                                                                                    |
| Automation Testing             | All test scenarios must be automated in Tier 1 (kubevirt/kubevirt) and/or Tier 2 (openshift-virtualization-tests)                          | Y                       | E2E test exists in `tests/network/nad_live_update.go`; additional Tier 2 scenarios needed              |
| Performance Testing            | Validate migration completes in reasonable timeframe during NAD swap                                                                       | N/A                     | No performance SLAs defined; general migration performance covered by existing tests                   |
| Security Testing               | Verify RBAC permissions for updating VM network references                                                                                 | Y                       | Standard VM RBAC applies; no new RBAC roles introduced                                                 |
| Usability Testing              | N/A - no UI changes in this scope (UI covered by CNV-82742)                                                                                | N/A                     | UI testing tracked separately                                                                          |
| Compatibility Testing          | Verify feature works across supported OCP versions and with OVN-Kubernetes CNI                                                             | Y                       | Test with OCP 4.22                                                                                     |
| Regression Testing             | Verify existing network hotplug/hotunplug functionality is not broken; verify pod network preservation (PR #17315 regression fix)           | Y                       | Critical: `syncNetworks` bug fix must be regression-tested                                             |
| Upgrade Testing                | Verify VMs with secondary networks behave correctly after upgrading to a version with `LiveUpdateNADRef` FG                                | Y                       | Upgrade from 4.21 to 4.22                                                                              |
| Backward Compatibility Testing | Verify existing VMs without NAD changes continue to work when FG is enabled                                                                | Y                       | FG is Beta - enabled by default                                                                        |
| Dependencies                   | Depends on Multus CNI, bridge CNI plugin, and live migration infrastructure                                                                | Y                       | Standard CNV dependencies                                                                              |
| Cross Integrations             | Migration controller, VM controller, VMI lifecycle controller all modified; verify no impact on standard migration flows                   | Y                       | Changes span `pkg/network/controllers/`, `pkg/network/migration/`, `pkg/network/vmliveupdate/`         |
| Monitoring                     | `VirtualMachineInstanceMigrationRequired` condition used to signal migration need                                                          | Y                       | Existing condition reused; no new metrics/alerts                                                       |
| Cloud Testing                  | N/A - feature requires Multus secondary networks which are primarily bare-metal/on-prem                                                   | N/A                     | Bridge CNI typically not available on cloud platforms                                                   |

#### **3. Test Environment**

| Environment Component                         | Configuration                                                                                       | Specification Examples                                                     |
|:----------------------------------------------|:----------------------------------------------------------------------------------------------------|:---------------------------------------------------------------------------|
| **Cluster Topology**                          | Multi-node (minimum 2 schedulable worker nodes)                                                     | 3-master/2-worker bare-metal                                               |
| **OCP & OpenShift Virtualization Version(s)** | OCP 4.22 with OpenShift Virtualization 4.22                                                         | Latest nightly or GA candidate                                             |
| **CPU Virtualization**                        | Standard (VT-x or AMD-V enabled)                                                                    | Nodes with hardware virtualization support                                 |
| **Compute Resources**                         | Minimum per worker node: 4 vCPUs, 16GB RAM                                                         | Sufficient for concurrent VM migrations                                    |
| **Special Hardware**                          | N/A (bridge CNI does not require special hardware)                                                   | SR-IOV NICs optional for SR-IOV binding tests                              |
| **Storage**                                   | Shared storage (RWX) required for live migration                                                    | ODF/Ceph RBD or NFS                                                       |
| **Network**                                   | OVN-Kubernetes (default CNI), Multus CNI, bridge CNI plugin                                         | 2+ bridge-type NADs configured on different bridges                        |
| **Required Operators**                        | OpenShift Virtualization, HyperConverged Cluster Operator                                           | Multus installed by default on OCP                                         |
| **Platform**                                  | Bare metal (primary), potentially AWS with Multus support                                           | Bare metal recommended for bridge networking                               |
| **Special Configurations**                    | `LiveUpdateNADRef` feature gate enabled, `VMRolloutStrategy: LiveUpdate`, workload update: LiveMigrate | KubeVirt CR configuration required                                         |

#### **3.1. Testing Tools & Frameworks**

| Category           | Tools/Frameworks                                                          |
|:-------------------|:--------------------------------------------------------------------------|
| **Test Framework** | Ginkgo/Gomega (Tier 1), pytest (Tier 2)                                   |
| **CI/CD**          | Standard kubevirt CI lanes; release checklist jobs                        |
| **Other Tools**    | `virtctl` for VM management, `oc`/`kubectl` for cluster operations       |

#### **4. Entry Criteria**

The following conditions must be met before testing can begin:

- [x] Requirements and design documents are **approved and merged** (VEP 140 merged)
- [x] Primary implementation PR merged ([#16412](https://github.com/kubevirt/kubevirt/pull/16412))
- [x] Bug fix for pod network preservation merged ([#17315](https://github.com/kubevirt/kubevirt/pull/17315) / [#17373](https://github.com/kubevirt/kubevirt/pull/17373))
- [ ] Test environment can be **set up and configured** with multi-node cluster, Multus, and bridge NADs
- [ ] `LiveUpdateNADRef` feature gate is available and enabled (Beta state) in target build
- [ ] HCO feature gate is open (previously blocked by discovered bug per Jira comment 2026/04/13)

#### **5. Risks**

| Risk Category        | Specific Risk for This Feature                                                                                                     | Mitigation Strategy                                                                                | Status |
|:---------------------|:-----------------------------------------------------------------------------------------------------------------------------------|:---------------------------------------------------------------------------------------------------|:-------|
| Timeline/Schedule    | Feature was at risk due to upstream freeze (Jira comment 2026/02/23) and HCO feature gate bug (2026/04/13)                         | Feature gate now in Beta state; monitor HCO integration                                            | [x]    |
| Test Coverage        | Feature not tested with Multus Dynamic Networks Controller (per PR notes)                                                          | Document as known limitation; add to future test scope when Dynamic Networks Controller is stable   | [ ]    |
| Test Environment     | Requires multi-node cluster with bridge CNI which may not be available in all CI environments                                      | Use dedicated bare-metal CI lanes; ensure bridge NADs are pre-provisioned                          | [ ]    |
| Untestable Aspects   | Cannot test with all possible NAD types (VLAN, VXLAN, macvlan, etc.) in initial scope                                              | Focus on bridge NAD (most common); expand to other types in subsequent releases                    | [ ]    |
| Resource Constraints | E2E tests were quarantined (PR #17084) and had flaky console login/ping issues (PR #17087)                                         | Monitor test stability; fix flaky tests before GA                                                  | [ ]    |
| Dependencies         | Feature depends on live migration infrastructure; migration failures would cascade                                                  | Verify migration health before NAD swap tests; add pre-condition checks                           | [ ]    |
| Regression           | `syncNetworks` bug (PR #17315) dropped pod network when FG enabled and VM had no explicit interfaces - could reappear on refactors | Add specific regression test for auto-injected pod network preservation; monitor code changes       | [x]    |

#### **6. Known Limitations**

- Feature is **not supported with Multus Dynamic Networks Controller** (per PR #16412 notes)
- Requires **multi-node cluster** - cannot function on Single Node OpenShift (SNO) as live migration is required
- Feature gate `LiveUpdateNADRef` must be explicitly enabled (Beta state - enabled by default but can be disabled)
- Only **Multus-based secondary networks** support NAD reference live update; pod network (masquerade) references cannot be changed this way
- The feature modifies the VMI spec networks via JSON patch; concurrent modifications to the same VM may conflict

---

### **III. Test Scenarios & Traceability**

| Requirement ID | Requirement Summary                                                                                                 | Test Scenario(s)                                                                                                                                                                                    | Tier   | Priority |
|:---------------|:--------------------------------------------------------------------------------------------------------------------|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:-------|:---------|
| CNV-72329      | As a VM admin, I want to swap the guest's uplink from one network to another without the VM noticing                | TS-CNV-72329-001: Verify VM NAD reference update triggers live migration and VM connects to new network post-migration with network connectivity validated via ping                                 | Tier 1 | P0       |
| CNV-72329      | NAD reference change should happen without VM reboot                                                                | TS-CNV-72329-002: Verify `VirtualMachineInstanceMigrationRequired` condition appears after NAD reference update and clears after successful migration (no restart)                                  | Tier 1 | P0       |
| CNV-72329      | Feature gate controls behavior                                                                                      | TS-CNV-72329-003: Verify with `LiveUpdateNADRef` FG disabled, changing NAD reference requires VM restart (not live migration)                                                                       | Tier 1 | P0       |
| CNV-72329      | VM interface identity preserved after NAD swap                                                                      | TS-CNV-72329-004: Verify VM interface name and MAC address remain the same after NAD reference live update and migration                                                                            | Tier 1 | P0       |
| CNV-72329      | Multiple secondary networks support                                                                                | TS-CNV-72329-005: Verify updating NAD references on multiple secondary networks simultaneously triggers migration and all networks are correctly updated                                            | Tier 1 | P1       |
| CNV-72329      | Pod network preservation (regression from PR #17315)                                                                | TS-CNV-72329-006: Verify auto-injected pod network (masquerade) is preserved when `LiveUpdateNADRef` FG is enabled and VM has no explicit interfaces/networks defined                               | Tier 1 | P0       |
| CNV-72329      | NAD swap with bridge binding                                                                                        | TS-CNV-72329-007: Verify NAD reference live update works with bridge binding - VM migrates and connects to new bridge network                                                                       | Tier 2 | P1       |
| CNV-72329      | NAD swap with SR-IOV binding                                                                                        | TS-CNV-72329-008: Verify NAD reference live update works with SR-IOV binding (if hardware available)                                                                                                | Tier 2 | P2       |
| CNV-72329      | End-to-end VLAN change workflow                                                                                     | TS-CNV-72329-009: Verify complete user workflow: create VM on VLAN-A NAD, update to VLAN-B NAD, verify migration, verify connectivity on VLAN-B, verify no connectivity on VLAN-A                   | Tier 2 | P0       |
| CNV-72329      | Upgrade compatibility                                                                                               | TS-CNV-72329-010: Verify VMs with secondary networks created before `LiveUpdateNADRef` FG enablement continue to function correctly after upgrade to 4.22                                          | Tier 2 | P1       |
| CNV-72329      | Negative: invalid NAD reference                                                                                     | TS-CNV-72329-011: Verify that updating NAD reference to a non-existent NAD is handled gracefully (migration should not proceed or should fail with clear error)                                     | Tier 2 | P1       |
| CNV-72329      | NAD swap during ongoing migration                                                                                   | TS-CNV-72329-012: Verify behavior when NAD reference is updated while a migration is already in progress                                                                                            | Tier 2 | P2       |
| CNV-72329      | RBAC validation                                                                                                     | TS-CNV-72329-013: Verify that non-admin users without VM update permissions cannot change NAD references                                                                                            | Tier 2 | P1       |

---

### **IV. Sign-off and Approval**

This Software Test Plan requires approval from the following stakeholders:

* **Reviewers:**
  - TBD / @TBD
  - TBD / @TBD
* **Approvers:**
  - TBD / @TBD
  - TBD / @TBD

---

### **Appendix: Regression Impact Analysis (LSP-Traced)**

The following dependency chains were identified via LSP analysis of the KubeVirt codebase, tracing from the modified files in PR #16412:

#### Modified Components

| File                                         | Key Changes                                                                                          |
|:---------------------------------------------|:-----------------------------------------------------------------------------------------------------|
| `pkg/network/controllers/vm.go`              | Added `clusterConfigurer` interface; `syncNetworks()` function for NAD reference sync; `syncVMIInterfaces()` now accepts `isLiveUpdateNADRefEnabled` parameter |
| `pkg/network/migration/evaluator.go`         | `Evaluate()` now accepts `pod` parameter; `shouldVMIBeMarkedForAutoMigration()` checks NAD name equality; added `isNADNameEqual()` function |
| `pkg/network/vmliveupdate/restart.go`        | `IsRestartRequired()` now uses `clusterConfigurer`; added `haveNetsChangedIgnoringNADName()` and `areNetsEqualIgnoringMultusNetName()` predicates |
| `pkg/virt-config/feature-gates.go`           | Added `LiveUpdateNADRefEnabled()` method on `ClusterConfig`                                          |
| `pkg/virt-config/featuregate/active.go`      | Registered `LiveUpdateNADRef` feature gate (Beta state)                                              |
| `tests/network/nad_live_update.go`           | New e2e test: NAD name live update with migration verification and ping connectivity test             |

#### Call Chain Dependencies

```
syncNetworks() <-- syncVMIInterfaces() <-- VMController.Sync()
    ^                                           ^
    |                                           |
    tests/sync_networks_test.go          pkg/virt-controller/watch/application.go (wires VMController)

Evaluator.Evaluate() <-- syncMigrationRequiredCondition() (pkg/virt-controller/watch/vmi/lifecycle.go)

IsRestartRequired() <-- syncRestartRequired() (pkg/virt-controller/watch/vm/vm.go)

LiveUpdateNADRefEnabled() -- used by VMController, Evaluator, and IsRestartRequired via clusterConfigurer interface
```

#### Regression Risk Areas

1. **VM Controller Sync** (`pkg/network/controllers/vm.go`): The `syncVMIInterfaces` function now passes `isLiveUpdateNADRefEnabled` to control NAD sync behavior. Risk: incorrect feature gate check could cause unexpected network changes.
2. **Migration Evaluator** (`pkg/network/migration/evaluator.go`): The `Evaluate` method signature changed to accept a pod parameter for NAD name comparison. Risk: callers must pass correct pod reference; `isNADNameEqual` parsing of Multus network status could fail.
3. **Live Update Restart Logic** (`pkg/network/vmliveupdate/restart.go`): New predicates `haveNetsChangedIgnoringNADName` ensure NAD-only changes don't trigger restart. Risk: predicate logic error could cause unnecessary restarts or miss required restarts.
4. **Pod Network Preservation** (fixed in PR #17315): The original `syncNetworks` implementation dropped networks not found in VM spec (including auto-injected pod network). Risk: regression if `syncNetworks` is refactored again.
