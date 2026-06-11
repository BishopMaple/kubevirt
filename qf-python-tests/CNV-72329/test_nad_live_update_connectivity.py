"""
NAD Reference Live Update — Connectivity and Multi-Interface E2E Tests

Validates end-to-end connectivity after NAD swap and simultaneous multi-interface
NAD reference updates. Covers STD scenarios TS-CNV-72329-003 and TS-CNV-72329-005.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
PR: https://github.com/kubevirt/kubevirt/pull/16412
"""

import logging

import pytest
from ocp_resources.virtual_machine_instance_migration import VirtualMachineInstanceMigration
from timeout_sampler import TimeoutSampler

from conftest import (
    MIGRATION_POLL_INTERVAL,
    NAD_SWAP_TIMEOUT,
    patch_nad_reference,
    wait_for_nad_swap_migration,
)
from utilities.network import assert_ping_successful, get_vmi_ip_v4_by_name

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")


@pytest.mark.polarion("CNV-72329")
class TestNadLiveUpdateConnectivity:
    """
    Tests for VM network connectivity after NAD reference live update.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Source bridge-based NAD created
        - Target bridge-based NAD created
        - VM created with secondary interface attached to source NAD
        - VM started and in Running state
        - Peer VM created and running on target network
    """

    def test_connectivity_on_new_network_after_nad_swap(
        self,
        admin_client,
        nad_swap_vm_scope_class,
        source_bridge_nad_scope_class,
        target_bridge_nad_scope_class,
        peer_vm_on_target_nad_scope_class,
    ):
        """
        Test that VM establishes connectivity on new network after NAD swap.

        TS-CNV-72329-003

        Preconditions:
            - Running VM with secondary bridge interface on source NAD
            - Peer VM running on target network

        Steps:
            1. Patch VM spec to change secondary network NAD reference from source to target
            2. Wait for auto-migration to complete
            3. Execute ping from VM to peer VM on target network

        Expected:
            - Ping from VM to peer VM on target network succeeds with 0% packet loss
        """
        vm = nad_swap_vm_scope_class

        LOGGER.info(
            f"Patching VM {vm.name} NAD reference from "
            f"{source_bridge_nad_scope_class.name} to {target_bridge_nad_scope_class.name}"
        )
        patch_nad_reference(
            networks=vm.instance.spec.template.spec.networks,
            old_nad_name=source_bridge_nad_scope_class.name,
            new_nad_name=target_bridge_nad_scope_class.name,
        )
        vm.update(resource_dict=vm.instance.to_dict())

        migration = wait_for_nad_swap_migration(
            admin_client=admin_client,
            namespace=vm.namespace,
        )
        assert migration is not None, "Migration was not triggered after NAD reference change"
        assert migration.instance.status.phase == "Succeeded", (
            f"Migration {migration.name} did not succeed: {migration.instance.status.phase}"
        )

        peer_ip = get_vmi_ip_v4_by_name(
            vm=peer_vm_on_target_nad_scope_class,
            name=target_bridge_nad_scope_class.name,
        )
        LOGGER.info(
            f"Verifying connectivity from {vm.name} "
            f"to peer VM at {peer_ip} on target network"
        )
        assert_ping_successful(
            src_vm=nad_swap_vm_scope_class,
            dst_ip=peer_ip,
        )

    def test_unreachable_on_old_network_after_nad_swap(
        self,
        nad_swap_vm_scope_class,
        source_bridge_nad_scope_class,
    ):
        """
        Test that VM is unreachable on old network after NAD swap.

        TS-CNV-72329-003

        Preconditions:
            - NAD swap and migration already completed (from previous test)
            - VM previously attached to source NAD, now on target NAD

        Steps:
            1. Retrieve VM's interface status for the old source NAD name
            2. Verify the source NAD name is no longer present in VMI network status

        Expected:
            - VM is no longer attached to source network
        """
        vm = nad_swap_vm_scope_class
        vmi = vm.vmi

        LOGGER.info(
            f"Verifying VM {vm.name} is no longer on source network "
            f"{source_bridge_nad_scope_class.name}"
        )
        vmi_network_names = [
            net.get("multus", {}).get("networkName", "")
            for net in vmi.instance.spec.networks
            if net.get("multus")
        ]
        assert not any(
            name.endswith(source_bridge_nad_scope_class.name) for name in vmi_network_names
        ), (
            f"VM should no longer reference source NAD {source_bridge_nad_scope_class.name}, "
            f"but VMI networks contain: {vmi_network_names}"
        )


@pytest.mark.polarion("CNV-72329")
class TestNadLiveUpdateMultiInterface:
    """
    Tests for simultaneous NAD updates on VM with multiple secondary interfaces.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Four bridge-based NADs created (source, source-2, target, target-2)
        - VM created with two secondary interfaces on source and source-2
        - VM started and in Running state
    """

    def test_simultaneous_nad_updates_single_migration(
        self,
        admin_client,
        multi_interface_vm_scope_class,
        source_bridge_nad_scope_class,
        source_bridge_nad_2_scope_class,
        target_bridge_nad_scope_class,
        target_bridge_nad_2_scope_class,
    ):
        """
        Test that multiple NAD references can be updated with a single migration.

        TS-CNV-72329-005

        Preconditions:
            - Running VM with two secondary interfaces on source NADs

        Steps:
            1. Patch VM spec to change both NAD references simultaneously
               (source to target, source-2 to target-2)
            2. Wait for migration to complete
            3. Count migration objects in the namespace

        Expected:
            - Only one migration occurs for the batch NAD update
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

    def test_all_interfaces_on_new_networks_after_multi_nad_swap(
        self,
        multi_interface_vm_scope_class,
        target_bridge_nad_scope_class,
        target_bridge_nad_2_scope_class,
    ):
        """
        Test that all interfaces connect to their respective new networks after multi-NAD swap.

        TS-CNV-72329-005

        Preconditions:
            - Multi-NAD swap and migration already completed (from previous test)

        Steps:
            1. Read VMI spec networks for both secondary interfaces
            2. Verify first interface references target NAD
            3. Verify second interface references target-2 NAD

        Expected:
            - Both secondary interfaces are connected to their respective target networks
        """
        vm = multi_interface_vm_scope_class
        vmi = vm.vmi

        vmi_nad_names = [
            net.get("multus", {}).get("networkName", "")
            for net in vmi.instance.spec.networks
            if net.get("multus")
        ]
        LOGGER.info(f"Post multi-NAD swap VMI network references: {vmi_nad_names}")

        assert any(
            name.endswith(target_bridge_nad_scope_class.name) for name in vmi_nad_names
        ), (
            f"First interface should reference {target_bridge_nad_scope_class.name}, "
            f"got {vmi_nad_names}"
        )
        assert any(
            name.endswith(target_bridge_nad_2_scope_class.name) for name in vmi_nad_names
        ), (
            f"Second interface should reference {target_bridge_nad_2_scope_class.name}, "
            f"got {vmi_nad_names}"
        )
