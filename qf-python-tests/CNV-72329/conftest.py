"""
Shared fixtures for NAD Reference Live Update tests.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging
from typing import Generator

import pytest

from ocp_resources.network_attachment_definition import NetworkAttachmentDefinition
from ocp_resources.virtual_machine import VirtualMachine
from ocp_resources.virtual_machine_instance import VirtualMachineInstance

from utilities.network import network_nad
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

LOGGER = logging.getLogger(__name__)

SOURCE_NAD_BRIDGE = "br-src-1"
TARGET_NAD_BRIDGE = "br-tgt-2"
SOURCE_NAD_NAME = "nad-source"
TARGET_NAD_NAME = "nad-target"


@pytest.fixture(scope="class")
def source_nad_scope_class(
    namespace,
) -> Generator[NetworkAttachmentDefinition, None, None]:
    """
    Source bridge-based NAD for NAD live update tests.

    Yields:
        NetworkAttachmentDefinition: Source NAD resource on br-src-1.
    """
    with network_nad(
        nad_type="bridge",
        network_name=SOURCE_NAD_BRIDGE,
        nad_name=SOURCE_NAD_NAME,
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def target_nad_scope_class(
    namespace,
) -> Generator[NetworkAttachmentDefinition, None, None]:
    """
    Target bridge-based NAD for NAD live update tests.

    Yields:
        NetworkAttachmentDefinition: Target NAD resource on br-tgt-2.
    """
    with network_nad(
        nad_type="bridge",
        network_name=TARGET_NAD_BRIDGE,
        nad_name=TARGET_NAD_NAME,
        namespace=namespace,
    ) as nad:
        yield nad


@pytest.fixture(scope="class")
def vm_with_source_nad_scope_class(
    unprivileged_client,
    namespace,
    source_nad_scope_class,
) -> Generator[VirtualMachineForTests, None, None]:
    """
    VM with a secondary interface attached to the source NAD.

    Yields:
        VirtualMachineForTests: Running VM with secondary bridge interface.
    """
    name = "nad-live-update-vm"
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
def peer_vmi_source_scope_class(
    unprivileged_client,
    namespace,
    source_nad_scope_class,
) -> Generator[VirtualMachineForTests, None, None]:
    """
    Peer VM on the source NAD for pre-swap connectivity verification.

    Yields:
        VirtualMachineForTests: Running peer VM on source bridge.
    """
    name = "peer-source-vmi"
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
def peer_vmi_target_scope_class(
    unprivileged_client,
    namespace,
    target_nad_scope_class,
) -> Generator[VirtualMachineForTests, None, None]:
    """
    Peer VM on the target NAD for post-swap connectivity verification.

    Yields:
        VirtualMachineForTests: Running peer VM on target bridge.
    """
    name = "peer-target-vmi"
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
