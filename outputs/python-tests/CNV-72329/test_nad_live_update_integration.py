"""
NAD Reference Live Update — Integration and Concurrent Operation E2E Tests

Validates NAD swap behavior during concurrent migration operations and
integration with interface hotplug/unplug. Covers STD scenarios
TS-CNV-72329-011 and TS-CNV-72329-012.

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
from utilities.virt import migrate_vm_and_verify

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")

CONCURRENT_OP_TIMEOUT = 180


@pytest.mark.polarion("CNV-72329")
class TestNadLiveUpdateDuringMigration:
    """
    Tests for NAD update behavior during concurrent migration operations.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Source and target bridge-based NADs created
        - VM created with secondary interface on source NAD
        - VM started and in Running state
    """

    def test_nad_update_during_active_migration(
        self,
        admin_client,
        nad_swap_vm_scope_class,
        source_bridge_nad_scope_class,
        target_bridge_nad_scope_class,
    ):
        """
        [NEGATIVE] Test that NAD update during active migration is handled gracefully.

        TS-CNV-72329-011

        Preconditions:
            - Running VM with secondary bridge interface on source NAD

        Steps:
            1. Trigger manual live migration via VirtualMachineInstanceMigration resource
            2. While migration is in progress, patch VM NAD reference to target NAD
            3. Wait for migration to complete
            4. Verify VM is in a consistent Running state

        Expected:
            - System handles concurrent NAD update and migration without crashing
            - VM is in a consistent Running state after migration completes
        """
        vm = nad_swap_vm_scope_class
        vmi = vm.vmi

        LOGGER.info(f"Triggering manual live migration for VM {vm.name}")
        manual_migration = VirtualMachineInstanceMigration(
            name=f"{vm.name}-manual-migration",
            namespace=vm.namespace,
            vmi_name=vmi.name,
        )
        manual_migration.create()

        LOGGER.info("Waiting briefly for migration to start before patching NAD")
        migration_started = False
        for sample in TimeoutSampler(
            wait_timeout=30,
            sleep=2,
            func=lambda: manual_migration.instance.status,
        ):
            if sample and sample.phase in ("Scheduling", "Scheduled", "PreparingTarget", "Running"):
                migration_started = True
                break

        assert migration_started, (
            f"Manual migration did not start within 30 seconds, "
            f"status: {manual_migration.instance.status}"
        )

        LOGGER.info(
            f"Migration in progress — patching NAD reference: "
            f"{source_bridge_nad_scope_class.name} -> {target_bridge_nad_scope_class.name}"
        )
        patch_nad_reference(
            networks=vm.instance.spec.template.spec.networks,
            old_nad_name=source_bridge_nad_scope_class.name,
            new_nad_name=target_bridge_nad_scope_class.name,
        )
        vm.update(resource_dict=vm.instance.to_dict())

        LOGGER.info("Waiting for all migrations to reach terminal state")
        for sample in TimeoutSampler(
            wait_timeout=CONCURRENT_OP_TIMEOUT,
            sleep=MIGRATION_POLL_INTERVAL,
            func=VirtualMachineInstanceMigration.get,
            client=admin_client,
            namespace=vm.namespace,
        ):
            migrations = list(sample)
            if not migrations:
                continue
            all_terminal = all(
                migration.instance.status.phase in ("Succeeded", "Failed")
                for migration in migrations
                if migration.instance.status
            )
            if all_terminal:
                break

        updated_vmi = vm.vmi
        assert updated_vmi.instance.status.phase == "Running", (
            f"VM should be in Running state after concurrent operations, "
            f"but is in {updated_vmi.instance.status.phase}"
        )
        LOGGER.info(
            f"VM {vm.name} is in consistent Running state after concurrent "
            "migration and NAD update"
        )


@pytest.mark.polarion("CNV-72329")
class TestNadLiveUpdateHotplugIntegration:
    """
    Tests for NAD live update integration with interface hotplug/unplug operations.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Source, target, and hotplug bridge-based NADs created
        - VM created and started with only the default pod network (no secondary interfaces)
    """

    def test_nad_swap_on_hotplugged_interface(
        self,
        admin_client,
        vm_for_hotplug_scope_class,
        source_bridge_nad_scope_class,
        target_bridge_nad_scope_class,
    ):
        """
        Test that NAD swap works on a hotplugged secondary interface.

        TS-CNV-72329-012

        Preconditions:
            - Running VM with only pod network

        Steps:
            1. Hotplug a secondary interface attached to source NAD
            2. Patch VM spec to change hotplugged interface NAD reference from source to target
            3. Wait for migration to complete

        Expected:
            - Hotplugged interface NAD reference is updated to target NAD
        """
        vm = vm_for_hotplug_scope_class

        LOGGER.info(
            f"Hotplugging secondary interface on VM {vm.name} "
            f"with NAD {source_bridge_nad_scope_class.name}"
        )
        networks = vm.instance.spec.template.spec.networks
        interfaces = vm.instance.spec.template.spec.domain.devices.interfaces

        networks.append({
            "name": source_bridge_nad_scope_class.name,
            "multus": {"networkName": source_bridge_nad_scope_class.name},
        })
        interfaces.append({
            "name": source_bridge_nad_scope_class.name,
            "bridge": {},
        })
        vm.update(resource_dict=vm.instance.to_dict())

        LOGGER.info("Waiting for hotplug to take effect")
        for sample in TimeoutSampler(
            wait_timeout=NAD_SWAP_TIMEOUT,
            sleep=MIGRATION_POLL_INTERVAL,
            func=lambda: vm.vmi.instance.status.interfaces,
        ):
            secondary_ifaces = [
                iface for iface in sample
                if iface.get("name") != "default"
            ]
            if secondary_ifaces:
                break

        LOGGER.info(
            f"Swapping hotplugged interface NAD: "
            f"{source_bridge_nad_scope_class.name} -> {target_bridge_nad_scope_class.name}"
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
        assert migration is not None, (
            "Migration was not triggered after NAD swap on hotplugged interface"
        )
        assert migration.instance.status.phase == "Succeeded", (
            f"Migration {migration.name} did not succeed: {migration.instance.status.phase}"
        )

        updated_nad_names = [
            net.get("multus", {}).get("networkName", "")
            for net in vm.vmi.instance.spec.networks
            if net.get("multus")
        ]
        LOGGER.info(f"Post-swap VMI NAD references: {updated_nad_names}")
        assert any(
            name.endswith(target_bridge_nad_scope_class.name) for name in updated_nad_names
        ), (
            f"Hotplugged interface should reference {target_bridge_nad_scope_class.name}, "
            f"got {updated_nad_names}"
        )

    def test_interface_hotplug_after_nad_swap(
        self,
        vm_for_hotplug_scope_class,
        hotplug_bridge_nad_scope_class,
    ):
        """
        Test that interface hotplug works correctly after a NAD swap has been performed.

        TS-CNV-72329-012

        Preconditions:
            - NAD swap on existing secondary interface already completed (from previous test)

        Steps:
            1. Hotplug a new secondary interface attached to hotplug NAD
            2. Verify hotplug takes effect on VMI

        Expected:
            - New interface is hotplugged successfully
            - VM remains in Running state with both secondary interfaces
        """
        vm = vm_for_hotplug_scope_class

        LOGGER.info(
            f"Hotplugging additional interface on VM {vm.name} "
            f"with NAD {hotplug_bridge_nad_scope_class.name}"
        )
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
        vm.update(resource_dict=vm.instance.to_dict())

        LOGGER.info("Waiting for second hotplug to take effect")
        for sample in TimeoutSampler(
            wait_timeout=NAD_SWAP_TIMEOUT,
            sleep=MIGRATION_POLL_INTERVAL,
            func=lambda: vm.vmi.instance.status.interfaces,
        ):
            secondary_ifaces = [
                iface for iface in sample
                if iface.get("name") != "default"
            ]
            if len(secondary_ifaces) >= 2:
                break

        vmi = vm.vmi
        assert vmi.instance.status.phase == "Running", (
            f"VM should remain Running after hotplug post-NAD-swap, "
            f"but is in {vmi.instance.status.phase}"
        )

        secondary_ifaces = [
            iface for iface in vmi.instance.status.interfaces
            if iface.get("name") != "default"
        ]
        assert len(secondary_ifaces) >= 2, (
            f"VM should have at least 2 secondary interfaces after both operations, "
            f"found {len(secondary_ifaces)}"
        )
        LOGGER.info(
            f"VM {vm.name} has {len(secondary_ifaces)} secondary interfaces "
            "after NAD swap + hotplug — both operations succeeded"
        )
