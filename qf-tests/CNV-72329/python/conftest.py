"""
Shared fixtures for Live Update NAD Reference tier 2 tests.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging
from contextlib import contextmanager

import pytest
from ocp_resources.datavolume import DataVolume
from ocp_resources.network_attachment_definition import NetworkAttachmentDefinition
from ocp_resources.virtual_machine_instance_migration import VirtualMachineInstanceMigration
from timeout_sampler import TimeoutSampler

from utilities.constants import TIMEOUT_5MIN, TIMEOUT_10MIN
from utilities.network import network_nad
from utilities.virt import (
    VirtualMachineForTests,
    fedora_vm_body,
    running_vm,
    wait_for_vm_interfaces,
)

LOGGER = logging.getLogger(__name__)

BRIDGE_NAD_TYPE = "linux-bridge"
NAD_VLAN_100 = 100
NAD_VLAN_200 = 200
NAD_VLAN_300 = 300
SECONDARY_IFACE_NAME = "secondary"
SECONDARY_IFACE_NAME_2 = "secondary2"


def _create_bridge_nad(client, namespace, name, vlan):
    """Create a bridge-type NetworkAttachmentDefinition with a specific VLAN.

    Args:
        client: Kubernetes client.
        namespace: Target namespace name.
        name: NAD resource name.
        vlan: VLAN ID for the bridge configuration.

    Returns:
        NetworkAttachmentDefinition: The created NAD resource.
    """
    return network_nad(
        nad_type=BRIDGE_NAD_TYPE,
        nad_name=name,
        namespace=namespace,
        interface_name=name,
        client=client,
        vlan=vlan,
    )


def _create_vm_with_secondary_network(client, namespace, name, nad_name):
    """Create a VM with a secondary bridge-binding network interface.

    Args:
        client: Kubernetes client.
        namespace: Target namespace name.
        name: VM resource name.
        nad_name: Name of the NAD to attach as secondary network.

    Returns:
        VirtualMachineForTests: The created and running VM.
    """
    vm = VirtualMachineForTests(
        client=client,
        name=name,
        namespace=namespace,
        body=fedora_vm_body(name=name),
        networks={SECONDARY_IFACE_NAME: nad_name},
        interfaces=[SECONDARY_IFACE_NAME],
    )
    vm.create(wait=True)
    running_vm(vm=vm)
    wait_for_vm_interfaces(vmi=vm.vmi)
    return vm


def _create_vm_with_multiple_secondary_networks(client, namespace, name, nad_names):
    """Create a VM with multiple secondary bridge-binding network interfaces.

    Args:
        client: Kubernetes client.
        namespace: Target namespace name.
        name: VM resource name.
        nad_names: Dict mapping interface names to NAD names.

    Returns:
        VirtualMachineForTests: The created and running VM.
    """
    vm = VirtualMachineForTests(
        client=client,
        name=name,
        namespace=namespace,
        body=fedora_vm_body(name=name),
        networks=nad_names,
        interfaces=list(nad_names.keys()),
    )
    vm.create(wait=True)
    running_vm(vm=vm)
    wait_for_vm_interfaces(vmi=vm.vmi)
    return vm


def _patch_vm_nad_reference(vm, interface_name, new_nad_name):
    """Patch a VM's secondary network NAD reference to a new NAD.

    Args:
        vm: VirtualMachineForTests instance.
        interface_name: Name of the interface to update.
        new_nad_name: Name of the target NAD.
    """
    LOGGER.info(f"Patching VM {vm.name} interface '{interface_name}' to NAD '{new_nad_name}'")
    networks = vm.instance.spec.template.spec.networks
    for network in networks:
        if network.get("name") == interface_name and network.get("multus"):
            network["multus"]["networkName"] = new_nad_name
            break
    vm.update(spec={"template": {"spec": {"networks": networks}}})


def _wait_for_migration_success(client, namespace, vm_name, timeout=TIMEOUT_10MIN):
    """Wait for a successful migration of the specified VM.

    Args:
        client: Kubernetes client.
        namespace: Namespace of the VM.
        vm_name: Name of the VM to monitor.
        timeout: Maximum wait time in seconds.
    """
    LOGGER.info(f"Waiting for migration of VM {vm_name} to succeed")
    for sample in TimeoutSampler(
        wait_timeout=timeout,
        sleep=5,
        func=VirtualMachineInstanceMigration.get,
        client=client,
        namespace=namespace,
    ):
        for migration in sample:
            if (
                migration.instance.spec.get("vmiName") == vm_name
                and migration.instance.status
                and migration.instance.status.get("phase") == "Succeeded"
            ):
                LOGGER.info(f"Migration for VM {vm_name} succeeded")
                return


@pytest.fixture(scope="class")
def nad_vlan_100(admin_client, namespace):
    """Create a bridge NAD with VLAN 100."""
    with _create_bridge_nad(
        client=admin_client,
        namespace=namespace.name,
        name="nad-vlan100",
        vlan=NAD_VLAN_100,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def nad_vlan_200(admin_client, namespace):
    """Create a bridge NAD with VLAN 200."""
    with _create_bridge_nad(
        client=admin_client,
        namespace=namespace.name,
        name="nad-vlan200",
        vlan=NAD_VLAN_200,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def nad_vlan_300(admin_client, namespace):
    """Create a bridge NAD with VLAN 300."""
    with _create_bridge_nad(
        client=admin_client,
        namespace=namespace.name,
        name="nad-vlan300",
        vlan=NAD_VLAN_300,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def under_test_vm(unprivileged_client, namespace, nad_vlan_100):
    """Create a running VM with secondary interface on NAD VLAN 100."""
    with VirtualMachineForTests(
        client=unprivileged_client,
        name="vm-under-test",
        namespace=namespace.name,
        body=fedora_vm_body(name="vm-under-test"),
        networks={SECONDARY_IFACE_NAME: nad_vlan_100.name},
        interfaces=[SECONDARY_IFACE_NAME],
    ) as vm:
        running_vm(vm=vm)
        wait_for_vm_interfaces(vmi=vm.vmi)
        yield vm


@pytest.fixture(scope="class")
def peer_vm(unprivileged_client, namespace, nad_vlan_200):
    """Create a running peer VM with secondary interface on NAD VLAN 200."""
    with VirtualMachineForTests(
        client=unprivileged_client,
        name="vm-peer",
        namespace=namespace.name,
        body=fedora_vm_body(name="vm-peer"),
        networks={SECONDARY_IFACE_NAME: nad_vlan_200.name},
        interfaces=[SECONDARY_IFACE_NAME],
    ) as vm:
        running_vm(vm=vm)
        wait_for_vm_interfaces(vmi=vm.vmi)
        yield vm


@pytest.fixture(scope="class")
def vm_with_multi_secondary(unprivileged_client, namespace, nad_vlan_100, nad_vlan_300):
    """Create a running VM with two secondary interfaces on different NADs."""
    with VirtualMachineForTests(
        client=unprivileged_client,
        name="vm-multi-iface",
        namespace=namespace.name,
        body=fedora_vm_body(name="vm-multi-iface"),
        networks={
            SECONDARY_IFACE_NAME: nad_vlan_100.name,
            SECONDARY_IFACE_NAME_2: nad_vlan_300.name,
        },
        interfaces=[SECONDARY_IFACE_NAME, SECONDARY_IFACE_NAME_2],
    ) as vm:
        running_vm(vm=vm)
        wait_for_vm_interfaces(vmi=vm.vmi)
        yield vm


@pytest.fixture(scope="class")
def vm_default_network_only(unprivileged_client, namespace):
    """Create a running VM with only the default pod network (no secondary interfaces)."""
    with VirtualMachineForTests(
        client=unprivileged_client,
        name="vm-default-only",
        namespace=namespace.name,
        body=fedora_vm_body(name="vm-default-only"),
    ) as vm:
        running_vm(vm=vm)
        wait_for_vm_interfaces(vmi=vm.vmi)
        yield vm


@pytest.fixture(scope="class")
def vm_for_bridge_binding(unprivileged_client, namespace, nad_vlan_100):
    """Create a running VM with a bridge-binding secondary interface."""
    with VirtualMachineForTests(
        client=unprivileged_client,
        name="vm-bridge-binding",
        namespace=namespace.name,
        body=fedora_vm_body(name="vm-bridge-binding"),
        networks={SECONDARY_IFACE_NAME: nad_vlan_100.name},
        interfaces=[SECONDARY_IFACE_NAME],
    ) as vm:
        running_vm(vm=vm)
        wait_for_vm_interfaces(vmi=vm.vmi)
        yield vm
