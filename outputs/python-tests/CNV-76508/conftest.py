"""
Shared fixtures for Cross-Cluster Migration Proxy tests.

Jira: https://redhat.atlassian.net/browse/CNV-76508
"""

import logging

import pytest

from utilities.virt import (
    VirtualMachineForTests,
    fedora_vm_body,
    running_vm,
)

LOGGER = logging.getLogger(__name__)


@pytest.fixture(scope="class")
def proxy_enabled_vm_scope_class(unprivileged_client, namespace):
    """Create a Fedora VM with proxy-enabled migration configuration.

    Yields:
        VirtualMachineForTests: Running VM ready for proxy migration testing.
    """
    vm_name = "proxy-enabled-vm"
    LOGGER.info(f"Creating proxy-enabled VM: {vm_name}")
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=vm_name,
        namespace=namespace.name,
        body=fedora_vm_body(name=vm_name),
    ) as vm:
        running_vm(vm=vm)
        LOGGER.info(f"VM {vm_name} is running and ready for proxy migration tests")
        yield vm
