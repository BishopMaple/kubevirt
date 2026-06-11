"""
NAD Reference Live Update - Connectivity Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
PR: https://github.com/kubevirt/kubevirt/pull/16412
"""

import logging

import pytest

from libs.net.vmspec import lookup_iface_status_ip
from ocp_resources.virtual_machine import VirtualMachine
from ocp_resources.virtual_machine_instance import VirtualMachineInstance
from timeout_sampler import TimeoutSampler
from utilities.network import assert_ping_successful, get_vmi_ip_v4_by_name
from utilities.virt import (
    VirtualMachineForTests,
    fedora_vm_body,
    migrate_vm_and_verify,
    running_vm,
)

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")

MIGRATION_TIMEOUT = 600
MIGRATION_POLL_INTERVAL = 5
CONNECTIVITY_TIMEOUT = 120


def patch_vm_nad_reference(vm, nad_name, interface_index=0):
    """
    Patch VM spec to change the NAD reference on a secondary network interface.

    Args:
        vm: VirtualMachineForTests instance to patch.
        nad_name: Name of the target NAD to switch to.
        interface_index: Index of the network entry to patch (default 0).
    """
    LOGGER.info(f"Patching VM {vm.name} NAD reference to {nad_name} on interface index {interface_index}")
    patch_data = [
        {
            "op": "replace",
            "path": f"/spec/template/spec/networks/{interface_index}/multus/networkName",
            "value": nad_name,
        }
    ]
    vm.instance.patch(
        body=patch_data,
        content_type="application/json-patch+json",
    )
    LOGGER.info(f"VM {vm.name} NAD reference patched to {nad_name}")


def wait_for_migration_required_condition(vmi, timeout=MIGRATION_TIMEOUT):
    """
    Wait for MigrationRequired condition to appear on VMI.

    Args:
        vmi: VirtualMachineInstance to monitor.
        timeout: Seconds to wait for condition.

    Returns:
        True when MigrationRequired condition is present and True.
    """
    LOGGER.info(f"Waiting for MigrationRequired condition on VMI {vmi.name}")
    for sample in TimeoutSampler(
        wait_timeout=timeout,
        sleep=MIGRATION_POLL_INTERVAL,
        func=_has_migration_required_condition,
        vmi=vmi,
    ):
        if sample:
            LOGGER.info(f"MigrationRequired condition detected on VMI {vmi.name}")
            return True


def wait_for_migration_complete(vmi, timeout=MIGRATION_TIMEOUT):
    """
    Wait for migration to complete (MigrationRequired condition clears).

    Args:
        vmi: VirtualMachineInstance to monitor.
        timeout: Seconds to wait for migration completion.

    Returns:
        True when MigrationRequired condition is missing or false.
    """
    LOGGER.info(f"Waiting for migration to complete on VMI {vmi.name}")
    for sample in TimeoutSampler(
        wait_timeout=timeout,
        sleep=MIGRATION_POLL_INTERVAL,
        func=_migration_required_cleared,
        vmi=vmi,
    ):
        if sample:
            LOGGER.info(f"Migration completed on VMI {vmi.name}")
            return True


def _has_migration_required_condition(vmi):
    """Check if MigrationRequired condition is True on the VMI."""
    vmi_instance = vmi.instance
    if not hasattr(vmi_instance, "status") or not vmi_instance.status:
        return False
    conditions = vmi_instance.status.get("conditions", [])
    for condition in conditions:
        if condition.get("type") == "MigrationRequired" and condition.get("status") == "True":
            return True
    return False


def _migration_required_cleared(vmi):
    """Check if MigrationRequired condition is missing or false on the VMI."""
    vmi_instance = vmi.instance
    if not hasattr(vmi_instance, "status") or not vmi_instance.status:
        return True
    conditions = vmi_instance.status.get("conditions", [])
    for condition in conditions:
        if condition.get("type") == "MigrationRequired":
            return condition.get("status") != "True"
    return True


@pytest.mark.gating
@pytest.mark.polarion("CNV-72329")
class TestNadLiveUpdateConnectivity:
    """
    Tests for NAD reference live update migration and connectivity validation.

    Markers:
        - gating

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - VM rollout strategy set to LiveUpdate with LiveMigrate workload update
        - Source bridge-based NAD created (br-src-1)
        - Target bridge-based NAD created (br-tgt-2)
        - VM running with secondary interface on source NAD, guest agent connected
        - Static peer VMI running on source NAD for pre-swap connectivity verification
    """

    def test_nad_reference_change_triggers_live_migration(
        self,
        vm_with_source_nad_scope_class,
        target_nad_scope_class,
    ):
        """
        Test that NAD reference change on running VM triggers live migration.

        Scenario: TS-CNV-72329-001

        Steps:
            1. Patch VM spec to change NAD reference from source to target
            2. Wait for MigrationRequired condition to appear on VMI
            3. Wait for migration to complete (MigrationRequired clears)

        Expected:
            - MigrationRequired condition appears on VMI after NAD reference change
            - Live migration completes automatically
            - MigrationRequired condition is MissingOrFalse after migration
        """
        vm = vm_with_source_nad_scope_class
        vmi = vm.vmi

        patch_vm_nad_reference(
            vm=vm,
            nad_name=target_nad_scope_class.name,
        )

        wait_for_migration_required_condition(vmi=vmi)
        wait_for_migration_complete(vmi=vmi)

        LOGGER.info(f"VM {vm.name} migration completed after NAD reference change")

    def test_vm_connectivity_on_new_network_after_nad_swap(
        self,
        vm_with_source_nad_scope_class,
        target_nad_scope_class,
        peer_vmi_target_scope_class,
    ):
        """
        Test that VM has connectivity on new network after NAD swap.

        Scenario: TS-CNV-72329-002

        Preconditions:
            - NAD swap performed and migration completed
            - Peer VMI created on target NAD on same node as migrated VM

        Steps:
            1. Patch VM spec to change NAD reference to target NAD
            2. Wait for migration to complete
            3. Execute ping from peer VMI to migrated VM on target network

        Expected:
            - Ping from peer VMI on target network succeeds with 0% packet loss
        """
        vm = vm_with_source_nad_scope_class
        vmi = vm.vmi
        peer_vm = peer_vmi_target_scope_class

        patch_vm_nad_reference(
            vm=vm,
            nad_name=target_nad_scope_class.name,
        )
        wait_for_migration_complete(vmi=vmi)

        migrated_vm_ip = get_vmi_ip_v4_by_name(
            vm=vm,
            name=target_nad_scope_class.name,
        )
        LOGGER.info(f"Migrated VM IP on target network: {migrated_vm_ip}")

        assert_ping_successful(
            src_vm=peer_vm,
            dst_ip=migrated_vm_ip,
        )

    def test_nad_swap_during_active_network_workload(
        self,
        vm_with_source_nad_scope_class,
        source_nad_scope_class,
        target_nad_scope_class,
        peer_vmi_source_scope_class,
        peer_vmi_target_scope_class,
    ):
        """
        Test that NAD swap completes during active network workload.

        Scenario: TS-CNV-72329-004

        Preconditions:
            - VM running with secondary interface on source NAD
            - Continuous ping workload active between VM and peer on source network

        Steps:
            1. Verify pre-swap connectivity on source network
            2. Patch VM spec to change NAD reference while workload is active
            3. Wait for migration to complete

        Expected:
            - Migration completes despite active traffic
            - Connectivity established on target network after migration
        """
        vm = vm_with_source_nad_scope_class
        vmi = vm.vmi
        source_peer = peer_vmi_source_scope_class
        target_peer = peer_vmi_target_scope_class

        source_peer_ip = get_vmi_ip_v4_by_name(
            vm=source_peer,
            name=source_nad_scope_class.name,
        )
        LOGGER.info(f"Verifying pre-swap connectivity to source peer at {source_peer_ip}")
        assert_ping_successful(
            src_vm=vm,
            dst_ip=source_peer_ip,
        )

        LOGGER.info("Starting NAD swap while connectivity workload is active")
        patch_vm_nad_reference(
            vm=vm,
            nad_name=target_nad_scope_class.name,
        )
        wait_for_migration_complete(vmi=vmi)

        migrated_vm_ip = get_vmi_ip_v4_by_name(
            vm=vm,
            name=target_nad_scope_class.name,
        )
        LOGGER.info(f"Verifying post-swap connectivity on target network at {migrated_vm_ip}")
        assert_ping_successful(
            src_vm=target_peer,
            dst_ip=migrated_vm_ip,
        )

    def test_ping_to_peer_vm_on_target_network_after_swap(
        self,
        vm_with_source_nad_scope_class,
        target_nad_scope_class,
        peer_vmi_target_scope_class,
    ):
        """
        Test that migrated VM can ping peer VM on target network after NAD swap.

        Scenario: TS-CNV-72329-008

        Preconditions:
            - NAD swap performed and migration completed
            - Peer VMI running on target NAD with known IP address

        Steps:
            1. Patch VM NAD reference and wait for migration
            2. Execute ping from peer VMI to migrated VM on target network

        Expected:
            - Ping succeeds with 0% packet loss
        """
        vm = vm_with_source_nad_scope_class
        vmi = vm.vmi
        peer_vm = peer_vmi_target_scope_class

        patch_vm_nad_reference(
            vm=vm,
            nad_name=target_nad_scope_class.name,
        )
        wait_for_migration_complete(vmi=vmi)

        migrated_vm_ip = get_vmi_ip_v4_by_name(
            vm=vm,
            name=target_nad_scope_class.name,
        )
        LOGGER.info(f"Pinging migrated VM at {migrated_vm_ip} from target peer")

        assert_ping_successful(
            src_vm=peer_vm,
            dst_ip=migrated_vm_ip,
        )

    def test_no_connectivity_to_old_network_peers_after_swap(
        self,
        vm_with_source_nad_scope_class,
        source_nad_scope_class,
        target_nad_scope_class,
        peer_vmi_source_scope_class,
    ):
        """
        [NEGATIVE] Test that VM has no connectivity to old network peers after NAD swap.

        Scenario: TS-CNV-72329-009

        Preconditions:
            - Pre-swap connectivity verified (ping from source peer VMI succeeded)
            - NAD swap performed and migration completed

        Steps:
            1. Verify pre-swap connectivity on source network
            2. Perform NAD swap and wait for migration
            3. Execute ping from source peer VMI to migrated VM on old network

        Expected:
            - Ping from old network peer to migrated VM fails
        """
        vm = vm_with_source_nad_scope_class
        vmi = vm.vmi
        source_peer = peer_vmi_source_scope_class

        vm_ip_before = get_vmi_ip_v4_by_name(
            vm=vm,
            name=source_nad_scope_class.name,
        )
        LOGGER.info(f"Pre-swap VM IP on source network: {vm_ip_before}")
        assert_ping_successful(
            src_vm=source_peer,
            dst_ip=vm_ip_before,
        )

        patch_vm_nad_reference(
            vm=vm,
            nad_name=target_nad_scope_class.name,
        )
        wait_for_migration_complete(vmi=vmi)

        LOGGER.info(f"Verifying NO connectivity to old network peer at {vm_ip_before}")
        with pytest.raises(Exception):
            assert_ping_successful(
                src_vm=source_peer,
                dst_ip=vm_ip_before,
            )
