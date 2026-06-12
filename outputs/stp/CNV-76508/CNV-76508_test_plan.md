# Openshift-virtualization-tests Test plan

## **Cross-Cluster Live Migration Network Proxy - Quality Engineering Plan**

### **Metadata & Tracking**

| Field                  | Details                                                                                                     |
|:-----------------------|:------------------------------------------------------------------------------------------------------------|
| **Enhancement(s)**     | [VEP #192](https://github.com/kubevirt/enhancements/issues/192)                                             |
| **Feature in Jira**    | [VIRTSTRAT-96](https://redhat.atlassian.net/browse/VIRTSTRAT-96) — Live (hot) cross-cluster VM Migrations   |
| **Jira Tracking**      | [CNV-76299](https://redhat.atlassian.net/browse/CNV-76299) (Epic), [CNV-76508](https://redhat.atlassian.net/browse/CNV-76508) (Story) |
| **QE Owner(s)**        | [TBD]                                                                                                       |
| **Owning SIG**         | sig-compute / sig-network                                                                                   |
| **Participating SIGs** | sig-storage, sig-migration                                                                                  |
| **Current Status**     | Draft                                                                                                       |

**Document Conventions:**
- **CCLM** — Cross-Cluster Live Migration
- **NAD** — NetworkAttachmentDefinition
- **Sync Controller** — Synchronization Controller (virt-synchronization-controller)
- **migration0** — In-cluster migration network interface
- **crosscluster0** — Cross-cluster migration network interface
- **Proxy** — TCP proxy in the synchronization controller that bridges migration0 ↔ crosscluster0

### **Feature Overview**

This feature introduces a TCP proxy within the KubeVirt synchronization controller that enables cross-cluster live migration without requiring every virt-handler to have a direct IP address on the cross-cluster network. The synchronization controller attaches to both the in-cluster migration network (`migration0`) and the cross-cluster migration network (`crosscluster0`), proxying migration traffic between virt-handlers that only have in-cluster network access and the remote cluster's synchronization controller. This dramatically reduces IP address requirements on the cross-cluster network from N×(M+2) to N×2 addresses (N=clusters, M=virt-handlers per cluster), simplifying network configuration for administrators.

Key technical components:
- **CrossClusterMigrationProxy** feature gate (Alpha, v1.9.0)
- **SyncProxyManager** — manages source and target proxy listeners with OS-allocated ports
- **deadlineResettingReader** — prevents idle connection hangs during bidirectional TCP copy
- **crossClusterNetwork** field in `MigrationConfiguration` — new API field
- **synchronizationPlacement** field in `KubeVirtSpec` — controls sync controller scheduling
- Three new Prometheus metrics for proxy monitoring

---

### **I. Motivation and Requirements Review (QE Review Guidelines)**

#### **1. Requirement & User Story Review Checklist**

| Check                                  | Done | Details/Notes                                                                                                                                                                                                 | Comments |
|:---------------------------------------|:-----|:--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:---------|
| **Review Requirements**                | [x]  | Reviewed CNV-76508 (Story), CNV-76299 (Epic), VIRTSTRAT-96 (Feature). Requirements focus on reducing IP address count on CCLM network and simplifying NAD configuration.                                      |          |
| **Understand Value**                   | [x]  | As a network admin, I want to configure a CCLM NAD that doesn't require automation to figure out IP exclusion ranges. As a cluster admin, I want to define both in-cluster and cross-cluster LM networks.      |          |
| **Customer Use Cases**                 | [x]  | Workload balancing for seasonal peaks, hardware refresh migrations, disaster recovery — all require cross-cluster VM movement with minimal network admin overhead.                                              |          |
| **Testability**                        | [x]  | Requirements are testable: feature gate enablement, proxy creation, migration completion, metrics emission, and network attachment can all be verified programmatically.                                         |          |
| **Acceptance Criteria**                | [x]  | D/S criteria: Proxy reduces CCLM network IPs to 2 per cluster; migration succeeds via proxy; proxy metrics are emitted; sync controller attaches to crosscluster0.                                             |          |
| **Non-Functional Requirements (NFRs)** | [x]  | Performance: proxy should not add significant latency (<5% overhead). Monitoring: 3 new metrics. Security: TLS passthrough (no termination at proxy). Scalability: default max 5 concurrent migrations.        | Performance impact from double-hop networking should be measured |

#### **2. Technology and Design Review**

| Check                            | Done | Details/Notes                                                                                                                                                                        | Comments |
|:---------------------------------|:-----|:-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|:---------|
| **Developer Handoff/QE Kickoff** | [ ]  | Pending — PR #17922 is still open. Recommend scheduling kickoff with @awels once PR stabilizes.                                                                                      | **Action needed** |
| **Technology Challenges**        | [x]  | Bidirectional TCP proxy with idle timeout handling; dual-network attachment on sync controllers; OS-allocated port management; potential bottleneck with concurrent migrations.         |          |
| **Test Environment Needs**       | [x]  | Requires Multus with at least 2 additional networks (migration + crosscluster). Bridge CNI with whereabouts IPAM for crosscluster NAD. Multi-node cluster.                           |          |
| **API Extensions**               | [x]  | New fields: `MigrationConfiguration.CrossClusterNetwork` (string), `KubeVirtSpec.SynchronizationPlacement` (ComponentConfig). New feature gate: `CrossClusterMigrationProxy`.         |          |
| **Topology Considerations**      | [x]  | Cross-cluster scenario requires 2 clusters. Single-cluster proxy testing is possible using bridge NAD to simulate cross-cluster network (as done in upstream tests/migration/namespace.go). |          |


### **II. Software Test Plan (STP)**

#### **1. Scope of Testing**

**Testing Goals**

- **[P0]** Verify that enabling `CrossClusterMigrationProxy` feature gate with `crossClusterNetwork` configuration causes synchronization controllers to attach to the crosscluster0 network interface
- **[P0]** Verify VM live migration completes successfully through the proxy (source virt-handler → source sync proxy → target sync proxy → target virt-handler)
- **[P0]** Verify proxy port mapping is correctly established: both libvirt and NBD channels are proxied (protocol ports 0, 49152, 49153)
- **[P1]** Verify proxy metrics are emitted: `kubevirt_decentralized_migration_proxy_active_connections`, `kubevirt_decentralized_migration_proxy_bytes_transferred_total`, `kubevirt_decentralized_migration_proxy_errors_total`
- **[P1]** Verify proxy graceful shutdown: when migration completes, proxy listeners are cleaned up
- **[P1]** Verify `synchronizationPlacement` API field correctly controls sync controller scheduling on specific nodes
- **[P1]** Validate backward compatibility: clusters without `crossClusterNetwork` configured continue to use direct virt-handler connectivity
- **[P2]** Verify idle timeout behavior: proxy closes connections after 5 minutes of inactivity
- **[P2]** Verify proxy error handling: connection failures to unreachable targets are properly reported via metrics

**Out of Scope (Testing Scope Exclusions)**

| Out-of-Scope Item                                                       | Rationale                                                                    | PM/Lead Agreement |
|:------------------------------------------------------------------------|:-----------------------------------------------------------------------------|:------------------|
| Multi-cluster end-to-end testing (2 separate OCP clusters)              | Requires dedicated multi-cluster infrastructure; upstream uses simulated NAD | [ ] Name/Date     |
| Performance benchmarking of proxy overhead                              | Requires dedicated performance lab; tracked separately                       | [ ] Name/Date     |
| SSL/TLS termination at proxy (future enhancement)                       | Not implemented in this PR; TLS is passed through transparently              | [ ] Name/Date     |
| Testing with more than 5 concurrent migrations                          | Default max is 5; scale testing tracked separately                           | [ ] Name/Date     |

#### **2. Test Strategy**

| Item                           | Description                                                                                                                                                  | Applicable (Y/N or N/A) | Comments |
|:-------------------------------|:-------------------------------------------------------------------------------------------------------------------------------------------------------------|:------------------------|:---------|
| Functional Testing             | Validates proxy creation, port mapping, migration flow, and cleanup                                                                                          | Y                       | Core focus |
| Automation Testing             | All test cases will be automated in Ginkgo (Tier 1) and pytest (Tier 2)                                                                                      | Y                       |          |
| Performance Testing            | Measure proxy overhead on migration throughput and latency                                                                                                    | N/A                     | Deferred to performance team |
| Security Testing               | Verify TLS passthrough (proxy does not decrypt migration traffic)                                                                                             | Y                       | Verify end-to-end encryption |
| Usability Testing              | No UI changes                                                                                                                                                | N/A                     |          |
| Compatibility Testing          | Verify feature works with OVN-Kubernetes CNI and bridge CNI for crosscluster network                                                                         | Y                       |          |
| Regression Testing             | Verify existing decentralized live migration (without proxy) still works when feature gate is disabled                                                        | Y                       |          |
| Upgrade Testing                | Verify upgrade from version without proxy to version with proxy preserves migration functionality                                                             | Y                       |          |
| Backward Compatibility Testing | Clusters without CrossClusterMigrationProxy feature gate should function identically to before                                                                | Y                       |          |
| Dependencies                   | Depends on DecentralizedLiveMigration feature gate (prerequisite); Multus CNI                                                                                 | Y                       |          |
| Cross Integrations             | Sync controller interacts with virt-handler, virt-controller, virt-operator, and virt-launcher                                                                | Y                       | Regression analysis shows broad impact |
| Monitoring                     | 3 new Prometheus metrics must be validated                                                                                                                    | Y                       |          |
| Cloud Testing                  | Bare metal primary; cloud platforms require compatible secondary network plugins                                                                               | N/A                     | Bare metal focus for initial release |

#### **3. Test Environment**

| Environment Component                         | Configuration                                                                                             |
|:----------------------------------------------|:----------------------------------------------------------------------------------------------------------|
| **Cluster Topology**                          | 3-master/3-worker bare-metal (minimum 2 workers for migration)                                            |
| **OCP & OpenShift Virtualization Version(s)** | OCP 4.22 with OpenShift Virtualization 4.22                                                               |
| **CPU Virtualization**                        | Nodes with VT-x (Intel) or AMD-V (AMD) enabled in BIOS                                                   |
| **Compute Resources**                         | Minimum per worker node: 8 vCPUs, 32GB RAM                                                               |
| **Special Hardware**                          | N/A (bridge CNI sufficient for crosscluster network simulation)                                           |
| **Storage**                                   | RWX-capable StorageClass (ODF/Ceph RBD) for cross-namespace migration                                    |
| **Network**                                   | OVN-Kubernetes (default), Multus CNI required, bridge CNI for crosscluster NAD, whereabouts IPAM          |
| **Required Operators**                        | OpenShift Virtualization, NMState Operator (optional), Multus                                             |
| **Platform**                                  | Bare metal                                                                                                |
| **Special Configurations**                    | Two additional network-attachment-definitions: migration network (migration0) and crosscluster network (crosscluster0, e.g., 172.22.42.0/24) |

#### **3.1. Testing Tools & Frameworks**

| Category           | Tools/Frameworks                                                              |
|:-------------------|:------------------------------------------------------------------------------|
| **Test Framework** | Ginkgo v2 (Tier 1, upstream), pytest (Tier 2, downstream)                     |
| **CI/CD**          | Standard KubeVirt CI lanes with Multus-enabled cluster profile                |
| **Other Tools**    | Prometheus API for metrics validation; `kubectl`/`oc` for resource inspection |

#### **4. Entry Criteria**

- [x] Requirements and design documents are **approved and merged** (VEP #192)
- [ ] PR [#17922](https://github.com/kubevirt/kubevirt/pull/17922) is **merged**
- [ ] Test environment can be **set up and configured** with dual migration networks
- [ ] DecentralizedLiveMigration feature gate is functional (prerequisite feature)

#### **5. Risks**

| Risk Category        | Specific Risk for This Feature                                                                                              | Mitigation Strategy                                                                                    | Status |
|:---------------------|:----------------------------------------------------------------------------------------------------------------------------|:-------------------------------------------------------------------------------------------------------|:-------|
| Timeline/Schedule    | PR #17922 is large (8064+ additions) and still open; may not merge before code freeze                                        | Monitor PR status weekly; prepare test stubs in parallel                                               | [ ]    |
| Test Coverage        | Cannot test true multi-cluster scenario in standard CI; bridge NAD simulation may miss real network issues                   | Document limitation; plan targeted multi-cluster validation in staging environment                      | [ ]    |
| Test Environment     | Requires Multus with bridge CNI and whereabouts IPAM; not all CI environments have this                                      | Use dedicated Multus-enabled cluster profile; validate environment in test setup                        | [ ]    |
| Untestable Aspects   | Proxy performance under high concurrent migration load (>5); real cross-cluster network latency effects                      | Defer to performance team; document expected behavior based on design                                  | [ ]    |
| Resource Constraints | Feature spans sync controller, virt-operator, virt-handler, virt-launcher, and API — broad regression surface                | Focus automation on critical proxy path; leverage existing decentralized migration tests for regression | [ ]    |
| Dependencies         | Depends on DecentralizedLiveMigration feature gate which is also Alpha                                                       | Ensure both feature gates are tested together; verify independent disablement                           | [ ]    |
| Other                | Proxy could become bottleneck with many concurrent migrations; dual-hop adds latency                                         | Verify migration completes within acceptable timeout; monitor proxy metrics                             | [ ]    |

#### **6. Known Limitations**

- Feature is **Alpha** (v1.9.0) — API may change in future releases
- Proxy does **not** terminate TLS — certificates must still be valid between virt-handlers
- Maximum 5 concurrent migrations by default (existing KubeVirt limitation, not proxy-specific)
- Cross-cluster network IP allocation is static via NAD IPAM (whereabouts) — no dynamic DNS
- The proxy adds a network hop on each side (source virt-handler → source proxy → cross-cluster network → target proxy → target virt-handler), which may increase migration latency

---

### **III. Test Scenarios & Traceability**

| Requirement ID | Requirement Summary | Test Scenario(s) | Tier | Priority |
|:---------------|:--------------------|:------------------|:-----|:---------|
| CNV-76508 | As a proxy/synchronization controller, properly multiplex connections between clusters | TS-CNV-76508-001: Verify sync controller attaches to crosscluster0 network when `CrossClusterMigrationProxy` feature gate and `crossClusterNetwork` are configured | Tier 1 | P0 |
| CNV-76508 | As a proxy/synchronization controller, properly multiplex connections between clusters | TS-CNV-76508-002: Verify VM live migration completes successfully through proxy with both libvirt and NBD channels proxied | Tier 1 | P0 |
| CNV-76508 | As a proxy/synchronization controller, properly multiplex connections between clusters | TS-CNV-76508-003: Verify source proxy creates listeners on migration0 IP with OS-allocated ports and forwards to target proxy on crosscluster0 | Tier 1 | P0 |
| CNV-76508 | As a proxy/synchronization controller, properly multiplex connections between clusters | TS-CNV-76508-004: Verify target proxy creates listeners on crosscluster0 IP and forwards to target virt-handler ports | Tier 1 | P0 |
| CNV-76508 | As a proxy/synchronization controller, properly multiplex connections between clusters | TS-CNV-76508-005: Verify proxy port map values are correctly propagated: VMI migration status shows proxy ports instead of virt-handler ports | Tier 1 | P0 |
| CNV-76299 | Provide a proxy between cluster LM network and cross cluster LM network | TS-CNV-76508-006: Verify `crossClusterNetwork` API field is accepted in `MigrationConfiguration` and propagated to sync controller deployment | Tier 1 | P0 |
| CNV-76299 | Provide a proxy between cluster LM network and cross cluster LM network | TS-CNV-76508-007: Verify `synchronizationPlacement` API field controls node scheduling of sync controller pods | Tier 1 | P1 |
| CNV-76299 | Provide a proxy between cluster LM network and cross cluster LM network | TS-CNV-76508-008: Verify proxy metrics `kubevirt_decentralized_migration_proxy_active_connections` increments during migration and decrements after | Tier 2 | P1 |
| CNV-76299 | Provide a proxy between cluster LM network and cross cluster LM network | TS-CNV-76508-009: Verify proxy metrics `kubevirt_decentralized_migration_proxy_bytes_transferred_total` records data transfer in both directions | Tier 2 | P1 |
| CNV-76299 | Provide a proxy between cluster LM network and cross cluster LM network | TS-CNV-76508-010: Verify proxy metrics `kubevirt_decentralized_migration_proxy_errors_total` increments on connection failure | Tier 2 | P1 |
| CNV-76299 | Provide a proxy between cluster LM network and cross cluster LM network | TS-CNV-76508-011: Verify proxy cleanup — after migration completes, all proxy listeners are stopped and resources released | Tier 1 | P1 |
| CNV-76299 | Provide a proxy between cluster LM network and cross cluster LM network | TS-CNV-76508-012: Verify backward compatibility — decentralized migration without `crossClusterNetwork` configured works as before (direct virt-handler connectivity) | Tier 1 | P1 |
| CNV-76299 | Provide a proxy between cluster LM network and cross cluster LM network | TS-CNV-76508-013: Verify that disabling `CrossClusterMigrationProxy` feature gate while `crossClusterNetwork` is set does NOT create proxy (feature gate guards the behavior) | Tier 1 | P1 |
| VIRTSTRAT-96 | Live cross-cluster VM migrations | TS-CNV-76508-014: Verify VM is functional after migration through proxy — login succeeds and workload continues | Tier 2 | P0 |
| VIRTSTRAT-96 | Live cross-cluster VM migrations | TS-CNV-76508-015: Verify migration cancellation through proxy — cancelling an in-flight proxied migration cleanly shuts down proxy connections | Tier 2 | P1 |
| CNV-76508 | Proxy idle timeout behavior | TS-CNV-76508-016: Verify proxy closes idle connections after timeout period (5 minutes) and records idle_timeout error metric | Tier 2 | P2 |
| CNV-76508 | Proxy error handling | TS-CNV-76508-017: [NEGATIVE] Verify proxy records `connection_failed` error metric when target is unreachable | Tier 2 | P2 |
| CNV-76508 | Proxy idempotency | TS-CNV-76508-018: Verify calling StartTargetProxies/StartSourceProxies with same port map returns existing proxies without recreating | Tier 1 | P2 |
| CNV-76508 | Proxy shutdown safety | TS-CNV-76508-019: Verify proxy manager Shutdown() is idempotent and stops all active proxies | Tier 1 | P2 |
| CNV-76508 | Disk path updates during cross-cluster migration | TS-CNV-76508-020: Verify disk source file paths are correctly updated with target domain namespace and name during migration | Tier 1 | P1 |

---

### **Regression Impact Analysis**

Based on LSP call graph analysis of PR #17922 against the kubevirt/kubevirt codebase:

**Components Modified:**

| Component | Files Changed | Regression Risk |
|:----------|:-------------|:----------------|
| synchronization-controller | `synchronization-controller.go` (+556/-34), `migration-proxy.go` (new, 587 lines), `deadline_resetting_reader.go` (new, 80 lines) | **High** — Core proxy logic; `handleSourceState` and `handleTargetState` are called from `execute()` which runs in the main work loop |
| virt-operator | `deployments.go` (+41), `config.go` (+40/-1), `core.go` (+13/-10) | **Medium** — Adds crosscluster network annotation and sync placement to deployment generation; `GetMigrationNetwork` referenced by daemonsets.go and deployments.go |
| virt-launcher | `live-migration-source.go` (+26/-3) | **Medium** — Modified `updateFilePathsToNewDomain` with additional nil/empty checks; affects disk path resolution during migration |
| API types | `types.go` (+14) | **Low** — Additive only: new `CrossClusterMigrationInterfaceName` constant, `SynchronizationPlacement` field, `CrossClusterNetwork` field |
| Feature gates | `active.go` (+11), `feature-gates.go` (+4) | **Low** — Additive: new `CrossClusterMigrationProxy` gate registered as Alpha |
| virt-handler | `migration-target.go` (+4) | **Low** — Clears ephemeral migration state (SourceState/TargetState) on successful migration completion |
| virt-controller | `vm.go` (+3) | **Low** — Nil-check for annotations map before setting `CreateMigrationTarget` |
| Monitoring | `decentralized_proxy_metrics.go` (new, 73 lines) | **Low** — New metrics only; no changes to existing metrics |
| Network/Multus | `status.go` (+35) | **Low** — New `GetMigrationNetworkIPs` helper function; existing functions unchanged |

**Upstream Test Coverage (from PR):**
- `migration-proxy_test.go` — 525 lines of unit tests for proxy manager
- `deadline_resetting_reader_test.go` — 163 lines of reader tests
- `synchronization-controller_test.go` — Updated test expectations (+21/-13)
- `deployments_test.go` — 335 lines testing sync controller deployment generation
- `config_test.go` — 157 lines testing config propagation
- `live-migration-source_test.go` — 142 lines testing path update logic
- `tests/migration/namespace.go` — 175 lines of e2e proxy migration test

**Call Chain (LSP-traced):**
```
execute() → handleSourceState() / handleTargetState()
  → SyncProxyManager.StartSourceProxies() / StartTargetProxies()
    → createProxyListener() → migrationProxy.run()
      → handleConnection() → deadlineResettingReader.Read()
        → io.Copy (bidirectional)
```

**References to DecentralizedLiveMigration feature gate** span 13 files including admission webhooks, operator reconciliation, and test infrastructure — confirming broad integration surface.

---

### **IV. Sign-off and Approval**

This Software Test Plan requires approval from the following stakeholders:

* **Reviewers:**
  - [QE Engineer / @TBD]
  - [QE Peer / @TBD]
* **Approvers:**
  - [QE Lead / @TBD]
  - [Product Manager / @TBD]
