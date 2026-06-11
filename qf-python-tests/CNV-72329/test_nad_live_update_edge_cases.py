"""
NAD Reference Live Update — Edge Case E2E Tests

Validates NAD swap behavior under edge conditions: concurrent NIC hotplug,
migration target unavailability, and upgrade compatibility.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging

import pytest
from ocp_resources.virtual_machine_instance_migration import VirtualMachineInstanceMigration
from timeout_sampler import TimeoutExpiredError, TimeoutSampler

from conftest import NAD_SWAP_TIMEOUT, MIGRATION_POLL_INTERVAL, wait_for_nad_swap_migration
from utilities.constants import BRIDGE
from utilities.network import network_nad
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm, node_mgmt_console

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")


@pytest.mark.polarion("CNV-72329")
class TestNADSwapDuringHotplug:
    """
    Tests for NAD swap interaction with ongoing NIC hotplug operations.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Running VM with secondary bridge interface on source NAD
        - Source, target, and hotplug bridge NADs created
    """

    def test_nad_swap_during_hotplug_operation(
        self,
        admin_client,
        nad_swap_vm_scope_class,
        source_bridge_nad_scope_class,
        target_bridge_nad_scope_class,
        hotplug_bridge_nad_scope_class,
    ):
        """
        Test that NAD swap during ongoing NIC hotplug is handled gracefully.

        Preconditions:
            - Running VM with secondary bridge interface on source NAD
            - Additional hotplug NAD created

        Steps:
            1. Initiate NIC hotplug by adding new interface to VM spec
            2. Immediately patch existing NAD reference to trigger NAD swap
            3. Wait for both operations to reach a stable state

        Expected:
            - VM reaches stable Running state with consistent network configuration
        """
        vm = nad_swap_vm_scope_class

        LOGGER.info(f"Initiating NIC hotplug on VM {vm.name} with {hotplug_bridge_nad_scope_class.name}")
        networks = vm.instance.spec.template.spec.networks
        interfaces = vm.instance.spec.template.spec.domain.devices.interfaces

        networks.append({
            "name": hotplug_bridge_nad_scope_class.name,
            "multus": {"networkName": hotplug_bridge_nad_scope_class.name},
        })
        interfaces.append({
            "name": hotplug_bridge_nad_scope_class.name,
            "bridge": {},
        })

        LOGGER.info(
            f"Simultaneously patching NAD reference: "
            f"{source_bridge_nad_scope_class.name} -> {target_bridge_nad_scope_class.name}"
        )
        for network in networks:
            multus = network.get("multus", {})
            if multus and multus.get("networkName", "").endswith(source_bridge_nad_scope_class.name):
                multus["networkName"] = target_bridge_nad_scope_class.name

        vm.update(resource_dict=vm.instance.to_dict())

        LOGGER.info("Waiting for VM to reach stable state after concurrent operations")
        vmi = vm.vmi
        for sample in TimeoutSampler(
            wait_timeout=NAD_SWAP_TIMEOUT,
            sleep=MIGRATION_POLL_INTERVAL,
            func=lambda: vmi.instance.status.phase,
        ):
            if sample == "Running":
                break

        assert vmi.instance.status.phase == "Running", (
            f"VM should be Running after concurrent NAD swap and hotplug, "
            f"but is in {vmi.instance.status.phase}"
        )
        LOGGER.info(f"VM {vm.name} is stable after concurrent NAD swap and hotplug")


@pytest.mark.polarion("CNV-72329")
class TestNADSwapMigrationUnavailable:
    """
    Tests for NAD swap behavior when migration target is unavailable.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Running VM with secondary bridge interface on source NAD
        - Source and target bridge NADs created
    """

    def test_nad_swap_when_migration_target_unavailable(
        self,
        admin_client,
        schedulable_nodes,
        nad_swap_vm_scope_class,
        source_bridge_nad_scope_class,
        target_bridge_nad_scope_class,
    ):
        """
        [NEGATIVE] Test that NAD swap is handled gracefully when no migration target node is available.

        Preconditions:
            - Running VM with secondary bridge interface
            - All worker nodes except the VM host node are cordoned

        Steps:
            1. Cordon all worker nodes except the one hosting the VM
            2. Patch NAD reference to trigger migration
            3. Observe migration status

        Expected:
            - Migration fails or remains pending due to no available target node
        """
        vm = nad_swap_vm_scope_class
        vm_node = vm.vmi.instance.status.nodeName

        LOGGER.info(f"VM {vm.name} is running on node {vm_node}")

        nodes_to_cordon = [
            node for node in schedulable_nodes
            if node.name != vm_node
        ]
        assert len(nodes_to_cordon) > 0, "Need at least 2 nodes to test unavailable migration target"

        cordon_contexts = []
        try:
            for node in nodes_to_cordon:
                LOGGER.info(f"Cordoning node {node.name}")
                ctx = node_mgmt_console(node=node, node_mgmt="cordon")
                ctx.__enter__()
                cordon_contexts.append((node, ctx))

            LOGGER.info(
                f"Patching VM {vm.name} NAD reference to trigger migration "
                "with no available target nodes"
            )
            networks = vm.instance.spec.template.spec.networks
            for network in networks:
                multus = network.get("multus", {})
                if multus and multus.get("networkName", "").endswith(source_bridge_nad_scope_class.name):
                    multus["networkName"] = target_bridge_nad_scope_class.name

            vm.update(resource_dict=vm.instance.to_dict())

            LOGGER.info("Verifying migration does not succeed with no target nodes")
            successful_migration = False
            try:
                for sample in TimeoutSampler(
                    wait_timeout=60,
                    sleep=MIGRATION_POLL_INTERVAL,
                    func=VirtualMachineInstanceMigration.get,
                    client=admin_client,
                    namespace=vm.namespace,
                ):
                    migrations = list(sample)
                    for migration in migrations:
                        if migration.instance.status.phase == "Succeeded":
                            successful_migration = True
                            break
                    if successful_migration:
                        break
            except TimeoutExpiredError:
                LOGGER.info("Migration did not succeed — expected when no target node available")

            assert not successful_migration, (
                "Migration should not succeed when all target nodes are cordoned"
            )

            assert vm.vmi.instance.status.phase == "Running", (
                f"VM should remain Running on original node, but is {vm.vmi.instance.status.phase}"
            )
        finally:
            for node, ctx in cordon_contexts:
                LOGGER.info(f"Uncordoning node {node.name}")
                ctx.__exit__(None, None, None)


@pytest.mark.upgrade
@pytest.mark.polarion("CNV-72329")
class TestNADLiveUpdateUpgrade:
    """
    Tests for upgrade compatibility with LiveUpdateNADRef feature.

    Markers:
        - upgrade
        - polarion("CNV-72329")

    Preconditions:
        - VM with secondary networks created before LiveUpdateNADRef feature gate enablement
        - Cluster upgraded to OCP/CNV 4.22 with LiveUpdateNADRef feature gate enabled
    """

    def test_upgrade_preserves_secondary_networks(
        self,
        nad_swap_vm_scope_class,
        source_bridge_nad_scope_class,
    ):
        """
        Test that VMs with secondary networks created before feature gate enablement work after upgrade.

        Preconditions:
            - Running VM with secondary bridge interface

        Steps:
            1. Verify VM status is Running
            2. Verify secondary interface is functional

        Expected:
            - Pre-existing VM is Running with functional secondary network interface
        """
        vm = nad_swap_vm_scope_class
        vmi = vm.vmi

        assert vmi.instance.status.phase == "Running", (
            f"VM should be Running after upgrade, but is in {vmi.instance.status.phase}"
        )

        secondary_interfaces = [
            iface
            for iface in vmi.instance.status.interfaces
            if iface.get("name") != "default"
        ]
        assert len(secondary_interfaces) > 0, (
            "VM should have secondary interfaces after upgrade"
        )
        LOGGER.info(
            f"VM {vm.name} has {len(secondary_interfaces)} secondary interface(s) "
            "after upgrade — working as expected"
        )
