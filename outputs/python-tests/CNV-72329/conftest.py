"""
Shared fixtures for NAD reference hotplug tier 2 tests.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging

import pytest

from utilities.network import network_nad
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

LOGGER = logging.getLogger(__name__)

BRIDGE_NAD_TYPE = "bridge"
SOURCE_BRIDGE_NAME = "source-br0"
TARGET_BRIDGE_NAME = "target-br0"
SOURCE_NAD_NAME = "source-bridge-nad"
TARGET_NAD_NAME = "target-bridge-nad"


@pytest.fixture(scope="class")
def source_nad_scope_class(namespace):
    """
    Source bridge NAD for initial VM network attachment.

    Yields:
        NetworkAttachmentDefinition: Source NAD resource
    """
    with network_nad(
        nad_type=BRIDGE_NAD_TYPE,
        network_name=SOURCE_BRIDGE_NAME,
        nad_name=SOURCE_NAD_NAME,
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def target_nad_scope_class(namespace):
    """
    Target bridge NAD for destination network after NAD change.

    Yields:
        NetworkAttachmentDefinition: Target NAD resource
    """
    with network_nad(
        nad_type=BRIDGE_NAD_TYPE,
        network_name=TARGET_BRIDGE_NAME,
        nad_name=TARGET_NAD_NAME,
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def vm_with_source_nad_scope_class(
    unprivileged_client,
    namespace,
    source_nad_scope_class,
):
    """
    Running Fedora VM with secondary network interface attached via source NAD.

    Yields:
        VirtualMachineForTests: Running VM with secondary network on source NAD
    """
    name = "nad-hotplug-test-vm"
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=name,
        namespace=namespace.name,
        body=fedora_vm_body(name=name),
        networks={source_nad_scope_class.name: source_nad_scope_class.name},
        interfaces=[source_nad_scope_class.name],
    ) as vm:
        running_vm(vm=vm)
        yield vm


@pytest.fixture(scope="class")
def peer_vm_on_target_nad_scope_class(
    unprivileged_client,
    namespace,
    target_nad_scope_class,
):
    """
    Peer VM attached to target NAD for connectivity validation.

    Yields:
        VirtualMachineForTests: Running peer VM on target NAD
    """
    name = "nad-hotplug-peer-vm"
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=name,
        namespace=namespace.name,
        body=fedora_vm_body(name=name),
        networks={target_nad_scope_class.name: target_nad_scope_class.name},
        interfaces=[target_nad_scope_class.name],
    ) as vm:
        running_vm(vm=vm)
        yield vm


@pytest.fixture(scope="class")
def peer_vm_on_source_nad_scope_class(
    unprivileged_client,
    namespace,
    source_nad_scope_class,
):
    """
    Peer VM attached to source NAD for negative connectivity validation.

    Yields:
        VirtualMachineForTests: Running peer VM on source NAD
    """
    name = "nad-hotplug-source-peer-vm"
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=name,
        namespace=namespace.name,
        body=fedora_vm_body(name=name),
        networks={source_nad_scope_class.name: source_nad_scope_class.name},
        interfaces=[source_nad_scope_class.name],
    ) as vm:
        running_vm(vm=vm)
        yield vm


@pytest.fixture(scope="class")
def third_nad_scope_class(namespace):
    """
    Third bridge NAD for sequential and concurrent NAD change tests.

    Yields:
        NetworkAttachmentDefinition: Third NAD resource
    """
    with network_nad(
        nad_type=BRIDGE_NAD_TYPE,
        network_name="third-br0",
        nad_name="third-bridge-nad",
        namespace=namespace,
    ) as nad:
        yield nad
