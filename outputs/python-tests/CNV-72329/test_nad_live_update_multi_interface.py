"""
NAD Reference Live Update — Multi-Interface E2E Tests

Validates NAD swap behavior with VMs that have multiple secondary interfaces:
simultaneous NAD reference updates, partial updates, and multi-interface
connectivity verification.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging

import pytest
from ocp_resources.virtual_machine_instance_migration import VirtualMachineInstanceMigration
from timeout_sampler import TimeoutSampler

from conftest import NAD_SWAP_TIMEOUT, MIGRATION_POLL_INTERVAL, wait_for_nad_swap_migration
from utilities.network import assert_ping_successful, get_vmi_ip_v4_by_name
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")


@pytest.mark.polarion("CNV-72329")
class TestMultiInterfaceNADSwap:
    """
    Tests for multi-interface NAD swap end-to-end connectivity.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Two source bridge NADs created (source-net, source-net-2)
        - Two target bridge NADs created (target-net, target-net-2)
        - Running VM with two secondary bridge interfaces on source NADs
    """

    def test_multi_interface_nad_swap_single_migration(
        self,
        admin_client,
        multi_interface_vm_scope_class,
        source_bridge_nad_scope_class,
        source_bridge_nad_2_scope_class,
        target_bridge_nad_scope_class,
        target_bridge_nad_2_scope_class,
    ):
        """
        Test that simultaneous update of multiple NAD references triggers a single migration.

        Preconditions:
            - Running VM with two secondary interfaces on source NADs

        Steps:
            1. Patch both NAD references simultaneously in a single VM update
            2. Wait for migration to complete
            3. Count migration objects

        Expected:
            - Exactly one migration is triggered for all NAD changes
        """
        vm = multi_interface_vm_scope_class
        networks = vm.instance.spec.template.spec.networks

        LOGGER.info(f"Patching both NAD references on VM {vm.name}")
        for network in networks:
            multus = network.get("multus", {})
            if not multus:
                continue
            network_name = multus.get("networkName", "")
            if network_name.endswith(source_bridge_nad_scope_class.name):
                multus["networkName"] = target_bridge_nad_scope_class.name
            elif network_name.endswith(source_bridge_nad_2_scope_class.name):
                multus["networkName"] = target_bridge_nad_2_scope_class.name

        vm.update(resource_dict=vm.instance.to_dict())

        migration = wait_for_nad_swap_migration(
            admin_client=admin_client,
            namespace=vm.namespace,
        )
        assert migration is not None, "Migration was not triggered after multi-NAD reference change"
        assert migration.instance.status.phase == "Succeeded", (
            f"Migration {migration.name} did not succeed: {migration.instance.status.phase}"
        )

        migrations = list(
            VirtualMachineInstanceMigration.get(
                client=admin_client,
                namespace=vm.namespace,
            )
        )
        assert len(migrations) == 1, (
            f"Expected exactly 1 migration for simultaneous NAD changes, got {len(migrations)}"
        )

    def test_multi_interface_nad_swap_connectivity(
        self,
        multi_interface_vm_scope_class,
        target_bridge_nad_scope_class,
        target_bridge_nad_2_scope_class,
        peer_vm_on_target_nad_scope_class,
    ):
        """
        Test that multi-interface NAD swap results in connectivity on target networks.

        Preconditions:
            - VM has been migrated after multi-NAD swap
            - Peer VM running on target network

        Steps:
            Execute ping from swapped VM to peer VM on first target network

        Expected:
            - Ping succeeds with 0% packet loss on target network
        """
        peer_ip = get_vmi_ip_v4_by_name(
            vm=peer_vm_on_target_nad_scope_class,
            name=target_bridge_nad_scope_class.name,
        )
        LOGGER.info(
            f"Verifying connectivity from {multi_interface_vm_scope_class.name} "
            f"to peer VM at {peer_ip} on target network"
        )
        assert_ping_successful(
            src_vm=multi_interface_vm_scope_class,
            dst_ip=peer_ip,
        )


@pytest.mark.polarion("CNV-72329")
class TestPartialNADUpdate:
    """
    Tests for partial NAD update where only some interfaces are changed.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Running VM with two secondary bridge interfaces on different source NADs
        - Target bridge NAD created for one interface
    """

    def test_partial_nad_update_preserves_unchanged_interface(
        self,
        admin_client,
        multi_interface_vm_scope_class,
        source_bridge_nad_scope_class,
        source_bridge_nad_2_scope_class,
        target_bridge_nad_scope_class,
    ):
        """
        Test that partial NAD update changes only the specified interface.

        Preconditions:
            - Running VM with two secondary interfaces on different source NADs

        Steps:
            1. Patch only the first interface NAD reference to target NAD
            2. Wait for migration to complete
            3. Verify first interface references new NAD
            4. Verify second interface still references original NAD

        Expected:
            - Changed interface references target NAD after migration
            - Unchanged interface still references original source NAD
        """
        vm = multi_interface_vm_scope_class
        networks = vm.instance.spec.template.spec.networks

        LOGGER.info(
            f"Patching only first NAD reference on VM {vm.name}: "
            f"{source_bridge_nad_scope_class.name} -> {target_bridge_nad_scope_class.name}"
        )
        for network in networks:
            multus = network.get("multus", {})
            if multus and multus.get("networkName", "").endswith(source_bridge_nad_scope_class.name):
                multus["networkName"] = target_bridge_nad_scope_class.name
                break

        vm.update(resource_dict=vm.instance.to_dict())

        migration = wait_for_nad_swap_migration(
            admin_client=admin_client,
            namespace=vm.namespace,
        )
        assert migration is not None, "Migration was not triggered after partial NAD change"

        updated_networks = vm.instance.spec.template.spec.networks
        nad_names = [
            net.get("multus", {}).get("networkName", "")
            for net in updated_networks
            if net.get("multus")
        ]
        LOGGER.info(f"Post-migration NAD references: {nad_names}")

        assert any(
            name.endswith(target_bridge_nad_scope_class.name) for name in nad_names
        ), f"Changed interface should reference {target_bridge_nad_scope_class.name}, got {nad_names}"

        assert any(
            name.endswith(source_bridge_nad_2_scope_class.name) for name in nad_names
        ), f"Unchanged interface should still reference {source_bridge_nad_2_scope_class.name}, got {nad_names}"
