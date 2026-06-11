"""
Shared fixtures for VMI domain reconciliation during virt-handler rolling update tests.

Jira: CNV-89519
"""

import logging

import pytest
from ocp_resources.daemonset import DaemonSet
from ocp_resources.pod import Pod
from timeout_sampler import TimeoutSampler

from utilities.constants import TIMEOUT_5MIN
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

LOGGER = logging.getLogger(__name__)
CNV_NAMESPACE = "openshift-cnv"
VIRT_HANDLER_LABEL = "kubevirt.io=virt-handler"


@pytest.fixture(scope="class")
def running_fedora_vm_scope_class(unprivileged_client, namespace):
    """Running Fedora VM for virt-handler restart testing."""
    name = "vm-virt-handler-restart"
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=name,
        namespace=namespace.name,
        body=fedora_vm_body(name=name),
    ) as vm:
        running_vm(vm=vm)
        yield vm


@pytest.fixture(scope="class")
def virt_handler_pod_on_vm_node_scope_class(admin_client, running_fedora_vm_scope_class):
    """Identify the virt-handler pod running on the same node as the test VM."""
    vmi = running_fedora_vm_scope_class.vmi
    vm_node_name = vmi.instance.status.nodeName
    LOGGER.info(f"VM is running on node: {vm_node_name}")
    virt_handler_pods = list(
        Pod.get(
            client=admin_client,
            namespace=CNV_NAMESPACE,
            label_selector=VIRT_HANDLER_LABEL,
            field_selector=f"spec.nodeName={vm_node_name}",
        )
    )
    assert virt_handler_pods, (
        f"No virt-handler pod found on node {vm_node_name}"
    )
    yield virt_handler_pods[0]


@pytest.fixture(scope="class")
def multiple_fedora_vms_on_same_node_scope_class(
    unprivileged_client, namespace, workers
):
    """Create 10+ Fedora VMs scheduled on the same worker node."""
    target_node = workers[0].name
    LOGGER.info(f"Creating multiple VMs on node: {target_node}")
    vms = []
    for index in range(10):
        name = f"vm-scale-{index}"
        vm = VirtualMachineForTests(
            client=unprivileged_client,
            name=name,
            namespace=namespace.name,
            body=fedora_vm_body(name=name),
            node_selector={"kubernetes.io/hostname": target_node},
        )
        vm.__enter__()
        vms.append(vm)

    for vm in vms:
        running_vm(vm=vm)

    yield vms

    for vm in reversed(vms):
        vm.__exit__(None, None, None)


@pytest.fixture(scope="class")
def target_node_name_scope_class(multiple_fedora_vms_on_same_node_scope_class):
    """Node name where all scale VMs are running."""
    vmi = multiple_fedora_vms_on_same_node_scope_class[0].vmi
    yield vmi.instance.status.nodeName


@pytest.fixture(scope="class")
def virt_handler_pod_on_target_node_scope_class(
    admin_client, target_node_name_scope_class
):
    """Identify the virt-handler pod running on the target scale-test node."""
    virt_handler_pods = list(
        Pod.get(
            client=admin_client,
            namespace=CNV_NAMESPACE,
            label_selector=VIRT_HANDLER_LABEL,
            field_selector=f"spec.nodeName={target_node_name_scope_class}",
        )
    )
    assert virt_handler_pods, (
        f"No virt-handler pod found on node {target_node_name_scope_class}"
    )
    yield virt_handler_pods[0]


def wait_for_virt_handler_ready(admin_client, node_name):
    """Wait for a replacement virt-handler pod to become Ready on the given node."""
    LOGGER.info(f"Waiting for replacement virt-handler pod on node: {node_name}")
    for sample in TimeoutSampler(
        wait_timeout=TIMEOUT_5MIN,
        sleep=5,
        func=_get_ready_virt_handler_pod,
        admin_client=admin_client,
        node_name=node_name,
    ):
        if sample:
            LOGGER.info(f"Replacement virt-handler pod ready: {sample.name}")
            return sample


def _get_ready_virt_handler_pod(admin_client, node_name):
    """Return the virt-handler pod on a node if it is Ready, else None."""
    pods = list(
        Pod.get(
            client=admin_client,
            namespace=CNV_NAMESPACE,
            label_selector=VIRT_HANDLER_LABEL,
            field_selector=f"spec.nodeName={node_name}",
        )
    )
    for pod in pods:
        conditions = pod.instance.status.conditions or []
        for condition in conditions:
            if condition.type == "Ready" and condition.status == "True":
                return pod
    return None
