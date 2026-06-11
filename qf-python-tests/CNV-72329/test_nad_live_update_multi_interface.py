"""
NAD Reference Live Update - Multi-Interface Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
PR: https://github.com/kubevirt/kubevirt/pull/16412
"""

import logging
from typing import Generator

import pytest

from ocp_resources.network_attachment_definition import NetworkAttachmentDefinition
from utilities.network import network_nad
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")

NAD_BRIDGES = {
    "source_1": "br-multi-s1",
    "source_2": "br-multi-s2",
    "source_3": "br-multi-s3",
    "target_1": "br-multi-t1",
    "target_2": "br-multi-t2",
}

MIGRATION_TIMEOUT = 600
MIGRATION_POLL_INTERVAL = 5


@pytest.fixture(scope="class")
def multi_source_nad_1_scope_class(
    namespace,
) -> Generator[NetworkAttachmentDefinition, None, None]:
    """
    First source NAD for multi-interface tests.

    Yields:
        NetworkAttachmentDefinition: Source NAD 1 resource.
    """
    with network_nad(
        nad_type="bridge",
        network_name=NAD_BRIDGES["source_1"],
        nad_name="multi-nad-s1",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def multi_source_nad_2_scope_class(
    namespace,
) -> Generator[NetworkAttachmentDefinition, None, None]:
    """
    Second source NAD for multi-interface tests.

    Yields:
        NetworkAttachmentDefinition: Source NAD 2 resource.
    """
    with network_nad(
        nad_type="bridge",
        network_name=NAD_BRIDGES["source_2"],
        nad_name="multi-nad-s2",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def multi_source_nad_3_scope_class(
    namespace,
) -> Generator[NetworkAttachmentDefinition, None, None]:
    """
    Third source NAD for multi-interface tests (unchanged during test).

    Yields:
        NetworkAttachmentDefinition: Source NAD 3 resource.
    """
    with network_nad(
        nad_type="bridge",
        network_name=NAD_BRIDGES["source_3"],
        nad_name="multi-nad-s3",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def multi_target_nad_1_scope_class(
    namespace,
) -> Generator[NetworkAttachmentDefinition, None, None]:
    """
    First target NAD for multi-interface swap.

    Yields:
        NetworkAttachmentDefinition: Target NAD 1 resource.
    """
    with network_nad(
        nad_type="bridge",
        network_name=NAD_BRIDGES["target_1"],
        nad_name="multi-nad-t1",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def multi_target_nad_2_scope_class(
    namespace,
) -> Generator[NetworkAttachmentDefinition, None, None]:
    """
    Second target NAD for multi-interface swap.

    Yields:
        NetworkAttachmentDefinition: Target NAD 2 resource.
    """
    with network_nad(
        nad_type="bridge",
        network_name=NAD_BRIDGES["target_2"],
        nad_name="multi-nad-t2",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def vm_with_three_interfaces_scope_class(
    unprivileged_client,
    namespace,
    multi_source_nad_1_scope_class,
    multi_source_nad_2_scope_class,
    multi_source_nad_3_scope_class,
) -> Generator[VirtualMachineForTests, None, None]:
    """
    VM with 3 secondary interfaces on separate source NADs.

    Yields:
        VirtualMachineForTests: Running VM with 3 secondary bridge interfaces.
    """
    name = "multi-nad-vm"
    nad_1 = multi_source_nad_1_scope_class
    nad_2 = multi_source_nad_2_scope_class
    nad_3 = multi_source_nad_3_scope_class
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=name,
        namespace=namespace.name,
        body=fedora_vm_body(name=name),
        networks={
            nad_1.name: nad_1.name,
            nad_2.name: nad_2.name,
            nad_3.name: nad_3.name,
        },
        interfaces=[nad_1.name, nad_2.name, nad_3.name],
    ) as vm:
        running_vm(vm=vm)
        yield vm


@pytest.mark.polarion("CNV-72329")
class TestNadLiveUpdateMultiInterface:
    """
    Tests for concurrent NAD reference updates on multiple interfaces.

    Markers:
        - polarion

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - VM rollout strategy set to LiveUpdate with LiveMigrate workload update
        - Five bridge-based NADs created (3 source + 2 target with distinct bridges)
        - VM running with 3 secondary interfaces on separate source NADs
    """

    def test_concurrent_nad_reference_updates_on_multiple_interfaces(
        self,
        vm_with_three_interfaces_scope_class,
        multi_source_nad_3_scope_class,
        multi_target_nad_1_scope_class,
        multi_target_nad_2_scope_class,
    ):
        """
        Test that multiple NAD references can be updated simultaneously.

        Scenario: TS-CNV-72329-012

        Steps:
            1. Patch VM spec to change 2 of 3 NAD references simultaneously
            2. Wait for migration to complete
            3. Verify VMI spec shows correct NAD references

        Expected:
            - Single migration handles all NAD changes
            - VMI spec shows 2 updated NAD references
            - VMI spec shows 1 unchanged NAD reference (third interface)
        """
        vm = vm_with_three_interfaces_scope_class
        vmi = vm.vmi
        target_nad_1 = multi_target_nad_1_scope_class
        target_nad_2 = multi_target_nad_2_scope_class
        unchanged_nad = multi_source_nad_3_scope_class

        LOGGER.info(
            f"Patching VM {vm.name} to change 2 of 3 NAD references: "
            f"net0 -> {target_nad_1.name}, net1 -> {target_nad_2.name}"
        )
        patch_data = [
            {
                "op": "replace",
                "path": "/spec/template/spec/networks/0/multus/networkName",
                "value": target_nad_1.name,
            },
            {
                "op": "replace",
                "path": "/spec/template/spec/networks/1/multus/networkName",
                "value": target_nad_2.name,
            },
        ]
        vm.instance.patch(
            body=patch_data,
            content_type="application/json-patch+json",
        )

        LOGGER.info("Waiting for migration to complete after multi-NAD patch")
        from timeout_sampler import TimeoutSampler

        for sample in TimeoutSampler(
            wait_timeout=MIGRATION_TIMEOUT,
            sleep=MIGRATION_POLL_INTERVAL,
            func=_migration_cleared,
            vmi=vmi,
        ):
            if sample:
                break

        LOGGER.info("Verifying VMI spec has correct NAD references")
        vmi_instance = vmi.instance
        networks = vmi_instance.spec.get("networks", [])
        networks_by_name = {net["name"]: net for net in networks if "multus" in net}

        updated_nad_names = [
            net["multus"]["networkName"]
            for net in networks_by_name.values()
        ]
        assert target_nad_1.name in updated_nad_names, (
            f"Target NAD 1 {target_nad_1.name} not found in VMI networks: {updated_nad_names}"
        )
        assert target_nad_2.name in updated_nad_names, (
            f"Target NAD 2 {target_nad_2.name} not found in VMI networks: {updated_nad_names}"
        )
        assert unchanged_nad.name in updated_nad_names, (
            f"Unchanged NAD {unchanged_nad.name} not found in VMI networks: {updated_nad_names}"
        )
        LOGGER.info("Multi-interface NAD update verified: 2 updated, 1 unchanged")


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
