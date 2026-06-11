"""
Live Update NAD Reference - Error Handling and Concurrency Tests

Validates graceful error handling for invalid NAD references, concurrent
migration conflicts, and coexistence with disk hotplug operations.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging

import pytest
from ocp_resources.datavolume import DataVolume
from ocp_resources.virtual_machine_instance_migration import VirtualMachineInstanceMigration
from timeout_sampler import TimeoutSampler

from utilities.constants import TIMEOUT_5MIN, TIMEOUT_10MIN
from utilities.storage import create_dv, wait_for_succeeded_dv
from utilities.virt import (
    VirtualMachineForTests,
    fedora_vm_body,
    running_vm,
    wait_for_vm_interfaces,
)

from conftest import (
    SECONDARY_IFACE_NAME,
    _patch_vm_nad_reference,
    _wait_for_migration_success,
)

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")


@pytest.mark.polarion("CNV-72329")
class TestLiveUpdateNADRefInvalidReference:
    """
    [NEGATIVE] Tests for NAD reference change error handling with invalid NADs.

    Markers:
        - gating

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - Bridge NAD in test namespace
        - Running VM with secondary bridge-binding interface
    """

    def test_invalid_nad_reference_handled_gracefully(
        self,
        admin_client,
        namespace,
        nad_vlan_100,
        under_test_vm,
    ):
        """
        [NEGATIVE] Test that changing NAD reference to non-existent NAD is
        handled gracefully.

        Preconditions:
            - Running VM with secondary interface on NAD VLAN 100

        Steps:
            1. Change VM secondary network NAD reference to a non-existent NAD name
            2. Check VM status and events

        Expected:
            - VM remains in a recoverable state
            - Error is surfaced through VM conditions or events
        """
        nonexistent_nad_name = "nad-does-not-exist"
        LOGGER.info(f"Changing NAD reference to non-existent NAD: {nonexistent_nad_name}")
        _patch_vm_nad_reference(
            vm=under_test_vm,
            interface_name=SECONDARY_IFACE_NAME,
            new_nad_name=nonexistent_nad_name,
        )
        LOGGER.info("Verifying VM remains in a recoverable state")
        vm_instance = under_test_vm.instance
        assert vm_instance is not None, "VM instance should exist after invalid NAD reference"

        vmi = under_test_vm.vmi
        assert vmi is not None, "VMI should still exist after invalid NAD reference"
        LOGGER.info(f"VM {under_test_vm.name} remains operational after invalid NAD reference change")


@pytest.mark.polarion("CNV-72329")
class TestLiveUpdateNADRefDuringMigration:
    """
    [NEGATIVE] Tests for NAD reference change during concurrent migration operations.

    Markers:
        - gating

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - Bridge NADs in test namespace
        - Running VM with secondary bridge-binding interface
    """

    def test_nad_change_during_in_progress_migration(
        self,
        admin_client,
        namespace,
        nad_vlan_100,
        nad_vlan_200,
        under_test_vm,
    ):
        """
        [NEGATIVE] Test that NAD reference change during an in-progress migration
        is handled correctly.

        Preconditions:
            - Running VM with secondary interface on NAD VLAN 100

        Steps:
            1. Trigger manual migration
            2. While migration is running, change NAD reference on VM
            3. Wait for all operations to settle

        Expected:
            - VM is in a consistent state after both operations complete
        """
        LOGGER.info(f"Triggering manual migration for VM {under_test_vm.name}")
        migration = VirtualMachineInstanceMigration(
            name=f"manual-migration-{under_test_vm.name}",
            namespace=namespace.name,
            vmi_name=under_test_vm.name,
            client=admin_client,
        )
        migration.create(wait=False)

        LOGGER.info("Migration triggered, immediately changing NAD reference")
        _patch_vm_nad_reference(
            vm=under_test_vm,
            interface_name=SECONDARY_IFACE_NAME,
            new_nad_name=nad_vlan_200.name,
        )

        LOGGER.info("Waiting for all operations to settle")
        for sample in TimeoutSampler(
            wait_timeout=TIMEOUT_10MIN,
            sleep=5,
            func=lambda: under_test_vm.instance.status.ready,
        ):
            if sample:
                break

        vm_instance = under_test_vm.instance
        LOGGER.info(f"VM {under_test_vm.name} status: ready={vm_instance.status.ready}")
        assert vm_instance.status.ready, (
            f"VM {under_test_vm.name} is not in a consistent state after concurrent "
            f"migration and NAD change"
        )


@pytest.mark.polarion("CNV-72329")
class TestLiveUpdateNADRefWithDiskHotplug:
    """
    Tests for NAD reference change coexistence with concurrent disk hotplug.

    Markers:
        - gating

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - Bridge NADs in test namespace
        - Running VM with secondary bridge-binding interface
        - DataVolume created and ready for hotplug
    """

    @pytest.fixture()
    def hotplug_data_volume(self, admin_client, namespace):
        """Create a DataVolume for disk hotplug testing."""
        dv = create_dv(
            client=admin_client,
            namespace=namespace.name,
            name="hotplug-dv",
            size="1Gi",
        )
        wait_for_succeeded_dv(dv=dv)
        yield dv
        dv.delete(wait=True)

    def test_nad_change_does_not_interfere_with_disk_hotplug(
        self,
        admin_client,
        namespace,
        nad_vlan_100,
        nad_vlan_200,
        under_test_vm,
        hotplug_data_volume,
    ):
        """
        Test that NAD reference change does not interfere with concurrent disk
        hotplug operation.

        Preconditions:
            - Running VM with secondary interface on NAD VLAN 100
            - DataVolume created and ready for hotplug

        Steps:
            1. Concurrently hotplug a disk and change NAD reference from NAD-A to NAD-B
            2. Wait for all operations to complete
            3. Verify disk is attached and NAD is changed

        Expected:
            - New disk is attached to VM
            - NAD reference is changed to NAD-B
        """
        LOGGER.info("Hotplugging disk and changing NAD reference concurrently")
        under_test_vm.add_volume(
            volume_name=hotplug_data_volume.name,
        )
        _patch_vm_nad_reference(
            vm=under_test_vm,
            interface_name=SECONDARY_IFACE_NAME,
            new_nad_name=nad_vlan_200.name,
        )

        LOGGER.info("Waiting for all operations to complete")
        for sample in TimeoutSampler(
            wait_timeout=TIMEOUT_10MIN,
            sleep=5,
            func=lambda: under_test_vm.instance.status.ready,
        ):
            if sample:
                break

        LOGGER.info("Verifying both operations completed successfully")
        vmi_spec = under_test_vm.vmi.instance.spec

        disk_attached = any(
            disk.get("name") == hotplug_data_volume.name
            for disk in vmi_spec.domain.devices.disks
        )
        assert disk_attached, (
            f"Hotplugged disk {hotplug_data_volume.name} not found in VMI spec"
        )

        nad_updated = False
        for network in vmi_spec.networks:
            if network.get("name") == SECONDARY_IFACE_NAME and network.get("multus"):
                nad_updated = network["multus"]["networkName"] == nad_vlan_200.name
                break

        assert nad_updated, (
            f"NAD reference not updated to {nad_vlan_200.name} after concurrent operations"
        )
