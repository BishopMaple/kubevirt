"""
NAD Reference Live Update - Hotplug Integration Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
PR: https://github.com/kubevirt/kubevirt/pull/16412
"""

import logging
from typing import Generator

import pytest

from ocp_resources.network_attachment_definition import NetworkAttachmentDefinition
from timeout_sampler import TimeoutSampler
from utilities.network import network_nad
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")

HOTPLUG_SOURCE_BRIDGE = "br-hp-src"
HOTPLUG_TARGET_BRIDGE = "br-hp-tgt"
HOTPLUG_NEW_BRIDGE = "br-hp-new"
HOTPLUG_SECOND_SOURCE_BRIDGE = "br-hp-src2"

MIGRATION_TIMEOUT = 600
MIGRATION_POLL_INTERVAL = 5


@pytest.fixture(scope="class")
def hotplug_source_nad_scope_class(
    namespace,
) -> Generator[NetworkAttachmentDefinition, None, None]:
    """
    Source NAD for hotplug integration tests.

    Yields:
        NetworkAttachmentDefinition: Source NAD resource.
    """
    with network_nad(
        nad_type="bridge",
        network_name=HOTPLUG_SOURCE_BRIDGE,
        nad_name="hp-source-nad",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def hotplug_target_nad_scope_class(
    namespace,
) -> Generator[NetworkAttachmentDefinition, None, None]:
    """
    Target NAD for NAD swap in hotplug tests.

    Yields:
        NetworkAttachmentDefinition: Target NAD resource.
    """
    with network_nad(
        nad_type="bridge",
        network_name=HOTPLUG_TARGET_BRIDGE,
        nad_name="hp-target-nad",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def hotplug_new_nad_scope_class(
    namespace,
) -> Generator[NetworkAttachmentDefinition, None, None]:
    """
    NAD for newly hotplugged interface.

    Yields:
        NetworkAttachmentDefinition: NAD resource for hotplug addition.
    """
    with network_nad(
        nad_type="bridge",
        network_name=HOTPLUG_NEW_BRIDGE,
        nad_name="hp-new-nad",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def hotplug_second_source_nad_scope_class(
    namespace,
) -> Generator[NetworkAttachmentDefinition, None, None]:
    """
    Second source NAD for hotunplug tests (interface to be removed).

    Yields:
        NetworkAttachmentDefinition: Second source NAD resource.
    """
    with network_nad(
        nad_type="bridge",
        network_name=HOTPLUG_SECOND_SOURCE_BRIDGE,
        nad_name="hp-source2-nad",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def vm_for_hotplug_scope_class(
    unprivileged_client,
    namespace,
    hotplug_source_nad_scope_class,
) -> Generator[VirtualMachineForTests, None, None]:
    """
    VM with one secondary interface for hotplug integration tests.

    Yields:
        VirtualMachineForTests: Running VM with single secondary bridge interface.
    """
    name = "hp-integration-vm"
    nad = hotplug_source_nad_scope_class
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=name,
        namespace=namespace.name,
        body=fedora_vm_body(name=name),
        networks={nad.name: nad.name},
        interfaces=[nad.name],
    ) as vm:
        running_vm(vm=vm)
        yield vm


@pytest.fixture(scope="class")
def vm_for_hotunplug_scope_class(
    unprivileged_client,
    namespace,
    hotplug_source_nad_scope_class,
    hotplug_second_source_nad_scope_class,
) -> Generator[VirtualMachineForTests, None, None]:
    """
    VM with two secondary interfaces for hotunplug integration tests.

    Yields:
        VirtualMachineForTests: Running VM with 2 secondary bridge interfaces.
    """
    name = "hp-unplug-vm"
    nad_1 = hotplug_source_nad_scope_class
    nad_2 = hotplug_second_source_nad_scope_class
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=name,
        namespace=namespace.name,
        body=fedora_vm_body(name=name),
        networks={
            nad_1.name: nad_1.name,
            nad_2.name: nad_2.name,
        },
        interfaces=[nad_1.name, nad_2.name],
    ) as vm:
        running_vm(vm=vm)
        yield vm


def _migration_cleared(vmi):
    """Check if MigrationRequired condition is missing or false on the VMI."""
    vmi_instance = vmi.instance
    if not hasattr(vmi_instance, "status") or not vmi_instance.status:
        return True
    conditions = vmi_instance.status.get("conditions", [])
    for condition in conditions:
        if condition.get("type") == "MigrationRequired":
            return condition.get("status") != "True"
    return True


@pytest.mark.polarion("CNV-72329")
class TestNadSwapWithHotplugOperations:
    """
    Tests for NAD swap coexistence with hotplug/hotunplug operations.

    Markers:
        - polarion

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - VM rollout strategy set to LiveUpdate with LiveMigrate workload update
        - Source NAD, target NAD, and hotplug NAD created with distinct bridges
        - VM running with secondary interface on source NAD
    """

    def test_nad_swap_with_concurrent_interface_hotplug(
        self,
        vm_for_hotplug_scope_class,
        hotplug_source_nad_scope_class,
        hotplug_target_nad_scope_class,
        hotplug_new_nad_scope_class,
    ):
        """
        Test that NAD swap works alongside interface hotplug operation.

        Scenario: TS-CNV-72329-019

        Steps:
            1. Patch VM to add new interface (hotplug) and change existing NAD reference simultaneously
            2. Wait for migration and hotplug to complete
            3. Verify VMI has new interface and swapped NAD

        Expected:
            - VM has the new hotplugged interface AND the swapped NAD reference
            - Both operations succeed without conflict
        """
        vm = vm_for_hotplug_scope_class
        vmi = vm.vmi
        source_nad = hotplug_source_nad_scope_class
        target_nad = hotplug_target_nad_scope_class
        new_nad = hotplug_new_nad_scope_class

        LOGGER.info(
            f"Patching VM {vm.name}: swap NAD {source_nad.name} -> {target_nad.name} "
            f"and hotplug new interface on {new_nad.name}"
        )

        patch_data = [
            {
                "op": "replace",
                "path": "/spec/template/spec/networks/0/multus/networkName",
                "value": target_nad.name,
            },
            {
                "op": "add",
                "path": "/spec/template/spec/domain/devices/interfaces/-",
                "value": {
                    "name": new_nad.name,
                    "bridge": {},
                },
            },
            {
                "op": "add",
                "path": "/spec/template/spec/networks/-",
                "value": {
                    "name": new_nad.name,
                    "multus": {"networkName": new_nad.name},
                },
            },
        ]
        vm.instance.patch(
            body=patch_data,
            content_type="application/json-patch+json",
        )

        LOGGER.info("Waiting for migration and hotplug operations to complete")
        for sample in TimeoutSampler(
            wait_timeout=MIGRATION_TIMEOUT,
            sleep=MIGRATION_POLL_INTERVAL,
            func=_migration_cleared,
            vmi=vmi,
        ):
            if sample:
                break

        LOGGER.info("Verifying VMI spec after concurrent hotplug + NAD swap")
        vmi_instance = vmi.instance
        networks = vmi_instance.spec.get("networks", [])
        network_names = [
            net["multus"]["networkName"]
            for net in networks
            if "multus" in net
        ]

        assert target_nad.name in network_names, (
            f"Swapped NAD {target_nad.name} not found in VMI networks: {network_names}"
        )
        assert new_nad.name in network_names, (
            f"Hotplugged NAD {new_nad.name} not found in VMI networks: {network_names}"
        )
        LOGGER.info("Concurrent hotplug + NAD swap verified successfully")

    def test_nad_swap_with_concurrent_interface_hotunplug(
        self,
        vm_for_hotunplug_scope_class,
        hotplug_source_nad_scope_class,
        hotplug_second_source_nad_scope_class,
        hotplug_target_nad_scope_class,
    ):
        """
        Test that NAD swap works alongside interface hotunplug operation.

        Scenario: TS-CNV-72329-020

        Preconditions:
            - VM running with 2 secondary interfaces on separate source NADs

        Steps:
            1. Patch VM to remove one interface and change NAD reference on the other simultaneously
            2. Wait for operations to complete
            3. Verify VMI spec

        Expected:
            - Removed interface is gone from VMI spec
            - Remaining interface has the swapped NAD reference
        """
        vm = vm_for_hotunplug_scope_class
        vmi = vm.vmi
        source_nad_1 = hotplug_source_nad_scope_class
        source_nad_2 = hotplug_second_source_nad_scope_class
        target_nad = hotplug_target_nad_scope_class

        LOGGER.info(
            f"Patching VM {vm.name}: swap NAD on interface 0 ({source_nad_1.name} -> {target_nad.name}) "
            f"and remove interface 1 ({source_nad_2.name})"
        )

        patch_data = [
            {
                "op": "replace",
                "path": "/spec/template/spec/networks/0/multus/networkName",
                "value": target_nad.name,
            },
            {
                "op": "remove",
                "path": "/spec/template/spec/networks/1",
            },
            {
                "op": "remove",
                "path": "/spec/template/spec/domain/devices/interfaces/1",
            },
        ]
        vm.instance.patch(
            body=patch_data,
            content_type="application/json-patch+json",
        )

        LOGGER.info("Waiting for hotunplug + NAD swap operations to complete")
        for sample in TimeoutSampler(
            wait_timeout=MIGRATION_TIMEOUT,
            sleep=MIGRATION_POLL_INTERVAL,
            func=_migration_cleared,
            vmi=vmi,
        ):
            if sample:
                break

        LOGGER.info("Verifying VMI spec after concurrent hotunplug + NAD swap")
        vmi_instance = vmi.instance
        networks = vmi_instance.spec.get("networks", [])
        multus_networks = [net for net in networks if "multus" in net]

        assert len(multus_networks) == 1, (
            f"Expected 1 multus network after hotunplug, got {len(multus_networks)}: {multus_networks}"
        )
        remaining_nad_name = multus_networks[0]["multus"]["networkName"]
        assert remaining_nad_name == target_nad.name, (
            f"Remaining NAD should be {target_nad.name}, got {remaining_nad_name}"
        )
        assert source_nad_2.name not in [net.get("multus", {}).get("networkName") for net in networks], (
            f"Removed NAD {source_nad_2.name} still present in VMI networks"
        )
        LOGGER.info("Concurrent hotunplug + NAD swap verified successfully")
