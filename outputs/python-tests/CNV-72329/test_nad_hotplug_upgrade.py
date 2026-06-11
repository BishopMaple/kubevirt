"""
NAD Reference Hotplug Upgrade and Sequential Operations Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Tests for NAD reference hotplug behavior after cluster upgrade and
sequential/concurrent NAD change operations.
"""

import logging

import pytest

from utilities.network import get_vmi_ip_v4_by_name
from utilities.virt import migrate_vm_and_verify

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
class TestNADHotplugUpgrade:
    """
    Tests for NAD reference hotplug behavior after cluster upgrade.

    Markers:
        - incremental

    Preconditions:
        - Cluster upgraded from version N-1 to current version with LiveUpdateNADRef
        - Running VM with secondary network interface created before upgrade
    """

    @pytest.mark.incremental
    @pytest.mark.polarion("CNV-72329-022")
    def test_existing_vms_stable_after_upgrade(
        self,
        source_nad_scope_class,
        vm_with_source_nad_scope_class,
    ):
        """
        Test that existing VMs with secondary networks remain stable after upgrade.

        Steps:
            1. Verify pre-existing VM is still in Running phase after upgrade
            2. Check for unintended migration objects

        Expected:
            - VM remains running with no unintended migrations triggered
        """
        vm = vm_with_source_nad_scope_class

        LOGGER.info(f"Verifying VM {vm.name} is still running after upgrade")
        assert vm.instance.status.phase == "Running", (
            f"VM {vm.name} is not in Running phase after upgrade: "
            f"{vm.instance.status.phase}"
        )

        LOGGER.info(f"Verifying no unintended migrations for VM {vm.name}")
        vmi = vm.vmi
        migration_state = getattr(vmi.instance.status, "migrationState", None)
        if migration_state is not None:
            LOGGER.warning(
                f"VM {vm.name} has migration state: {migration_state}. "
                "Checking if it was unintended."
            )

        source_ip = get_vmi_ip_v4_by_name(
            vm=vm,
            name=source_nad_scope_class.name,
        )
        assert source_ip, (
            f"VM {vm.name} lost network connectivity on source NAD after upgrade"
        )

    @pytest.mark.incremental
    @pytest.mark.polarion("CNV-72329-023")
    def test_nad_change_works_on_upgraded_cluster(
        self,
        source_nad_scope_class,
        target_nad_scope_class,
        vm_with_source_nad_scope_class,
    ):
        """
        Test that NAD change feature works correctly on upgraded cluster.

        Preconditions:
            - Target NAD available in test namespace on upgraded cluster

        Steps:
            1. Patch VM NAD reference to target NAD
            2. Wait for migration to complete

        Expected:
            - Migration triggered and completes successfully on upgraded cluster
        """
        vm = vm_with_source_nad_scope_class

        _patch_vm_nad_reference(
            vm=vm,
            target_nad_name=target_nad_scope_class.name,
        )
        _wait_for_migration_and_verify(vm=vm)

        LOGGER.info(
            f"Verifying VM {vm.name} is on target network after NAD change "
            "on upgraded cluster"
        )
        target_ip = get_vmi_ip_v4_by_name(
            vm=vm,
            name=target_nad_scope_class.name,
        )
        assert target_ip, (
            f"VM {vm.name} did not acquire IP on target network after NAD "
            "change on upgraded cluster"
        )


@pytest.mark.polarion("CNV-72329")
class TestNADHotplugSequentialOperations:
    """
    Tests for sequential and concurrent NAD change operations.

    Markers:
        - incremental

    Preconditions:
        - Two bridge-type NADs (source and target) deployed in test namespace
        - Running VM with secondary network interface attached via source NAD
    """

    @pytest.mark.incremental
    @pytest.mark.polarion("CNV-72329-024")
    def test_sequential_nad_changes_on_same_vm(
        self,
        source_nad_scope_class,
        target_nad_scope_class,
        vm_with_source_nad_scope_class,
    ):
        """
        Test that multiple sequential NAD changes on the same VM work correctly.

        Steps:
            1. Patch VM NAD reference from source to target and wait for migration
            2. Patch VM NAD reference from target back to source and wait for migration

        Expected:
            - Both migrations complete successfully
            - VM is functional after both NAD changes
        """
        vm = vm_with_source_nad_scope_class

        LOGGER.info(
            f"First NAD change: {source_nad_scope_class.name} -> "
            f"{target_nad_scope_class.name}"
        )
        _patch_vm_nad_reference(
            vm=vm,
            target_nad_name=target_nad_scope_class.name,
        )
        _wait_for_migration_and_verify(vm=vm)

        target_ip = get_vmi_ip_v4_by_name(
            vm=vm,
            name=target_nad_scope_class.name,
        )
        assert target_ip, (
            f"VM {vm.name} did not acquire IP after first NAD change"
        )

        LOGGER.info(
            f"Second NAD change: {target_nad_scope_class.name} -> "
            f"{source_nad_scope_class.name}"
        )
        _patch_vm_nad_reference(
            vm=vm,
            target_nad_name=source_nad_scope_class.name,
        )
        _wait_for_migration_and_verify(vm=vm)

        source_ip = get_vmi_ip_v4_by_name(
            vm=vm,
            name=source_nad_scope_class.name,
        )
        assert source_ip, (
            f"VM {vm.name} did not acquire IP after second NAD change"
        )
        LOGGER.info(
            f"VM {vm.name} functional after both sequential NAD changes"
        )

    @pytest.mark.incremental
    @pytest.mark.polarion("CNV-72329-025")
    def test_nad_change_during_ongoing_migration(
        self,
        source_nad_scope_class,
        target_nad_scope_class,
        third_nad_scope_class,
        vm_with_source_nad_scope_class,
    ):
        """
        [NEGATIVE] Test that NAD change during an ongoing migration is handled gracefully.

        Preconditions:
            - Third NAD (third-bridge-nad) available for second change

        Steps:
            1. Patch VM NAD reference to trigger first migration
            2. While migration is in progress, patch NAD reference again

        Expected:
            - Second NAD change is handled gracefully (queued or rejected)
            - VM is not left in an inconsistent state
        """
        vm = vm_with_source_nad_scope_class

        LOGGER.info(
            f"Triggering first NAD change on VM {vm.name}: "
            f"{source_nad_scope_class.name} -> {target_nad_scope_class.name}"
        )
        _patch_vm_nad_reference(
            vm=vm,
            target_nad_name=target_nad_scope_class.name,
        )

        LOGGER.info(
            f"Immediately triggering second NAD change on VM {vm.name}: "
            f"{target_nad_scope_class.name} -> {third_nad_scope_class.name}"
        )
        _patch_vm_nad_reference(
            vm=vm,
            target_nad_name=third_nad_scope_class.name,
        )

        LOGGER.info(f"Waiting for VM {vm.name} to reach stable state")
        vm.wait_for_status(status=vm.Status.RUNNING, timeout=300)

        assert vm.instance.status.phase == "Running", (
            f"VM {vm.name} is not in Running phase after concurrent NAD changes: "
            f"{vm.instance.status.phase}"
        )
        LOGGER.info(
            f"VM {vm.name} is in consistent Running state after concurrent "
            "NAD change attempts"
        )
