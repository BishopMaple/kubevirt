"""
NAD Reference Live Update - Upgrade Persistence Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
PR: https://github.com/kubevirt/kubevirt/pull/16412
"""

import logging

import pytest

from timeout_sampler import TimeoutSampler
from utilities.network import assert_ping_successful, get_vmi_ip_v4_by_name, network_nad
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")

MIGRATION_TIMEOUT = 600
MIGRATION_POLL_INTERVAL = 5
UPGRADE_SOURCE_BRIDGE = "br-upg-src"
UPGRADE_TARGET_BRIDGE = "br-upg-tgt"


def _migration_cleared(vmi):
    """Check if MigrationRequired condition is missing or false on the VMI."""
    vmi_instance = vmi.instance
    if not hasattr(vmi_instance, "status") or not vmi_instance.status:
        return True
    conditions = vmi_instance.status.get("conditions", [])
    for condition in conditions:
        if condition.get("type") == "MigrationRequired":
            return condition.get("status") != "True"
    return True


@pytest.mark.polarion("CNV-72329")
class TestNadLiveUpdateUpgrade:
    """
    Tests for NAD reference live update persistence through OCP/CNV upgrade.

    Markers:
        - polarion
        - upgrade

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - VM rollout strategy set to LiveUpdate with LiveMigrate workload update
        - Source and target bridge-based NADs created
        - Upgrade channel configured for OCP/CNV cluster
    """

    def test_nad_swap_functionality_after_cluster_upgrade(
        self,
        admin_client,
        unprivileged_client,
        namespace,
    ):
        """
        Test that NAD swap functionality works after OCP/CNV upgrade.

        Scenario: TS-CNV-72329-022

        Preconditions:
            - NAD swap verified working before upgrade
            - Cluster upgrade completed

        Steps:
            1. Create source and target NADs
            2. Create VM with secondary interface on source NAD
            3. Patch VM to swap NAD reference to target
            4. Wait for migration to complete
            5. Verify connectivity on target network

        Expected:
            - NAD swap triggers migration after upgrade
            - Connectivity established on target network post-upgrade
        """
        with network_nad(
            nad_type="bridge",
            network_name=UPGRADE_SOURCE_BRIDGE,
            nad_name="upg-source-nad",
            namespace=namespace,
        ) as source_nad, network_nad(
            nad_type="bridge",
            network_name=UPGRADE_TARGET_BRIDGE,
            nad_name="upg-target-nad",
            namespace=namespace,
        ) as target_nad:
            vm_name = "upg-nad-swap-vm"
            with VirtualMachineForTests(
                client=unprivileged_client,
                name=vm_name,
                namespace=namespace.name,
                body=fedora_vm_body(name=vm_name),
                networks={source_nad.name: source_nad.name},
                interfaces=[source_nad.name],
            ) as vm:
                running_vm(vm=vm)
                vmi = vm.vmi

                LOGGER.info(f"Patching VM {vm.name} NAD reference to {target_nad.name}")
                patch_data = [
                    {
                        "op": "replace",
                        "path": "/spec/template/spec/networks/0/multus/networkName",
                        "value": target_nad.name,
                    },
                ]
                vm.instance.patch(
                    body=patch_data,
                    content_type="application/json-patch+json",
                )

                LOGGER.info("Waiting for migration to complete after NAD swap")
                for sample in TimeoutSampler(
                    wait_timeout=MIGRATION_TIMEOUT,
                    sleep=MIGRATION_POLL_INTERVAL,
                    func=_migration_cleared,
                    vmi=vmi,
                ):
                    if sample:
                        break

                peer_name = "upg-peer-vmi"
                with VirtualMachineForTests(
                    client=unprivileged_client,
                    name=peer_name,
                    namespace=namespace.name,
                    body=fedora_vm_body(name=peer_name),
                    networks={target_nad.name: target_nad.name},
                    interfaces=[target_nad.name],
                ) as peer_vm:
                    running_vm(vm=peer_vm)

                    migrated_vm_ip = get_vmi_ip_v4_by_name(
                        vm=vm,
                        name=target_nad.name,
                    )
                    LOGGER.info(
                        f"Verifying connectivity on target network: "
                        f"peer -> migrated VM at {migrated_vm_ip}"
                    )
                    assert_ping_successful(
                        src_vm=peer_vm,
                        dst_ip=migrated_vm_ip,
                    )

    def test_vms_with_changed_nads_persist_through_upgrade(
        self,
        admin_client,
        unprivileged_client,
        namespace,
    ):
        """
        Test that VMs with previously changed NADs persist through cluster upgrade.

        Scenario: TS-CNV-72329-023

        Preconditions:
            - VM with completed NAD swap running on target NAD
            - Connectivity on target network verified before upgrade
            - Cluster upgrade completed

        Steps:
            1. Create NADs and VM, perform NAD swap pre-upgrade
            2. Verify VM spec still references target NAD post-upgrade
            3. Execute ping to peer on target network post-upgrade

        Expected:
            - VM spec still references target NAD after upgrade
            - Network connectivity on target NAD preserved post-upgrade
        """
        with network_nad(
            nad_type="bridge",
            network_name=UPGRADE_SOURCE_BRIDGE,
            nad_name="persist-source-nad",
            namespace=namespace,
        ) as source_nad, network_nad(
            nad_type="bridge",
            network_name=UPGRADE_TARGET_BRIDGE,
            nad_name="persist-target-nad",
            namespace=namespace,
        ) as target_nad:
            vm_name = "persist-nad-vm"
            with VirtualMachineForTests(
                client=unprivileged_client,
                name=vm_name,
                namespace=namespace.name,
                body=fedora_vm_body(name=vm_name),
                networks={source_nad.name: source_nad.name},
                interfaces=[source_nad.name],
            ) as vm:
                running_vm(vm=vm)
                vmi = vm.vmi

                LOGGER.info(f"Performing pre-upgrade NAD swap on VM {vm.name}")
                patch_data = [
                    {
                        "op": "replace",
                        "path": "/spec/template/spec/networks/0/multus/networkName",
                        "value": target_nad.name,
                    },
                ]
                vm.instance.patch(
                    body=patch_data,
                    content_type="application/json-patch+json",
                )

                for sample in TimeoutSampler(
                    wait_timeout=MIGRATION_TIMEOUT,
                    sleep=MIGRATION_POLL_INTERVAL,
                    func=_migration_cleared,
                    vmi=vmi,
                ):
                    if sample:
                        break

                LOGGER.info("Verifying VM spec still references target NAD")
                vm_instance = vm.instance
                networks = vm_instance.spec["template"]["spec"].get("networks", [])
                multus_nad_names = [
                    net["multus"]["networkName"]
                    for net in networks
                    if "multus" in net
                ]
                assert target_nad.name in multus_nad_names, (
                    f"Target NAD {target_nad.name} not found in VM spec networks: {multus_nad_names}"
                )

                peer_name = "persist-peer-vmi"
                with VirtualMachineForTests(
                    client=unprivileged_client,
                    name=peer_name,
                    namespace=namespace.name,
                    body=fedora_vm_body(name=peer_name),
                    networks={target_nad.name: target_nad.name},
                    interfaces=[target_nad.name],
                ) as peer_vm:
                    running_vm(vm=peer_vm)

                    migrated_vm_ip = get_vmi_ip_v4_by_name(
                        vm=vm,
                        name=target_nad.name,
                    )
                    LOGGER.info(
                        f"Verifying post-upgrade connectivity: "
                        f"peer -> VM at {migrated_vm_ip} on target network"
                    )
                    assert_ping_successful(
                        src_vm=peer_vm,
                        dst_ip=migrated_vm_ip,
                    )
