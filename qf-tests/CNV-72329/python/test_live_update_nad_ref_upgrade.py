"""
Live Update NAD Reference - Upgrade/Backward Compatibility Tests

Validates that VMs created before LiveUpdateNADRef FG enablement support
NAD reference changes after the feature gate is enabled.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging

import pytest
from ocp_resources.kubevirt import KubeVirt
from timeout_sampler import TimeoutSampler

from utilities.constants import TIMEOUT_5MIN, TIMEOUT_10MIN
from utilities.virt import (
    VirtualMachineForTests,
    fedora_vm_body,
    running_vm,
    wait_for_vm_interfaces,
)

from conftest import (
    SECONDARY_IFACE_NAME,
    _create_bridge_nad,
    _patch_vm_nad_reference,
    _wait_for_migration_success,
)

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")

LIVE_UPDATE_NAD_REF_FG = "LiveUpdateNADRef"


def _get_kubevirt_feature_gates(admin_client, hco_namespace):
    """Get current feature gates from KubeVirt CR.

    Args:
        admin_client: Kubernetes admin client.
        hco_namespace: HCO operator namespace.

    Returns:
        list: Current feature gates.
    """
    kubevirt_list = list(KubeVirt.get(client=admin_client, namespace=hco_namespace.name))
    assert kubevirt_list, "No KubeVirt CR found"
    kv = kubevirt_list[0]
    dev_config = kv.instance.spec.get("configuration", {}).get("developerConfiguration", {})
    return dev_config.get("featureGates", [])


def _set_kubevirt_feature_gate(admin_client, hco_namespace, feature_gate, enabled):
    """Enable or disable a feature gate on the KubeVirt CR.

    Args:
        admin_client: Kubernetes admin client.
        hco_namespace: HCO operator namespace.
        feature_gate: Feature gate name.
        enabled: Whether to enable (True) or disable (False) the feature gate.
    """
    kubevirt_list = list(KubeVirt.get(client=admin_client, namespace=hco_namespace.name))
    assert kubevirt_list, "No KubeVirt CR found"
    kv = kubevirt_list[0]

    current_gates = _get_kubevirt_feature_gates(
        admin_client=admin_client,
        hco_namespace=hco_namespace,
    )

    if enabled and feature_gate not in current_gates:
        current_gates.append(feature_gate)
        LOGGER.info(f"Enabling feature gate: {feature_gate}")
    elif not enabled and feature_gate in current_gates:
        current_gates.remove(feature_gate)
        LOGGER.info(f"Disabling feature gate: {feature_gate}")
    else:
        LOGGER.info(f"Feature gate {feature_gate} already in desired state (enabled={enabled})")
        return

    kv.update(
        resource_dict={
            "spec": {
                "configuration": {
                    "developerConfiguration": {
                        "featureGates": current_gates,
                    },
                },
            },
        },
    )

    for sample in TimeoutSampler(
        wait_timeout=TIMEOUT_5MIN,
        sleep=5,
        func=lambda: feature_gate in _get_kubevirt_feature_gates(
            admin_client=admin_client,
            hco_namespace=hco_namespace,
        ),
    ):
        if sample == enabled:
            break

    LOGGER.info(f"Feature gate {feature_gate} is now {'enabled' if enabled else 'disabled'}")


@pytest.mark.polarion("CNV-72329")
class TestLiveUpdateNADRefUpgrade:
    """
    Tests for backward compatibility: VMs created before FG enablement.

    Markers:
        - gating

    Preconditions:
        - Cluster with LiveUpdateNADRef FG configurable
        - Bridge NADs in test namespace
    """

    def test_existing_vms_work_after_fg_enablement(
        self,
        admin_client,
        unprivileged_client,
        namespace,
        hco_namespace,
    ):
        """
        Test that existing VMs work correctly after upgrading to version
        with LiveUpdateNADRef FG.

        Preconditions:
            - LiveUpdateNADRef feature gate initially disabled
            - Two bridge NADs in test namespace

        Steps:
            1. Disable LiveUpdateNADRef FG
            2. Create NADs and VM with secondary interface
            3. Enable LiveUpdateNADRef FG
            4. Change NAD reference on pre-existing VM
            5. Wait for migration to complete

        Expected:
            - Migration succeeds on pre-existing VM
        """
        original_gates = _get_kubevirt_feature_gates(
            admin_client=admin_client,
            hco_namespace=hco_namespace,
        )
        original_fg_enabled = LIVE_UPDATE_NAD_REF_FG in original_gates

        try:
            _set_kubevirt_feature_gate(
                admin_client=admin_client,
                hco_namespace=hco_namespace,
                feature_gate=LIVE_UPDATE_NAD_REF_FG,
                enabled=False,
            )

            LOGGER.info("Creating NADs and VM while FG is disabled")
            with _create_bridge_nad(
                client=admin_client,
                namespace=namespace.name,
                name="nad-upgrade-a",
                vlan=100,
            ) as nad_a, _create_bridge_nad(
                client=admin_client,
                namespace=namespace.name,
                name="nad-upgrade-b",
                vlan=200,
            ) as nad_b:
                with VirtualMachineForTests(
                    client=unprivileged_client,
                    name="vm-pre-upgrade",
                    namespace=namespace.name,
                    body=fedora_vm_body(name="vm-pre-upgrade"),
                    networks={SECONDARY_IFACE_NAME: nad_a.name},
                    interfaces=[SECONDARY_IFACE_NAME],
                ) as vm:
                    running_vm(vm=vm)
                    wait_for_vm_interfaces(vmi=vm.vmi)

                    LOGGER.info("VM created before FG enablement, now enabling FG")
                    _set_kubevirt_feature_gate(
                        admin_client=admin_client,
                        hco_namespace=hco_namespace,
                        feature_gate=LIVE_UPDATE_NAD_REF_FG,
                        enabled=True,
                    )

                    LOGGER.info("Changing NAD reference on pre-existing VM")
                    _patch_vm_nad_reference(
                        vm=vm,
                        interface_name=SECONDARY_IFACE_NAME,
                        new_nad_name=nad_b.name,
                    )
                    _wait_for_migration_success(
                        client=admin_client,
                        namespace=namespace.name,
                        vm_name=vm.name,
                    )
                    LOGGER.info(
                        f"Migration succeeded on pre-existing VM {vm.name} "
                        f"after FG enablement"
                    )
        finally:
            _set_kubevirt_feature_gate(
                admin_client=admin_client,
                hco_namespace=hco_namespace,
                feature_gate=LIVE_UPDATE_NAD_REF_FG,
                enabled=original_fg_enabled,
            )
