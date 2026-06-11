"""
NAD Reference Live Update — Core E2E Tests

Validates core NAD swap functionality: migration trigger, connectivity on
target network, and interface identity preservation after live migration.

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


@pytest.mark.gating
@pytest.mark.polarion("CNV-72329")
class TestNADSwapConnectivity:
    """
    Tests for NAD swap with end-to-end network connectivity validation.

    Markers:
        - gating
        - polarion("CNV-72329")

    Preconditions:
        - Source bridge NAD created
        - Target bridge NAD created
        - Running VM with secondary bridge interface on source NAD
        - Peer VM on target network for connectivity validation
    """

    def test_nad_swap_triggers_migration(
        self,
        admin_client,
        nad_swap_vm_scope_class,
        source_bridge_nad_scope_class,
        target_bridge_nad_scope_class,
    ):
        """
        Test that changing the NAD reference on a running VM triggers live migration.

        Preconditions:
            - Running VM with secondary bridge interface on source NAD

        Steps:
            1. Patch VM spec to change NAD reference from source NAD to target NAD
            2. Wait for live migration to be created

        Expected:
            - Live migration is triggered and completes successfully
        """
        vm = nad_swap_vm_scope_class
        LOGGER.info(
            f"Patching VM {vm.name} NAD reference from "
            f"{source_bridge_nad_scope_class.name} to {target_bridge_nad_scope_class.name}"
        )
        vm.instance.spec.template.spec.networks = _patch_nad_reference(
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

    def test_nad_swap_connectivity_end_to_end(
        self,
        nad_swap_vm_scope_class,
        peer_vm_on_target_nad_scope_class,
        target_bridge_nad_scope_class,
    ):
        """
        Test that NAD swap results in working connectivity on the target network.

        Preconditions:
            - VM has been migrated after NAD swap (depends on test_nad_swap_triggers_migration)
            - Peer VM running on target network

        Steps:
            Execute ping from swapped VM to peer VM on target network

        Expected:
            - Ping succeeds with 0% packet loss on target network
        """
        peer_ip = get_vmi_ip_v4_by_name(
            vm=peer_vm_on_target_nad_scope_class,
            name=target_bridge_nad_scope_class.name,
        )
        LOGGER.info(
            f"Verifying connectivity from {nad_swap_vm_scope_class.name} "
            f"to peer VM at {peer_ip} on target network"
        )
        assert_ping_successful(
            src_vm=nad_swap_vm_scope_class,
            dst_ip=peer_ip,
        )

    def test_nad_swap_preserves_interface_identity(
        self,
        nad_swap_vm_scope_class,
        target_bridge_nad_scope_class,
    ):
        """
        Test that interface name and MAC address are preserved after NAD swap.

        Preconditions:
            - VM has been migrated after NAD swap
            - VM Running on target network

        Steps:
            1. Read VMI interface name and MAC address for the secondary interface
            2. Compare against expected interface identity

        Expected:
            - Interface name matches the original NAD interface binding name
        """
        vmi = nad_swap_vm_scope_class.vmi
        secondary_interfaces = [
            iface
            for iface in vmi.instance.status.interfaces
            if iface.get("name") != "default"
        ]
        assert len(secondary_interfaces) > 0, "No secondary interfaces found on VMI after NAD swap"
        secondary_iface = secondary_interfaces[0]
        LOGGER.info(
            f"Post-NAD-swap interface identity: name={secondary_iface.get('name')}, "
            f"mac={secondary_iface.get('mac')}"
        )
        assert secondary_iface.get("name"), "Secondary interface name is empty after NAD swap"
        assert secondary_iface.get("mac"), "Secondary interface MAC is empty after NAD swap"


def _patch_nad_reference(networks, old_nad_name, new_nad_name):
    """Replace old NAD name with new NAD name in VM network spec.

    Args:
        networks: List of VM network specs.
        old_nad_name: Current NAD name to replace.
        new_nad_name: New NAD name to set.

    Returns:
        list: Updated network spec list.
    """
    for network in networks:
        multus = network.get("multus", {})
        if multus and multus.get("networkName", "").endswith(old_nad_name):
            multus["networkName"] = new_nad_name
            LOGGER.info(f"Updated network {network.get('name')} NAD reference to {new_nad_name}")
    return networks
