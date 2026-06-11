"""
NAD Reference Live Update — Validation and Namespace Handling Tests

Validates namespace-qualified vs unqualified NAD name comparison, pod network
isolation from NAD sync logic, and VM controller spec sync behavior.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging

import pytest
from ocp_resources.virtual_machine_instance_migration import VirtualMachineInstanceMigration
from timeout_sampler import TimeoutExpiredError, TimeoutSampler

from conftest import MIGRATION_POLL_INTERVAL, wait_for_nad_swap_migration
from utilities.constants import BRIDGE
from utilities.network import network_nad
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")

NO_MIGRATION_WAIT = 30


@pytest.mark.polarion("CNV-72329")
class TestNADNamespaceQualification:
    """
    Tests for namespace-qualified and unqualified NAD name comparison.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Source bridge NAD created in test namespace
        - Running VM with secondary bridge interface referencing source NAD
    """

    def test_qualified_and_unqualified_nad_names_equivalent(
        self,
        admin_client,
        namespace,
        source_bridge_nad_scope_class,
    ):
        """
        Test that namespace-qualified NAD name is treated as equivalent to unqualified name.

        Preconditions:
            - Running VM with secondary bridge interface on source NAD

        Steps:
            1. Patch VM NAD reference using namespace-qualified name for the same NAD
            2. Wait and verify no migration is triggered

        Expected:
            - No false migration triggered for equivalent NAD references
        """
        name = "ns-qual-test-vm"
        with VirtualMachineForTests(
            client=admin_client,
            name=name,
            namespace=namespace.name,
            body=fedora_vm_body(name=name),
            networks={source_bridge_nad_scope_class.name: source_bridge_nad_scope_class.name},
            interfaces=[source_bridge_nad_scope_class.name],
        ) as vm:
            running_vm(vm=vm)

            qualified_name = f"{namespace.name}/{source_bridge_nad_scope_class.name}"
            LOGGER.info(
                f"Patching VM {vm.name} NAD reference to namespace-qualified name: {qualified_name}"
            )

            networks = vm.instance.spec.template.spec.networks
            for network in networks:
                multus = network.get("multus", {})
                if multus:
                    multus["networkName"] = qualified_name

            vm.update(resource_dict=vm.instance.to_dict())

            LOGGER.info("Verifying no migration triggered for equivalent NAD name")
            try:
                for sample in TimeoutSampler(
                    wait_timeout=NO_MIGRATION_WAIT,
                    sleep=MIGRATION_POLL_INTERVAL,
                    func=VirtualMachineInstanceMigration.get,
                    client=admin_client,
                    namespace=vm.namespace,
                ):
                    migrations = list(sample)
                    assert len(migrations) == 0, (
                        f"No migration should be triggered for equivalent NAD names "
                        f"(qualified vs unqualified), found {len(migrations)}"
                    )
            except TimeoutExpiredError:
                LOGGER.info(
                    "No migration triggered for qualified/unqualified equivalence — expected"
                )


@pytest.mark.polarion("CNV-72329")
class TestPodNetworkIsolation:
    """
    Tests verifying pod network (default) is not affected by NAD sync logic.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Running VM with default pod network and secondary bridge interface
        - Source and target bridge NADs created
    """

    def test_pod_network_unchanged_after_nad_swap(
        self,
        admin_client,
        nad_swap_vm_scope_class,
        source_bridge_nad_scope_class,
        target_bridge_nad_scope_class,
    ):
        """
        Test that the pod network (default) remains unchanged after NAD swap.

        Preconditions:
            - Running VM with default pod network and secondary bridge interface

        Steps:
            1. Record pod network configuration before NAD swap
            2. Perform NAD swap on secondary interface
            3. Verify pod network configuration is unchanged

        Expected:
            - Pod network (default masquerade) is identical before and after NAD swap
        """
        vm = nad_swap_vm_scope_class
        vmi = vm.vmi

        pre_swap_default_network = None
        for network in vmi.instance.spec.networks:
            if network.get("pod"):
                pre_swap_default_network = network
                break

        assert pre_swap_default_network is not None, "VM should have a pod (default) network"
        LOGGER.info(f"Pre-swap pod network config: {pre_swap_default_network}")

        LOGGER.info("Performing NAD swap on secondary interface")
        networks = vm.instance.spec.template.spec.networks
        for network in networks:
            multus = network.get("multus", {})
            if multus and multus.get("networkName", "").endswith(source_bridge_nad_scope_class.name):
                multus["networkName"] = target_bridge_nad_scope_class.name

        vm.update(resource_dict=vm.instance.to_dict())

        wait_for_nad_swap_migration(
            admin_client=admin_client,
            namespace=vm.namespace,
        )

        post_swap_default_network = None
        updated_vmi = vm.vmi
        for network in updated_vmi.instance.spec.networks:
            if network.get("pod"):
                post_swap_default_network = network
                break

        assert post_swap_default_network is not None, (
            "Pod network should still exist after NAD swap"
        )
        assert pre_swap_default_network == post_swap_default_network, (
            f"Pod network changed after NAD swap: "
            f"before={pre_swap_default_network}, after={post_swap_default_network}"
        )
        LOGGER.info("Pod network unchanged after NAD swap — verified")


@pytest.mark.polarion("CNV-72329")
class TestVMISpecSync:
    """
    Tests for VM controller NAD reference sync from VM spec to VMI spec.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - Source and target bridge NADs created
        - Running VM with secondary bridge interface on source NAD
    """

    def test_vmi_spec_synced_after_nad_change(
        self,
        admin_client,
        nad_swap_vm_scope_class,
        source_bridge_nad_scope_class,
        target_bridge_nad_scope_class,
    ):
        """
        Test that VMI spec networks are updated to match VM spec after NAD change.

        Preconditions:
            - Running VM with secondary bridge interface on source NAD

        Steps:
            1. Patch VM NAD reference to target NAD
            2. Wait for VMI spec to reflect the updated NAD reference

        Expected:
            - VMI spec networks contain the target NAD name within 30 seconds
        """
        vm = nad_swap_vm_scope_class
        LOGGER.info(
            f"Patching VM {vm.name} NAD reference: "
            f"{source_bridge_nad_scope_class.name} -> {target_bridge_nad_scope_class.name}"
        )

        networks = vm.instance.spec.template.spec.networks
        for network in networks:
            multus = network.get("multus", {})
            if multus and multus.get("networkName", "").endswith(source_bridge_nad_scope_class.name):
                multus["networkName"] = target_bridge_nad_scope_class.name

        vm.update(resource_dict=vm.instance.to_dict())

        LOGGER.info("Waiting for VMI spec to sync with updated NAD reference")
        vmi_synced = False
        for sample in TimeoutSampler(
            wait_timeout=NO_MIGRATION_WAIT,
            sleep=MIGRATION_POLL_INTERVAL,
            func=lambda: vm.vmi.instance.spec.networks,
        ):
            for network in sample:
                multus = network.get("multus", {})
                if multus and target_bridge_nad_scope_class.name in multus.get("networkName", ""):
                    vmi_synced = True
                    break
            if vmi_synced:
                break

        assert vmi_synced, (
            f"VMI spec should contain target NAD {target_bridge_nad_scope_class.name} "
            f"within {NO_MIGRATION_WAIT}s"
        )
        LOGGER.info("VMI spec synced with VM spec NAD reference — verified")
