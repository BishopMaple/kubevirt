"""
Fixtures for NAD Reference Live Update Tier 2 Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging

import pytest
from ocp_resources.virtual_machine_instance_migration import VirtualMachineInstanceMigration
from timeout_sampler import TimeoutSampler

from utilities.constants import BRIDGE
from utilities.network import network_nad
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

LOGGER = logging.getLogger(__name__)

NAD_SWAP_TIMEOUT = 120
MIGRATION_POLL_INTERVAL = 5


@pytest.fixture(scope="class")
def source_bridge_nad_scope_class(namespace):
    """
    Source bridge NAD for NAD swap tests.

    Yields:
        NetworkAttachmentDefinition: Source bridge NAD resource
    """
    with network_nad(
        nad_type=BRIDGE,
        network_name="source-net",
        nad_name="source-bridge-nad",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def target_bridge_nad_scope_class(namespace):
    """
    Target bridge NAD for NAD swap tests.

    Yields:
        NetworkAttachmentDefinition: Target bridge NAD resource
    """
    with network_nad(
        nad_type=BRIDGE,
        network_name="target-net",
        nad_name="target-bridge-nad",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def source_bridge_nad_2_scope_class(namespace):
    """
    Second source bridge NAD for multi-interface tests.

    Yields:
        NetworkAttachmentDefinition: Second source bridge NAD resource
    """
    with network_nad(
        nad_type=BRIDGE,
        network_name="source-net-2",
        nad_name="source-bridge-nad-2",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def target_bridge_nad_2_scope_class(namespace):
    """
    Second target bridge NAD for multi-interface tests.

    Yields:
        NetworkAttachmentDefinition: Second target bridge NAD resource
    """
    with network_nad(
        nad_type=BRIDGE,
        network_name="target-net-2",
        nad_name="target-bridge-nad-2",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def hotplug_bridge_nad_scope_class(namespace):
    """
    Additional bridge NAD for NIC hotplug tests after NAD swap.

    Yields:
        NetworkAttachmentDefinition: Bridge NAD for hotplug operation
    """
    with network_nad(
        nad_type=BRIDGE,
        network_name="hotplug-net",
        nad_name="hotplug-bridge-nad",
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def nad_swap_vm_scope_class(
    unprivileged_client,
    namespace,
    source_bridge_nad_scope_class,
):
    """
    Running Fedora VM with secondary bridge interface on source NAD.

    Yields:
        VirtualMachineForTests: Running Fedora VM with secondary bridge interface
    """
    name = "nad-swap-vm"
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=name,
        namespace=namespace.name,
        body=fedora_vm_body(name=name),
        networks={source_bridge_nad_scope_class.name: source_bridge_nad_scope_class.name},
        interfaces=[source_bridge_nad_scope_class.name],
    ) as vm:
        running_vm(vm=vm)
        yield vm


@pytest.fixture(scope="class")
def peer_vm_on_target_nad_scope_class(
    unprivileged_client,
    namespace,
    target_bridge_nad_scope_class,
):
    """
    Peer Fedora VM on the target NAD for connectivity validation.

    Yields:
        VirtualMachineForTests: Running Fedora VM on target bridge NAD
    """
    name = "nad-swap-peer-vm"
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=name,
        namespace=namespace.name,
        body=fedora_vm_body(name=name),
        networks={target_bridge_nad_scope_class.name: target_bridge_nad_scope_class.name},
        interfaces=[target_bridge_nad_scope_class.name],
    ) as vm:
        running_vm(vm=vm)
        yield vm


@pytest.fixture(scope="class")
def multi_interface_vm_scope_class(
    unprivileged_client,
    namespace,
    source_bridge_nad_scope_class,
    source_bridge_nad_2_scope_class,
):
    """
    Running Fedora VM with two secondary bridge interfaces on source NADs.

    Yields:
        VirtualMachineForTests: Running Fedora VM with two secondary interfaces
    """
    name = "multi-iface-nad-swap-vm"
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=name,
        namespace=namespace.name,
        body=fedora_vm_body(name=name),
        networks={
            source_bridge_nad_scope_class.name: source_bridge_nad_scope_class.name,
            source_bridge_nad_2_scope_class.name: source_bridge_nad_2_scope_class.name,
        },
        interfaces=[
            source_bridge_nad_scope_class.name,
            source_bridge_nad_2_scope_class.name,
        ],
    ) as vm:
        running_vm(vm=vm)
        yield vm


@pytest.fixture(scope="class")
def vm_for_hotplug_scope_class(
    unprivileged_client,
    namespace,
):
    """
    Running Fedora VM with only pod network for hotplug integration tests.

    Yields:
        VirtualMachineForTests: Running Fedora VM with default pod network only
    """
    name = "hotplug-nad-swap-vm"
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=name,
        namespace=namespace.name,
        body=fedora_vm_body(name=name),
    ) as vm:
        running_vm(vm=vm)
        yield vm


def patch_nad_reference(networks, old_nad_name, new_nad_name):
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


def wait_for_nad_swap_migration(admin_client, namespace, timeout=NAD_SWAP_TIMEOUT):
    """Wait for a NAD swap migration to be created and succeed.

    Args:
        admin_client: Admin API client.
        namespace: Namespace to watch for migrations.
        timeout: Maximum wait time in seconds.

    Returns:
        VirtualMachineInstanceMigration: The completed migration resource.
    """
    LOGGER.info(f"Waiting for NAD swap migration in namespace {namespace}")
    migration = None
    for sample in TimeoutSampler(
        wait_timeout=timeout,
        sleep=MIGRATION_POLL_INTERVAL,
        func=VirtualMachineInstanceMigration.get,
        client=admin_client,
        namespace=namespace,
    ):
        migrations = list(sample)
        if migrations:
            migration = migrations[0]
            if migration.instance.status.phase == "Succeeded":
                LOGGER.info(f"Migration {migration.name} succeeded")
                return migration
    return migration
