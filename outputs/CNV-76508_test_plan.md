# Openshift-virtualization-tests Test plan

## **Cross-Cluster Live Migration Network Proxy (Connection Multiplexing) - Quality Engineering Plan**

### **Metadata & Tracking**

| Field                  | Details                                                                                                       |
|:-----------------------|:--------------------------------------------------------------------------------------------------------------|
| **Enhancement(s)**     | [VEP #192](https://github.com/kubevirt/enhancements/issues/192)                                               |
| **Feature in Jira**    | [CNV-76299](https://redhat.atlassian.net/browse/CNV-76299) - Provide a proxy between the cluster LM network and the cross cluster LM network |
| **Jira Tracking**      | [CNV-76508](https://redhat.atlassian.net/browse/CNV-76508) - Synchronization controller connection multiplexing |
| **QE Owner(s)**        | TBD                                                                                                           |
| **Owning SIG**         | sig-network, sig-compute                                                                                      |
| **Participating SIGs** | sig-storage, sig-migration                                                                                    |
| **Current Status**     | Draft                                                                                                         |

**Document Conventions:**
- **CCLM** - Cross-Cluster Live Migration
- **LM** - Live Migration
- **NAD** - Network Attachment Definition
- **Sync Controller** - Synchronization Controller (the component that coordinates decentralized live migrations between clusters)
- **Proxy** - TCP proxy layer in the sync controller that forwards migration traffic between in-cluster and cross-cluster networks

### **Feature Overview**

This feature adds TCP proxy support to the KubeVirt synchronization controller, enabling decentralized cross-cluster live migration without requiring all virt-handler pods to have direct network access to the cross-cluster migration network. The synchronization controller opens local proxy ports that forward migration traffic (libvirt state migration and NBD block migration channels) between the in-cluster LM network and the cross-cluster LM network. This reduces the number of IP addresses needed on the cross-cluster network to only the synchronization controllers (typically 2-3 per cluster), instead of every virt-handler pod.

The implementation is gated behind the `DecentralizedLiveMigration` feature gate (Alpha state) and introduces a new `crossClusterNetwork` configuration field in the KubeVirt CR's `MigrationConfiguration`, along with `synchronizationPlacement` for controlling sync controller scheduling.

**Related Enhancements:**
- [VEP #192 - Cross-cluster migration proxy](https://github.com/kubevirt/enhancements/issues/192)
- [CNV-50823 - Cross-cluster live migration - GA](https://redhat.atlassian.net/browse/CNV-50823)

---

### **I. Motivation and Requirements Review (QE Review Guidelines)**

#### **1. Requirement & User Story Review Checklist**

| Check                                  | Done | Details/Notes | Comments |
|:---------------------------------------|:-----|:-------------|:---------|
| **Review Requirements**                | [x]  | Reviewed CNV-76508 (story) and CNV-76299 (parent epic). Feature implements TCP proxy in sync controller for CCLM migration traffic multiplexing. | PR #17922 in kubevirt/kubevirt provides the implementation. |
| **Understand Value**                   | [x]  | **Customer value:** Network administrators no longer need to assign IP addresses to every virt-handler pod on the cross-cluster migration network. Only 2-3 sync controller IPs are needed per cluster, greatly simplifying NAD configuration and eliminating the need for DHCP or complex static IP automation. | Upstream feature, applicable to downstream CNV deployments. |
| **Customer Use Cases**                 | [x]  | 1. Admin configures a cross-cluster LM network with only 2-3 static IPs. 2. Admin uses `synchronizationPlacement` to schedule sync controllers on specific nodes with CCLM network access. 3. VMs migrate across clusters transparently through the proxy layer. | |
| **Testability**                        | [x]  | Feature is testable: proxy opens TCP ports, forwards traffic, and migration completes. Metrics are emitted for observability. Feature gate controls enablement. | Multi-cluster environment required for full E2E validation. |
| **Acceptance Criteria**                | [x]  | 1. Sync controller proxy opens ports on both source and target sides. 2. Migration traffic flows through proxy transparently. 3. Proxy cleans up on migration completion/failure. 4. Metrics track active connections, bytes transferred, and errors. | |
| **Non-Functional Requirements (NFRs)** | [x]  | **Performance:** Proxy should not be a bottleneck for concurrent migrations (default max 5 per cluster). **Security:** SSRF protection via IP validation (blocks link-local/cloud metadata). **Monitoring:** 3 new Prometheus metrics. | Concurrency limited to 64 connections per proxy mapping. |

#### **2. Technology and Design Review**

| Check                            | Done | Details/Notes | Comments |
|:---------------------------------|:-----|:-------------|:---------|
| **Developer Handoff/QE Kickoff** | [ ]  | Pending - PR #17922 is still open. | Schedule after merge. |
| **Technology Challenges**        | [x]  | TCP proxy with bidirectional `io.Copy` forwarding. Connection semaphore limits concurrency. `DeadlineResettingReader` wraps connections to prevent idle timeout during active transfers. | Need to verify proxy doesn't become bottleneck under load. |
| **Test Environment Needs**       | [x]  | Multi-cluster setup with separate in-cluster and cross-cluster LM networks. Sync controllers need access to both networks via Multus NADs. | See Section II.3. |
| **API Extensions**               | [x]  | New fields in `KubeVirtSpec.Configuration.MigrationConfiguration`: `crossClusterNetwork` (string). New field in `KubeVirtSpec`: `synchronizationPlacement` (ComponentConfig). New `DecentralizedLiveMigration` feature gate registered at Alpha. | |
| **Topology Considerations**      | [x]  | Multi-cluster topology required. Sync controllers must be placed on nodes with access to the CCLM network (via `synchronizationPlacement`). Proxy reduces cross-cluster IP requirements. | |

---

### **II. Software Test Plan (STP)**

#### **1. Scope of Testing**

**Testing Goals**

- **[P0]** Verify TCP proxy opens correct number of ports on both source and target sync controllers when `crossClusterNetwork` is configured and `DecentralizedLiveMigration` feature gate is enabled
- **[P0]** Verify cross-cluster live migration completes successfully with migration traffic flowing through the proxy layer (both libvirt state and NBD block channels)
- **[P0]** Verify proxy ports are properly cleaned up after migration completion (success or failure)
- **[P0]** Verify the `DecentralizedLiveMigration` feature gate correctly gates proxy functionality (disabled by default, Alpha)
- **[P1]** Verify `synchronizationPlacement` correctly schedules sync controller pods on designated nodes
- **[P1]** Verify proxy handles concurrent migrations without connection exhaustion (respects 64-connection limit per mapping)
- **[P1]** Verify SSRF protection blocks proxy connections to link-local addresses (169.254.0.0/16) and IPv6 link-local (fe80::/10)
- **[P1]** Verify new Prometheus metrics (`kubevirt_decentralized_migration_proxy_active_connections`, `kubevirt_decentralized_migration_proxy_bytes_transferred_total`, `kubevirt_decentralized_migration_proxy_errors_total`) are emitted correctly
- **[P1]** Verify proxy graceful shutdown closes all connections and listeners when sync controller pod restarts
- **[P2]** Verify migration cancellation through proxy (both source-initiated and target-initiated) properly tears down proxy connections
- **[P2]** Verify `crossClusterNetwork` API field validation in KubeVirt CR

**Out of Scope (Testing Scope Exclusions)**

| Out-of-Scope Item | Rationale | PM/Lead Agreement |
|:-------------------|:----------|:------------------|
| Testing of underlying CNI/Multus network provisioning | Platform-level infrastructure tested by networking team | [ ] TBD |
| Kubernetes scheduler behavior for sync controller placement | Core K8s scheduling, tested upstream | [ ] TBD |
| Raw gRPC synchronization protocol (non-proxy path) | Existing functionality, not modified by this feature | [ ] TBD |
| Performance benchmarking of proxy throughput | Requires dedicated performance testing infrastructure; deferred to performance team | [ ] TBD |

#### **2. Test Strategy**

| Item                           | Description | Applicable | Comments |
|:-------------------------------|:------------|:-----------|:---------|
| Functional Testing             | Validates proxy port opening, traffic forwarding, cleanup, and migration completion through proxy | Y | Core focus |
| Automation Testing             | All test cases will be automated in kubevirt/kubevirt (Tier 1/Go) and openshift-virtualization-tests (Tier 2/Python) | Y | |
| Performance Testing            | Validate proxy doesn't become bottleneck with default max concurrent migrations | Y | Basic validation only; dedicated perf testing deferred |
| Security Testing               | SSRF protection: verify blocked addresses, validate target address restrictions | Y | |
| Usability Testing              | N/A - no UI changes | N/A | Backend-only feature |
| Compatibility Testing          | Verify feature works with OVN-Kubernetes and secondary networks | Y | |
| Regression Testing             | Verify existing non-proxy decentralized migrations still work when crossClusterNetwork is not configured | Y | |
| Upgrade Testing                | Verify upgrade from version without proxy to version with proxy preserves existing migration configurations | Y | |
| Backward Compatibility Testing | Verify migration works when only one cluster has proxy support | Y | Important for rolling upgrades |
| Dependencies                   | Depends on Multus for secondary network attachment, gRPC sync protocol | Y | |
| Cross Integrations             | Live migration, storage migration (NBD), virt-operator deployment | Y | |
| Monitoring                     | 3 new Prometheus metrics for proxy observability | Y | |
| Cloud Testing                  | N/A - feature is cluster-to-cluster, not cloud-specific | N/A | |

#### **3. Test Environment**

| Environment Component                         | Configuration | Specification |
|:----------------------------------------------|:--------------|:-------------|
| **Cluster Topology**                          | Multi-cluster | 2 clusters, each with 3-master/3-worker bare-metal or nested virt |
| **OCP & OpenShift Virtualization Version(s)** | Latest | OCP 4.22 with OpenShift Virtualization 4.22 |
| **CPU Virtualization**                        | Required | Nodes with VT-x (Intel) or AMD-V (AMD) enabled |
| **Compute Resources**                         | Standard | Minimum per worker node: 8 vCPUs, 32GB RAM |
| **Special Hardware**                          | None | Standard NICs sufficient |
| **Storage**                                   | Shared | Shared storage accessible by both clusters for block migration, default StorageClass with RWX support |
| **Network**                                   | Multi-network | OVN-Kubernetes (primary), dedicated in-cluster LM network (NAD), dedicated cross-cluster LM network (NAD), Multus enabled |
| **Required Operators**                        | Multiple | OpenShift Virtualization, NMState Operator (for network configuration) |
| **Platform**                                  | Bare metal or nested | Bare metal preferred for production-like testing |
| **Special Configurations**                    | Feature gate | `DecentralizedLiveMigration` feature gate must be enabled; `crossClusterNetwork` must be configured in KubeVirt CR |

#### **3.1. Testing Tools & Frameworks**

| Category           | Tools/Frameworks |
|:-------------------|:----------------|
| **Test Framework** | Ginkgo v2 (Tier 1), pytest (Tier 2) |
| **CI/CD**          | Standard CI lanes; multi-cluster test lane required |
| **Other Tools**    | virtctl, oc, kubectl for CLI validation |

#### **4. Entry Criteria**

- [x] Requirements and design documents are **approved and merged** (VEP #192)
- [ ] PR #17922 is **merged** into kubevirt/kubevirt main branch
- [ ] Test environment can be **set up and configured** with multi-cluster topology and dual LM networks
- [ ] `DecentralizedLiveMigration` feature gate is available and functional
- [ ] `crossClusterNetwork` field is accepted by KubeVirt CR validation

#### **5. Risks**

| Risk | Impact | Likelihood | Mitigation |
|:-----|:-------|:-----------|:-----------|
| Multi-cluster test environment availability | High - cannot test core functionality without it | Medium | Coordinate with DevOps for dedicated multi-cluster CI lane |
| Proxy becomes performance bottleneck under concurrent migrations | Medium - could impact migration SLA | Low | Default max 5 concurrent migrations per cluster mitigates this |
| Feature gate interactions with other migration features | Medium - unexpected behavior combinations | Low | Test with and without other migration-related feature gates |
| Network partitioning between clusters during proxy forwarding | High - migration may hang or fail ungracefully | Medium | Test network interruption scenarios; verify timeout and cleanup |

#### **6. Test Scenarios & Requirements Mapping**

##### **6.1. Validated Requirements**

| Requirement ID | Requirement Summary | Source | Evidence | Priority | Test Tier |
|:---------------|:-------------------|:-------|:---------|:---------|:----------|
| CNV-76508 | TCP proxy correctly opens and maps ports for CCLM migration traffic on both source and target sync controllers | regression_analysis | `ProxyMappingManager.OpenProxyPorts()` called by `handleTargetState()` (line 707) and `SyncTargetMigrationStatus()` (line 1167) | P0 | Unit + Tier 1 |
| | Proxy forwards migration traffic bidirectionally (libvirt state + NBD block channels) without data loss | regression_analysis | `ProxyMapping.handleConnection()` uses `io.Copy` for bidirectional forwarding (line 296-331) | P0 | Tier 1 + Tier 2 |
| | Proxy ports are cleaned up on migration completion, failure, or cancellation | regression_analysis | `targetProxyManager.Close()` called in `deleteMigrationFunc()` (line 218), `handleSourceState()` (line 653), `handleTargetState()` (line 744) | P0 | Unit + Tier 1 |
| | `DecentralizedLiveMigration` feature gate enables/disables proxy functionality | regression_analysis | Feature gate registered as Alpha in `featuregate/active.go` (line 115, 257); referenced across 13 files including admission webhooks and virt-operator | P0 | Tier 1 |
| | `crossClusterNetwork` configuration field is validated and propagated to sync controller deployment | regression_analysis | New API field in `types.go`; virt-operator `deployments.go` uses it to configure sync controller pod network attachments | P1 | Tier 1 |
| | `synchronizationPlacement` allows scheduling sync controllers on specific nodes | regression_analysis | New `ComponentConfig` field in KubeVirt CR; virt-operator `deployments.go` (line 41+) applies node placement | P1 | Tier 1 |
| | Proxy enforces connection concurrency limit (64 per mapping) and rejects excess connections | regression_analysis | `connSem` channel with `maxConnectionsPerProxy=64` in `ProxyMapping.serve()` (line 282-293) | P1 | Unit |
| | SSRF protection blocks proxy to link-local and cloud metadata addresses | regression_analysis | `validateTargetAddress()` and `validateIP()` block 169.254.0.0/16 and fe80::/10 (proxy_mapping.go lines 83-126) | P1 | Unit |
| | Prometheus metrics track proxy connections, bytes transferred, and errors | regression_analysis | 3 new metrics in `decentralized_proxy_metrics.go`; registered in virt-handler metrics (line 2) | P1 | Tier 1 |
| CNV-76299 | Cross-cluster live migration completes end-to-end through proxy with reduced IP address requirements | regression_analysis | Full proxy flow: target opens CCLM ports → remaps VMI status → source opens in-cluster ports → migration proceeds | P0 | Tier 2 |
| | Proxy graceful shutdown on sync controller restart preserves migration state | regression_analysis | `closeConnections()` (line 295) calls `CloseAll()` on both proxy managers; `grpcServer.GracefulStop()` | P1 | Tier 1 |
| | Migration cancellation through proxy cleans up connections on both sides | regression_analysis | `cancelSourceRemoteMigration()` and `cancelTargetRemoteMigration()` close connections and proxy mappings | P2 | Tier 1 |
| | Existing non-proxy decentralized migrations work when `crossClusterNetwork` is not configured | regression_analysis | Proxy only activates when `targetState.DirectMigrationNodePorts != nil && targetState.NodeAddress != nil` and migration is decentralized | P1 | Tier 1 |

##### **6.2. Rejected Requirements (Out of Scope)**

| Requirement Summary | Reason | Gate Failed |
|:-------------------|:-------|:-----------|
| Multus NAD provisioning and CNI plugin configuration | Platform-level networking tested by networking infrastructure team | Requirement Level Validation |
| Kubernetes pod scheduling and node affinity behavior | Core K8s scheduler tested by platform upstream | Requirement Level Validation |
| gRPC transport layer and TLS certificate management | Existing functionality, not modified by this PR | Scope Boundary |
| Raw PVC binding and storage provisioning | Platform storage infrastructure tested by storage team | Requirement Level Validation |

##### **6.3. Test Scenario Details**

**TS-CNV-76508-001: Proxy Port Opening and Mapping (Unit)**
- **Tier:** Unit
- **Priority:** P0
- **Preconditions:** None (unit test)
- **Steps:**
    1. Create a `ProxyMappingManager`
    2. Call `OpenProxyPorts()` with a migration ID, bind address, target address, and port map (2 ports)
    3. Verify returned remapped ports map has same number of entries
    4. Verify each remapped port is a valid listening TCP port
    5. Verify `HasMappings()` returns true for the migration ID
- **Expected:** Two local TCP listeners are opened on random ports, remapped port map is returned correctly

**TS-CNV-76508-002: Proxy Port Cleanup on Close (Unit)**
- **Tier:** Unit
- **Priority:** P0
- **Preconditions:** Proxy ports opened via TS-001
- **Steps:**
    1. Call `Close()` with the migration ID
    2. Verify `HasMappings()` returns false
    3. Attempt to connect to the previously opened ports
- **Expected:** All listeners are closed, connections are refused, mappings are removed

**TS-CNV-76508-003: SSRF Protection - Link-Local Addresses Blocked (Unit)**
- **Tier:** Unit
- **Priority:** P1
- **Preconditions:** None
- **Steps:**
    1. Call `OpenProxyPorts()` with target address `169.254.169.254:8080` (cloud metadata)
    2. Call `OpenProxyPorts()` with target address `169.254.1.1:443` (link-local IPv4)
    3. Call `OpenProxyPorts()` with target address `[fe80::1]:8080` (link-local IPv6)
- **Expected:** All calls return error with "not allowed" message; no listeners are opened

**TS-CNV-76508-004: Connection Concurrency Limit (Unit)**
- **Tier:** Unit
- **Priority:** P1
- **Preconditions:** Proxy port opened with a target echo server
- **Steps:**
    1. Open 64 concurrent connections to the proxy port
    2. Attempt to open a 65th connection
- **Expected:** First 64 connections succeed; 65th connection is rejected (closed immediately)

**TS-CNV-76508-005: Bidirectional Traffic Forwarding (Unit)**
- **Tier:** Unit
- **Priority:** P0
- **Preconditions:** Proxy port opened with a target TCP server
- **Steps:**
    1. Connect to the proxy port
    2. Send data from client to proxy → target
    3. Send response data from target → proxy → client
- **Expected:** Data is forwarded correctly in both directions without corruption

**TS-CNV-76508-006: Feature Gate Enablement (Tier 1)**
- **Tier:** Tier 1
- **Priority:** P0
- **Preconditions:** OCP cluster with OpenShift Virtualization installed
- **Steps:**
    1. Verify `DecentralizedLiveMigration` feature gate is NOT enabled by default
    2. Enable `DecentralizedLiveMigration` in KubeVirt CR developer configuration
    3. Verify the feature gate is active
    4. Disable the feature gate and verify proxy-related configuration is rejected
- **Expected:** Feature gate correctly controls feature availability

**TS-CNV-76508-007: CrossClusterNetwork API Field (Tier 1)**
- **Tier:** Tier 1
- **Priority:** P1
- **Preconditions:** `DecentralizedLiveMigration` feature gate enabled
- **Steps:**
    1. Set `spec.configuration.migrations.crossClusterNetwork` in KubeVirt CR to a valid NAD name
    2. Verify KubeVirt CR is accepted and validated
    3. Verify sync controller deployment is updated with the network annotation
    4. Remove the field and verify sync controller deployment is updated accordingly
- **Expected:** API field is properly validated and propagated to sync controller deployment

**TS-CNV-76508-008: SynchronizationPlacement (Tier 1)**
- **Tier:** Tier 1
- **Priority:** P1
- **Preconditions:** `DecentralizedLiveMigration` feature gate enabled
- **Steps:**
    1. Set `spec.synchronizationPlacement` with node selector targeting specific worker nodes
    2. Verify sync controller pods are scheduled only on designated nodes
    3. Update placement to target different nodes
    4. Verify pods are rescheduled accordingly
- **Expected:** Sync controller pods respect placement constraints

**TS-CNV-76508-009: Proxy Metrics Emission (Tier 1)**
- **Tier:** Tier 1
- **Priority:** P1
- **Preconditions:** Proxy active during a cross-cluster migration
- **Steps:**
    1. Initiate a cross-cluster live migration through the proxy
    2. Query Prometheus for `kubevirt_decentralized_migration_proxy_active_connections`
    3. Query for `kubevirt_decentralized_migration_proxy_bytes_transferred_total`
    4. After migration completes, query for `kubevirt_decentralized_migration_proxy_errors_total`
- **Expected:** Active connections metric shows > 0 during migration, bytes transferred > 0, errors = 0 for successful migration

**TS-CNV-76508-010: Target-Side Proxy Port Remapping (Tier 1)**
- **Tier:** Tier 1
- **Priority:** P0
- **Preconditions:** Multi-cluster setup, `DecentralizedLiveMigration` enabled, `crossClusterNetwork` configured
- **Steps:**
    1. Trigger a cross-cluster migration targeting the local cluster
    2. Verify the target sync controller opens CCLM proxy ports
    3. Verify VMI status is updated with remapped port map (proxy ports instead of virt-handler ports)
    4. Verify VMI status NodeAddress points to sync controller CCLM IP
- **Expected:** Target proxy correctly remaps ports and updates VMI migration state

**TS-CNV-76508-011: Source-Side Proxy Port Remapping (Tier 1)**
- **Tier:** Tier 1
- **Priority:** P0
- **Preconditions:** Target-side proxy active (from TS-010)
- **Steps:**
    1. Verify the source sync controller receives the remapped target state
    2. Verify source sync controller opens in-cluster proxy ports
    3. Verify VMI status is updated with source-side remapped ports
    4. Verify VMI status TargetNodeAddress points to source sync controller in-cluster IP
- **Expected:** Source proxy correctly opens local ports and updates both legacy and new-style VMI migration state fields

**TS-CNV-76508-012: End-to-End Cross-Cluster Migration Through Proxy (Tier 2)**
- **Tier:** Tier 2
- **Priority:** P0
- **Preconditions:** 2 clusters configured with in-cluster and cross-cluster LM networks, `DecentralizedLiveMigration` enabled, `crossClusterNetwork` set
- **Steps:**
    1. Create a VM with a running workload on source cluster
    2. Initiate cross-cluster live migration to target cluster
    3. Verify migration completes successfully
    4. Verify VM is running on target cluster with workload intact
    5. Verify proxy ports are cleaned up on both clusters after migration
- **Expected:** VM migrates successfully through proxy, workload continues without interruption

**TS-CNV-76508-013: Proxy Cleanup on Migration Failure (Tier 1)**
- **Tier:** Tier 1
- **Priority:** P0
- **Preconditions:** Proxy active during migration
- **Steps:**
    1. Initiate a cross-cluster migration
    2. Induce migration failure (e.g., target node becomes unavailable)
    3. Verify proxy ports are cleaned up on both source and target
    4. Verify proxy error metrics are incremented
- **Expected:** All proxy resources are released on failure; error metric reflects the failure

**TS-CNV-76508-014: Migration Cancellation Through Proxy (Tier 1)**
- **Tier:** Tier 1
- **Priority:** P2
- **Preconditions:** Cross-cluster migration in progress through proxy
- **Steps:**
    1. Cancel the migration via `VirtualMachineInstanceMigration` deletion
    2. Verify `CancelMigration` gRPC call reaches the remote cluster
    3. Verify proxy ports are cleaned up on both sides
    4. Verify VM remains running on source cluster
- **Expected:** Migration is cancelled cleanly, proxy resources released, VM unaffected

**TS-CNV-76508-015: Regression - Non-Proxy Decentralized Migration (Tier 1)**
- **Tier:** Tier 1
- **Priority:** P1
- **Preconditions:** `DecentralizedLiveMigration` enabled but `crossClusterNetwork` NOT configured
- **Steps:**
    1. Initiate a decentralized migration without cross-cluster network configuration
    2. Verify migration proceeds without proxy (direct virt-handler connection)
    3. Verify no proxy ports are opened
- **Expected:** Existing non-proxy migration path is unaffected

**TS-CNV-76508-016: Proxy Resilience - Sync Controller Restart (Tier 1)**
- **Tier:** Tier 1
- **Priority:** P1
- **Preconditions:** Active proxy connections during migration
- **Steps:**
    1. Restart the sync controller pod during an active proxy session
    2. Verify `rebuildConnectionsAndUpdateSyncAddress()` reconstructs connections
    3. Verify migration either recovers or fails gracefully (not hung)
- **Expected:** Sync controller graceful shutdown closes all proxy connections; restart rebuilds state

**TS-CNV-76508-017: Operator Deployment of Sync Controller with CCLM Network (Tier 1)**
- **Tier:** Tier 1
- **Priority:** P1
- **Preconditions:** `DecentralizedLiveMigration` enabled, `crossClusterNetwork` configured
- **Steps:**
    1. Verify virt-operator creates sync controller Deployment with correct network annotations
    2. Verify sync controller pod has interfaces on both in-cluster LM and CCLM networks
    3. Update `crossClusterNetwork` value and verify deployment is updated
- **Expected:** virt-operator correctly configures sync controller deployment with multi-network support

#### **7. Requirements Traceability Matrix**

| Jira ID | Requirement | TS IDs | Tier | Priority |
|:--------|:-----------|:-------|:-----|:---------|
| CNV-76508 | TCP proxy port opening and mapping | TS-001, TS-002 | Unit | P0 |
| CNV-76508 | Bidirectional traffic forwarding | TS-005 | Unit | P0 |
| CNV-76508 | SSRF protection | TS-003 | Unit | P1 |
| CNV-76508 | Connection concurrency limit | TS-004 | Unit | P1 |
| CNV-76508 | Feature gate enablement | TS-006 | Tier 1 | P0 |
| CNV-76508 | CrossClusterNetwork API field | TS-007 | Tier 1 | P1 |
| CNV-76508 | SynchronizationPlacement | TS-008 | Tier 1 | P1 |
| CNV-76508 | Proxy metrics | TS-009 | Tier 1 | P1 |
| CNV-76508 | Target-side proxy remapping | TS-010 | Tier 1 | P0 |
| CNV-76508 | Source-side proxy remapping | TS-011 | Tier 1 | P0 |
| CNV-76508 | Proxy cleanup on failure | TS-013 | Tier 1 | P0 |
| CNV-76508 | Migration cancellation | TS-014 | Tier 1 | P2 |
| CNV-76508 | Non-proxy regression | TS-015 | Tier 1 | P1 |
| CNV-76508 | Sync controller restart resilience | TS-016 | Tier 1 | P1 |
| CNV-76508 | Operator deployment with CCLM network | TS-017 | Tier 1 | P1 |
| CNV-76299 | End-to-end cross-cluster migration | TS-012 | Tier 2 | P0 |

#### **8. Test Schedule**

| Phase | Activities | Dependencies |
|:------|:----------|:-------------|
| Phase 1 (Post-merge) | Unit tests (TS-001 through TS-005) - proxy mapping, SSRF, concurrency | PR #17922 merged |
| Phase 2 | Tier 1 functional tests (TS-006 through TS-011, TS-013 through TS-017) | Single-cluster environment with feature gate |
| Phase 3 | Tier 2 E2E tests (TS-012) | Multi-cluster test infrastructure available |

---

### **III. Regression Impact Analysis (LSP-Based)**

#### **Call Graph Summary**

The LSP analysis traced the following dependency chains for the new proxy functionality:

1. **`ProxyMappingManager.OpenProxyPorts()`** (proxy_mapping.go:138)
   - Called by `handleTargetState()` (synchronization-controller.go:707) — target-side proxy
   - Called by `SyncTargetMigrationStatus()` (synchronization-controller.go:1167) — source-side proxy
   - Referenced in 4 test files with 17 total references

2. **`DecentralizedLiveMigration` feature gate** (featuregate/active.go:115)
   - Referenced across 13 files:
     - Admission webhooks: `migration-create-admitter.go`, `vms-admitter.go`
     - Operator: `core.go`, `reconcile.go`, `update.go`
     - Config: `feature-gates.go`
     - Tests: `operator.go`, `kubevirtresource.go`

3. **`validateTargetAddress()`** (proxy_mapping.go:83)
   - Called only from `OpenProxyPorts()` — internal validation, well-encapsulated

4. **Sync controller struct fields** `targetProxyManager` and `sourceProxyManager`:
   - Initialized in `NewSynchronizationController()` (line 141-142)
   - Cleaned up in `closeConnections()` (line 308-309)
   - Used in `deleteMigrationFunc()` (lines 218, 224)

#### **Components Affected**

| Component | Files Modified | Impact |
|:----------|:--------------|:-------|
| synchronization-controller | `synchronization-controller.go`, `proxy_mapping.go` | Core: new proxy layer added |
| virt-config/featuregate | `active.go`, `feature-gates.go` | Feature gate registration |
| virt-operator | `deployments.go`, `core.go`, `config.go` | Sync controller deployment with CCLM network |
| virt-controller | `vm.go` | Minor: VM controller awareness |
| virt-handler | `migration-target.go` | Minor: migration target awareness |
| virt-launcher | `live-migration-source.go` | Minor: migration source awareness |
| API types | `types.go` | New fields: `crossClusterNetwork`, `synchronizationPlacement` |
| network/multus | `status.go` | Network status parsing for CCLM network |
| monitoring | `decentralized_proxy_metrics.go` | 3 new Prometheus metrics |

#### **LSP Diagnostics Notes**

- `proxy_mapping.go`: `proxyIdleTimeout` constant is defined but unused (line 39) — may indicate planned timeout feature not yet implemented
- `synchronization-controller.go`: Uses deprecated `golang.org/x/net/context` (line 46) — should use stdlib `context` package
