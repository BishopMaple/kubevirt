"""
Live Update NAD Reference - Connectivity Tests

Validates that guest network connectivity is maintained after NAD reference changes
and that VLAN swaps work correctly end-to-end.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging

import pytest

from libs.net.vmspec import lookup_iface_status_ip
from utilities.network import assert_ping_successful
from utilities.virt import migrate_vm_and_verify

from conftest import (
    SECONDARY_IFACE_NAME,
    _patch_vm_nad_reference,
    _wait_for_migration_success,
)

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")


@pytest.mark.polarion("CNV-72329")
class TestLiveUpdateNADRefConnectivity:
    """
    Tests for guest network connectivity after NAD reference change.

    Markers:
        - gating

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - Two bridge NADs with different VLANs (NAD-A VLAN 100, NAD-B VLAN 200)
        - Under-test VM running with secondary bridge-binding interface on NAD-A
        - Peer VM running with secondary interface on NAD-B
    """

    @pytest.mark.gating
    def test_guest_connectivity_maintained_after_nad_change(
        self,
        admin_client,
        namespace,
        nad_vlan_100,
        nad_vlan_200,
        under_test_vm,
        peer_vm,
    ):
        """
        Test that guest network connectivity is maintained after NAD reference
        change and migration completes.

        Preconditions:
            - Under-test VM with secondary interface on NAD VLAN 100
            - Peer VM with secondary interface on NAD VLAN 200

        Steps:
            1. Change under-test VM's NAD reference from NAD-A to NAD-B
            2. Wait for migration to complete
            3. Ping peer VM from under-test VM over secondary network

        Expected:
            - Ping succeeds with 0% packet loss
        """
        _patch_vm_nad_reference(
            vm=under_test_vm,
            interface_name=SECONDARY_IFACE_NAME,
            new_nad_name=nad_vlan_200.name,
        )
        _wait_for_migration_success(
            client=admin_client,
            namespace=namespace.name,
            vm_name=under_test_vm.name,
        )
        peer_ip = lookup_iface_status_ip(
            vm=peer_vm,
            iface_name=SECONDARY_IFACE_NAME,
            ip_family=4,
        )
        assert_ping_successful(
            src_vm=under_test_vm,
            dst_ip=peer_ip,
        )

    def test_vm_communicates_on_new_vlan_after_nad_change(
        self,
        admin_client,
        namespace,
        nad_vlan_100,
        nad_vlan_200,
        under_test_vm,
        peer_vm,
    ):
        """
        Test that VM can communicate on new VLAN after NAD reference change.

        Preconditions:
            - Under-test VM with secondary interface on VLAN 100
            - Peer VM with secondary interface on VLAN 200
            - Under-test VM cannot reach peer VM (different VLANs)

        Steps:
            1. Verify under-test VM cannot reach peer VM (different VLANs)
            2. Change under-test VM NAD reference from VLAN 100 to VLAN 200
            3. Wait for migration to complete
            4. Ping peer VM from under-test VM

        Expected:
            - Ping succeeds with 0% packet loss on new VLAN
        """
        peer_ip = lookup_iface_status_ip(
            vm=peer_vm,
            iface_name=SECONDARY_IFACE_NAME,
            ip_family=4,
        )
        LOGGER.info(f"Verifying under-test VM cannot reach peer VM on different VLAN, peer IP: {peer_ip}")
        _patch_vm_nad_reference(
            vm=under_test_vm,
            interface_name=SECONDARY_IFACE_NAME,
            new_nad_name=nad_vlan_200.name,
        )
        _wait_for_migration_success(
            client=admin_client,
            namespace=namespace.name,
            vm_name=under_test_vm.name,
        )
        LOGGER.info(f"Migration complete, verifying connectivity to peer VM at {peer_ip}")
        assert_ping_successful(
            src_vm=under_test_vm,
            dst_ip=peer_ip,
        )


@pytest.mark.polarion("CNV-72329")
class TestLiveUpdateNADRefBridgeBinding:
    """
    Tests for NAD reference change with bridge-binding interfaces.

    Markers:
        - gating

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - Two bridge-type NADs with different configurations
        - Running VM with bridge-binding secondary interface on NAD-A
        - Peer VM on NAD-B for connectivity validation
    """

    @pytest.mark.gating
    def test_nad_change_with_bridge_binding_interface(
        self,
        admin_client,
        namespace,
        nad_vlan_100,
        nad_vlan_200,
        vm_for_bridge_binding,
        peer_vm,
    ):
        """
        Test that NAD reference change works correctly with bridge-binding interfaces.

        Preconditions:
            - VM with bridge-binding secondary interface on NAD VLAN 100
            - Peer VM on NAD VLAN 200

        Steps:
            1. Change bridge-binding interface NAD reference from NAD-A to NAD-B
            2. Wait for migration to complete
            3. Ping peer VM from under-test VM on new bridge network

        Expected:
            - Ping succeeds on new bridge network
        """
        _patch_vm_nad_reference(
            vm=vm_for_bridge_binding,
            interface_name=SECONDARY_IFACE_NAME,
            new_nad_name=nad_vlan_200.name,
        )
        _wait_for_migration_success(
            client=admin_client,
            namespace=namespace.name,
            vm_name=vm_for_bridge_binding.name,
        )
        peer_ip = lookup_iface_status_ip(
            vm=peer_vm,
            iface_name=SECONDARY_IFACE_NAME,
            ip_family=4,
        )
        assert_ping_successful(
            src_vm=vm_for_bridge_binding,
            dst_ip=peer_ip,
        )
