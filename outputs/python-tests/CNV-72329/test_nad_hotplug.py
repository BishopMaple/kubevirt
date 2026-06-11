"""
NAD Reference Hotplug End-to-End Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Tests for NAD (Network Attachment Definition) reference hotplug functionality,
validating that VMs can transparently switch between secondary networks via
live migration when the NAD reference is changed.
"""

import logging

import pytest

from utilities.network import assert_ping_successful, get_vmi_ip_v4_by_name
from utilities.virt import migrate_vm_and_verify
from tests.network.utils import assert_no_ping

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")

SECONDARY_NETWORK_NAME = "secondary"


def _patch_vm_nad_reference(vm, target_nad_name: str) -> None:
    """
    Patch VM spec to change the secondary network NAD reference.

    Args:
        vm: VirtualMachineForTests instance to patch
        target_nad_name: Name of the target NAD to switch to
    """
    LOGGER.info(f"Patching VM {vm.name} NAD reference to {target_nad_name}")
    vm.patch(
        body={
            "spec": {
                "template": {
                    "spec": {
                        "networks": [
                            {"name": "default", "pod": {}},
                            {
                                "name": SECONDARY_NETWORK_NAME,
                                "multus": {"networkName": target_nad_name},
                            },
                        ]
                    }
                }
            }
        }
    )


def _wait_for_migration_and_verify(vm) -> None:
    """
    Wait for the migration triggered by NAD change to complete.

    Args:
        vm: VirtualMachineForTests instance to wait on
    """
    LOGGER.info(f"Waiting for migration of VM {vm.name} to complete")
    migrate_vm_and_verify(vm=vm)


@pytest.mark.polarion("CNV-72329")
class TestNADHotplugConnectivity:
    """
    Tests for NAD reference hotplug connectivity validation.

    Markers:
        - gating

    Preconditions:
        - Two bridge-type NADs (source and target) with different bridge configurations
        - Running VM with secondary network interface attached via source NAD
        - Peer VM attached to target NAD for connectivity validation
    """

    @pytest.mark.gating
    @pytest.mark.polarion("CNV-72329-004")
    def test_nad_change_during_active_workload(
        self,
        source_nad_scope_class,
        target_nad_scope_class,
        vm_with_source_nad_scope_class,
    ):
        """
        Test that NAD change completes while VM has active network workload.

        Preconditions:
            - Continuous ping running from VM via secondary interface

        Steps:
            1. Patch VM spec to change networkName to target NAD
            2. Wait for migration to complete

        Expected:
            - Connectivity is restored on the new network after migration
        """
        vm = vm_with_source_nad_scope_class

        LOGGER.info(f"Starting continuous ping workload on VM {vm.name}")
        vm.ssh_exec.executor().run_command(
            command="ping -c 1000 -i 0.1 127.0.0.1 &",
        )

        _patch_vm_nad_reference(
            vm=vm,
            target_nad_name=target_nad_scope_class.name,
        )
        _wait_for_migration_and_verify(vm=vm)

        LOGGER.info("Verifying VM connectivity after NAD change during workload")
        target_ip = get_vmi_ip_v4_by_name(
            vm=vm,
            name=target_nad_scope_class.name,
        )
        assert target_ip, (
            f"VM {vm.name} did not acquire IP on target network "
            f"{target_nad_scope_class.name}"
        )

    @pytest.mark.gating
    @pytest.mark.polarion("CNV-72329-008")
    def test_connectivity_on_target_network_after_nad_change(
        self,
        source_nad_scope_class,
        target_nad_scope_class,
        vm_with_source_nad_scope_class,
        peer_vm_on_target_nad_scope_class,
    ):
        """
        Test that VM has network connectivity on target network after NAD change.

        Preconditions:
            - Peer VM running on target NAD with known IP address

        Steps:
            1. Patch VM spec to change networkName to target NAD
            2. Wait for migration to complete
            3. Execute ping from test VM to peer VM via secondary interface

        Expected:
            - Ping succeeds with 0% packet loss on target network
        """
        vm = vm_with_source_nad_scope_class
        peer_vm = peer_vm_on_target_nad_scope_class

        peer_ip = get_vmi_ip_v4_by_name(
            vm=peer_vm,
            name=target_nad_scope_class.name,
        )
        LOGGER.info(f"Peer VM IP on target network: {peer_ip}")

        _patch_vm_nad_reference(
            vm=vm,
            target_nad_name=target_nad_scope_class.name,
        )
        _wait_for_migration_and_verify(vm=vm)

        LOGGER.info(f"Verifying connectivity from VM {vm.name} to peer {peer_ip}")
        assert_ping_successful(
            src_vm=vm,
            dst_ip=peer_ip,
        )

    @pytest.mark.polarion("CNV-72329-009")
    def test_old_network_unreachable_after_nad_change(
        self,
        source_nad_scope_class,
        target_nad_scope_class,
        vm_with_source_nad_scope_class,
        peer_vm_on_source_nad_scope_class,
    ):
        """
        [NEGATIVE] Test that old network is unreachable after NAD change.

        Preconditions:
            - Peer VM running on source NAD with known IP address
            - NAD change from source to target completed with migration

        Steps:
            1. Execute ping from test VM to source network peer

        Expected:
            - Ping to source network peer fails
        """
        vm = vm_with_source_nad_scope_class
        source_peer_vm = peer_vm_on_source_nad_scope_class

        source_peer_ip = get_vmi_ip_v4_by_name(
            vm=source_peer_vm,
            name=source_nad_scope_class.name,
        )
        LOGGER.info(f"Source peer VM IP: {source_peer_ip}")

        _patch_vm_nad_reference(
            vm=vm,
            target_nad_name=target_nad_scope_class.name,
        )
        _wait_for_migration_and_verify(vm=vm)

        LOGGER.info(
            f"Verifying old network unreachable from VM {vm.name} "
            f"to source peer {source_peer_ip}"
        )
        assert_no_ping(
            src_vm=vm,
            dst_ip=source_peer_ip,
        )


@pytest.mark.polarion("CNV-72329")
class TestNADHotplugErrorHandling:
    """
    Tests for NAD reference hotplug error handling and recovery.

    Markers:
        - p1

    Preconditions:
        - Running VM with secondary network interface attached via valid NAD
    """

    @pytest.mark.polarion("CNV-72329-019")
    def test_graceful_handling_of_invalid_target_nad(
        self,
        source_nad_scope_class,
        vm_with_source_nad_scope_class,
    ):
        """
        [NEGATIVE] Test that invalid target NAD is handled gracefully.

        Steps:
            1. Patch VM NAD reference to a non-existent NAD name
            2. Wait for controller to evaluate the change
            3. Verify VM remains in Running phase

        Expected:
            - VM remains in Running phase
        """
        vm = vm_with_source_nad_scope_class
        nonexistent_nad_name = "nonexistent-nad-does-not-exist"

        _patch_vm_nad_reference(
            vm=vm,
            target_nad_name=nonexistent_nad_name,
        )

        LOGGER.info(
            f"Verifying VM {vm.name} remains running after invalid NAD change"
        )
        vm.wait_for_status(status=vm.Status.RUNNING, timeout=60)
        assert vm.instance.status.phase == "Running", (
            f"VM {vm.name} is not in Running phase after invalid NAD change: "
            f"{vm.instance.status.phase}"
        )

    @pytest.mark.polarion("CNV-72329-020")
    def test_vm_operational_after_failed_nad_change(
        self,
        source_nad_scope_class,
        target_nad_scope_class,
        vm_with_source_nad_scope_class,
    ):
        """
        [NEGATIVE] Test that VM is operational and recoverable after failed NAD change.

        Preconditions:
            - Failed NAD change attempted (reference to non-existent NAD)

        Steps:
            1. Patch VM NAD reference to a non-existent NAD
            2. Revert VM NAD reference to a valid target NAD
            3. Wait for migration to complete

        Expected:
            - VM recovers and is fully operational on valid NAD
        """
        vm = vm_with_source_nad_scope_class
        nonexistent_nad_name = "nonexistent-nad-recovery-test"

        LOGGER.info(f"Attempting invalid NAD change on VM {vm.name}")
        _patch_vm_nad_reference(
            vm=vm,
            target_nad_name=nonexistent_nad_name,
        )

        vm.wait_for_status(status=vm.Status.RUNNING, timeout=60)

        LOGGER.info(f"Reverting VM {vm.name} to valid target NAD")
        _patch_vm_nad_reference(
            vm=vm,
            target_nad_name=target_nad_scope_class.name,
        )
        _wait_for_migration_and_verify(vm=vm)

        LOGGER.info(f"Verifying VM {vm.name} is operational after recovery")
        target_ip = get_vmi_ip_v4_by_name(
            vm=vm,
            name=target_nad_scope_class.name,
        )
        assert target_ip, (
            f"VM {vm.name} did not acquire IP on target network after recovery"
        )
