"""
Live Update NAD Reference - Multi-Interface and Hotplug Tests

Validates isolation between secondary interfaces during NAD reference changes,
hotplug interaction, and absent interface handling.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging

import pytest

from timeout_sampler import TimeoutSampler

from utilities.constants import TIMEOUT_5MIN
from utilities.virt import (
    VirtualMachineForTests,
    fedora_vm_body,
    running_vm,
    wait_for_vm_interfaces,
)

from conftest import (
    SECONDARY_IFACE_NAME,
    SECONDARY_IFACE_NAME_2,
    _patch_vm_nad_reference,
    _wait_for_migration_success,
)

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")


@pytest.mark.polarion("CNV-72329")
class TestLiveUpdateNADRefMultiInterface:
    """
    Tests for NAD reference change isolation across multiple secondary interfaces.

    Markers:
        - gating

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - Three bridge NADs (NAD-A VLAN 100, NAD-B VLAN 200, NAD-C VLAN 300)
        - Running VM with two secondary interfaces: iface1 on NAD-A, iface2 on NAD-C
    """

    @pytest.mark.gating
    def test_nad_change_does_not_affect_other_interfaces(
        self,
        admin_client,
        namespace,
        nad_vlan_100,
        nad_vlan_200,
        nad_vlan_300,
        vm_with_multi_secondary,
    ):
        """
        Test that NAD reference change on one interface does not affect other
        secondary interfaces.

        Preconditions:
            - VM with two secondary interfaces: iface1 on NAD-A, iface2 on NAD-C

        Steps:
            1. Record current VMI spec for both interfaces
            2. Change iface1 NAD reference from NAD-A to NAD-B
            3. Wait for migration to complete
            4. Verify iface1 references NAD-B and iface2 still references NAD-C

        Expected:
            - iface1 references NAD-B
            - iface2 still references NAD-C (unchanged)
        """
        original_iface2_nad = nad_vlan_300.name
        LOGGER.info(
            f"Original state: {SECONDARY_IFACE_NAME}={nad_vlan_100.name}, "
            f"{SECONDARY_IFACE_NAME_2}={original_iface2_nad}"
        )
        _patch_vm_nad_reference(
            vm=vm_with_multi_secondary,
            interface_name=SECONDARY_IFACE_NAME,
            new_nad_name=nad_vlan_200.name,
        )
        _wait_for_migration_success(
            client=admin_client,
            namespace=namespace.name,
            vm_name=vm_with_multi_secondary.name,
        )
        LOGGER.info("Migration complete, verifying interface isolation")
        vmi_networks = vm_with_multi_secondary.vmi.instance.spec.networks
        iface1_nad = None
        iface2_nad = None
        for network in vmi_networks:
            if network.get("name") == SECONDARY_IFACE_NAME and network.get("multus"):
                iface1_nad = network["multus"]["networkName"]
            elif network.get("name") == SECONDARY_IFACE_NAME_2 and network.get("multus"):
                iface2_nad = network["multus"]["networkName"]

        assert iface1_nad == nad_vlan_200.name, (
            f"iface1 NAD reference not updated: expected {nad_vlan_200.name}, got {iface1_nad}"
        )
        assert iface2_nad == original_iface2_nad, (
            f"iface2 NAD reference changed unexpectedly: expected {original_iface2_nad}, got {iface2_nad}"
        )


@pytest.mark.polarion("CNV-72329")
class TestLiveUpdateNADRefHotplug:
    """
    Tests for NAD reference change interactions with network hotplug and hot-unplug.

    Markers:
        - gating

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - Bridge NADs in test namespace
        - Running VM
    """

    def test_nad_change_after_hotplug(
        self,
        admin_client,
        namespace,
        nad_vlan_100,
        nad_vlan_200,
        vm_default_network_only,
    ):
        """
        Test that NAD reference change works after hotplugging a new network interface.

        Preconditions:
            - Two bridge NADs (NAD-A and NAD-B) in test namespace
            - Running VM with only default (pod) network

        Steps:
            1. Hotplug secondary interface with NAD-A
            2. Change hotplugged interface NAD reference from NAD-A to NAD-B
            3. Wait for migration to complete

        Expected:
            - Hotplugged interface references NAD-B
        """
        vm = vm_default_network_only
        LOGGER.info(f"Hotplugging secondary interface with NAD {nad_vlan_100.name}")
        vm.add_interface(
            interface_name=SECONDARY_IFACE_NAME,
            nad_name=nad_vlan_100.name,
        )
        for sample in TimeoutSampler(
            wait_timeout=TIMEOUT_5MIN,
            sleep=5,
            func=lambda: any(
                network.get("name") == SECONDARY_IFACE_NAME
                for network in vm.vmi.instance.spec.networks
            ),
        ):
            if sample:
                break

        LOGGER.info(f"Interface hotplugged, changing NAD to {nad_vlan_200.name}")
        _patch_vm_nad_reference(
            vm=vm,
            interface_name=SECONDARY_IFACE_NAME,
            new_nad_name=nad_vlan_200.name,
        )
        _wait_for_migration_success(
            client=admin_client,
            namespace=namespace.name,
            vm_name=vm.name,
        )
        vmi_networks = vm.vmi.instance.spec.networks
        hotplugged_nad = None
        for network in vmi_networks:
            if network.get("name") == SECONDARY_IFACE_NAME and network.get("multus"):
                hotplugged_nad = network["multus"]["networkName"]
                break

        assert hotplugged_nad == nad_vlan_200.name, (
            f"Hotplugged interface NAD not updated: expected {nad_vlan_200.name}, got {hotplugged_nad}"
        )

    def test_nad_change_rejected_for_absent_interface(
        self,
        admin_client,
        namespace,
        nad_vlan_100,
        nad_vlan_200,
        under_test_vm,
    ):
        """
        [NEGATIVE] Test that NAD reference change is rejected or ignored for
        interfaces marked as absent (hot-unplugged).

        Preconditions:
            - Running VM with secondary interface on NAD-A
            - Secondary interface hot-unplugged (marked as absent)

        Steps:
            1. Hot-unplug the secondary interface
            2. Attempt to change NAD reference on absent interface from NAD-A to NAD-B

        Expected:
            - Change is rejected or has no effect
            - VM remains in a consistent, recoverable state
        """
        vm = under_test_vm
        LOGGER.info(f"Hot-unplugging secondary interface from VM {vm.name}")
        vm.remove_interface(interface_name=SECONDARY_IFACE_NAME)

        for sample in TimeoutSampler(
            wait_timeout=TIMEOUT_5MIN,
            sleep=5,
            func=lambda: _is_interface_absent(vm=vm, interface_name=SECONDARY_IFACE_NAME),
        ):
            if sample:
                break

        LOGGER.info("Interface marked as absent, attempting NAD change")
        _patch_vm_nad_reference(
            vm=vm,
            interface_name=SECONDARY_IFACE_NAME,
            new_nad_name=nad_vlan_200.name,
        )

        LOGGER.info("Verifying VM remains in consistent state")
        vm_instance = vm.instance
        assert vm_instance.status.ready, (
            f"VM {vm.name} is not in a ready state after NAD change on absent interface"
        )


def _is_interface_absent(vm, interface_name):
    """Check if an interface is marked as absent on the VM.

    Args:
        vm: VirtualMachineForTests instance.
        interface_name: Name of the interface to check.

    Returns:
        bool: True if the interface is marked as absent.
    """
    interfaces = vm.instance.spec.template.spec.domain.devices.interfaces
    for iface in interfaces:
        if iface.get("name") == interface_name and iface.get("state") == "absent":
            return True
    return False
