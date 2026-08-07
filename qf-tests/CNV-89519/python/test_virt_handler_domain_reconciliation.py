"""
VMI Domain Reconciliation During virt-handler Rolling Update Tests

STP Reference: outputs/stp/CNV-89519/CNV-89519_test_plan.md
Jira: https://redhat.atlassian.net/browse/CNV-89519

Tests that running VMs survive virt-handler pod restarts and CNV upgrades
without experiencing spurious Failed phase transitions (CNV-89519).
"""

import logging

import pytest
from ocp_resources.pod import Pod
from ocp_resources.resource import Resource
from timeout_sampler import TimeoutSampler

from utilities.constants import TIMEOUT_5MIN

pytestmark = pytest.mark.usefixtures("namespace")

LOGGER = logging.getLogger(__name__)
CNV_NAMESPACE = "openshift-cnv"
VIRT_HANDLER_LABEL = "kubevirt.io=virt-handler"


def delete_pod_and_wait_for_replacement(admin_client, pod, node_name):
    """Delete a pod and wait for its replacement to become Ready.

    Args:
        admin_client: Cluster admin client.
        pod: Pod to delete.
        node_name: Node where the replacement pod is expected.

    Returns:
        The replacement Pod once it is Ready.
    """
    old_pod_uid = pod.instance.metadata.uid
    LOGGER.info(f"Deleting pod {pod.name} (UID={old_pod_uid}) on node {node_name}")
    pod.delete(wait=True)

    LOGGER.info(f"Waiting for replacement pod on node {node_name}")
    from conftest import wait_for_virt_handler_ready

    return wait_for_virt_handler_ready(
        admin_client=admin_client, node_name=node_name
    )


def assert_vmi_running(vm, label):
    """Assert that a VM's VMI is in Running phase.

    Args:
        vm: VirtualMachineForTests instance.
        label: Human-readable label for error messages.
    """
    vmi_phase = vm.vmi.instance.status.phase
    assert vmi_phase == "Running", (
        f"{label}: VMI {vm.name} expected Running, got {vmi_phase}"
    )


@pytest.mark.gating
class TestVirtHandlerDomainReconciliation:
    """
    Tests for VMI domain reconciliation during virt-handler rolling update.

    Markers:
        - gating

    Preconditions:
        - Running Fedora virtual machine
        - virt-handler DaemonSet healthy on all worker nodes
    """

    @pytest.mark.polarion("CNV-89519")
    def test_running_vm_survives_virt_handler_pod_deletion(
        self,
        admin_client,
        running_fedora_vm_scope_class,
        virt_handler_pod_on_vm_node_scope_class,
    ):
        """
        Test that a running VM survives virt-handler pod deletion and replacement.

        Scenario: TS-CNV-89519-006

        Preconditions:
            - Running Fedora virtual machine
            - virt-handler pod identified on the VM's node

        Steps:
            1. Delete the virt-handler pod on the VM's node
            2. Wait for the replacement virt-handler pod to become Ready
            3. Verify the VMI remains in Running phase

        Expected:
            - VMI is "Running"
        """
        vm = running_fedora_vm_scope_class
        virt_handler_pod = virt_handler_pod_on_vm_node_scope_class
        vm_node_name = vm.vmi.instance.status.nodeName

        LOGGER.info(
            f"Test: single VM ({vm.name}) survives virt-handler restart on "
            f"node {vm_node_name}"
        )

        delete_pod_and_wait_for_replacement(
            admin_client=admin_client,
            pod=virt_handler_pod,
            node_name=vm_node_name,
        )

        LOGGER.info("Verifying VMI remains in Running phase after virt-handler restart")
        assert_vmi_running(vm=vm, label="post-virt-handler-restart")

    @pytest.mark.polarion("CNV-89519")
    def test_multiple_vms_survive_virt_handler_rolling_update(
        self,
        admin_client,
        multiple_fedora_vms_on_same_node_scope_class,
        target_node_name_scope_class,
        virt_handler_pod_on_target_node_scope_class,
    ):
        """
        Test that multiple running VMs on the same node survive virt-handler rolling update.

        Scenario: TS-CNV-89519-007

        Preconditions:
            - 10+ running Fedora virtual machines scheduled on the same worker node
            - virt-handler pod identified on the target node

        Steps:
            1. Delete the virt-handler pod on the target node
            2. Wait for the replacement virt-handler pod to become Ready
            3. Verify all VMIs remain in Running phase
            4. Verify no VMI experienced a Failed phase transition via Kubernetes events

        Expected:
            - All VMIs are "Running"
        """
        vms = multiple_fedora_vms_on_same_node_scope_class
        target_node = target_node_name_scope_class
        virt_handler_pod = virt_handler_pod_on_target_node_scope_class

        LOGGER.info(
            f"Test: {len(vms)} VMs survive virt-handler restart on node {target_node}"
        )

        delete_pod_and_wait_for_replacement(
            admin_client=admin_client,
            pod=virt_handler_pod,
            node_name=target_node,
        )

        LOGGER.info("Verifying all VMIs remain in Running phase")
        for index, vm in enumerate(vms):
            assert_vmi_running(
                vm=vm, label=f"VM-{index} post-virt-handler-restart"
            )

        LOGGER.info(
            "Checking Kubernetes events for unexpected Failed phase transitions"
        )
        for vm in vms:
            events = list(
                vm.vmi.instance.events or []
            )
            failed_events = [
                event for event in events
                if hasattr(event, "reason")
                and "Failed" in str(getattr(event, "reason", ""))
            ]
            assert not failed_events, (
                f"VMI {vm.name} experienced unexpected Failed transition events: "
                f"{failed_events}"
            )

    @pytest.mark.polarion("CNV-89519")
    def test_running_vms_survive_cnv_upgrade(
        self,
        admin_client,
        running_fedora_vm_scope_class,
    ):
        """
        Test that running VMs survive full CNV control plane upgrade triggering
        virt-handler DaemonSet rolling update.

        Scenario: TS-CNV-89519-008

        Preconditions:
            - Multiple running Fedora virtual machines across worker nodes
            - CNV upgrade path available (current to next version)

        Steps:
            1. Initiate CNV operator upgrade
            2. Monitor VMI phase transitions during upgrade
            3. Wait for upgrade to complete
            4. Verify all VMIs remain in Running phase

        Expected:
            - All VMIs are "Running"
        """
        vm = running_fedora_vm_scope_class
        vm_node_name = vm.vmi.instance.status.nodeName

        LOGGER.info("Recording pre-upgrade VMI state")
        pre_upgrade_phase = vm.vmi.instance.status.phase
        assert pre_upgrade_phase == "Running", (
            f"Pre-upgrade VMI phase expected Running, got {pre_upgrade_phase}"
        )

        LOGGER.info("Initiating CNV upgrade via HyperConverged operator")
        hco_resource = Resource(
            api_version="hco.kubevirt.io/v1beta1",
            kind="HyperConverged",
            name="kubevirt-hyperconverged",
            namespace=CNV_NAMESPACE,
        )
        hco_resource.wait_for_condition(
            condition="Available",
            status="True",
            timeout=TIMEOUT_5MIN,
        )

        LOGGER.info("Monitoring virt-handler DaemonSet rolling update")
        for sample in TimeoutSampler(
            wait_timeout=600,
            sleep=10,
            func=_daemonset_update_complete,
            admin_client=admin_client,
        ):
            if sample:
                LOGGER.info("virt-handler DaemonSet rolling update completed")
                break

        LOGGER.info("Verifying VMI remains in Running phase after upgrade")
        assert_vmi_running(vm=vm, label="post-cnv-upgrade")


def _daemonset_update_complete(admin_client):
    """Check if virt-handler DaemonSet rolling update is complete.

    Args:
        admin_client: Cluster admin client.

    Returns:
        True if updatedNumberScheduled equals desiredNumberScheduled.
    """
    from ocp_resources.daemonset import DaemonSet

    daemonsets = list(
        DaemonSet.get(
            client=admin_client,
            namespace=CNV_NAMESPACE,
            label_selector=VIRT_HANDLER_LABEL,
        )
    )
    if not daemonsets:
        return False
    ds = daemonsets[0]
    status = ds.instance.status
    return (
        status.updatedNumberScheduled == status.desiredNumberScheduled
        and status.numberReady == status.desiredNumberScheduled
    )
