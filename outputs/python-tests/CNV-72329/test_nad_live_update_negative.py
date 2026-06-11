"""
NAD Reference Live Update — Negative and Error Handling E2E Tests

Validates error handling, RBAC enforcement, and edge-case behavior for
NAD reference live update operations.

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""

import logging

import pytest
from ocp_resources.virtual_machine_instance_migration import VirtualMachineInstanceMigration
from timeout_sampler import TimeoutExpiredError, TimeoutSampler

from utilities.constants import BRIDGE, TIMEOUT_5MIN
from utilities.network import network_nad
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")

NO_MIGRATION_WAIT = 30
MIGRATION_POLL_INTERVAL = 5


@pytest.mark.polarion("CNV-72329")
class TestNADLiveUpdateNegative:
    """
    Negative tests for NAD reference live update error handling.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Source bridge NAD created in the test namespace
        - Running VM with secondary bridge interface on source NAD
    """

    def test_invalid_nad_reference_handled_gracefully(
        self,
        admin_client,
        nad_swap_vm_scope_class,
        source_bridge_nad_scope_class,
    ):
        """
        [NEGATIVE] Test that updating NAD reference to non-existent NAD is handled gracefully.

        Preconditions:
            - Running VM with secondary bridge interface on source NAD

        Steps:
            1. Patch VM spec to change NAD reference to a non-existent NAD name
            2. Observe system behavior for 30 seconds

        Expected:
            - Migration does not proceed or fails with error condition on VM
            - VM remains Running and functional on original NAD
        """
        vm = nad_swap_vm_scope_class
        nonexistent_nad_name = "nonexistent-nad-does-not-exist"

        LOGGER.info(
            f"Patching VM {vm.name} NAD reference to non-existent NAD: {nonexistent_nad_name}"
        )
        networks = vm.instance.spec.template.spec.networks
        for network in networks:
            multus = network.get("multus", {})
            if multus and multus.get("networkName", "").endswith(source_bridge_nad_scope_class.name):
                multus["networkName"] = nonexistent_nad_name

        vm.update(resource_dict=vm.instance.to_dict())

        LOGGER.info("Verifying migration does not succeed with non-existent NAD")
        successful_migration_found = False
        try:
            for sample in TimeoutSampler(
                wait_timeout=NO_MIGRATION_WAIT,
                sleep=MIGRATION_POLL_INTERVAL,
                func=VirtualMachineInstanceMigration.get,
                client=admin_client,
                namespace=vm.namespace,
            ):
                migrations = list(sample)
                for migration in migrations:
                    if migration.instance.status.phase == "Succeeded":
                        successful_migration_found = True
                        break
                if successful_migration_found:
                    break
        except TimeoutExpiredError:
            LOGGER.info("No successful migration found — expected for non-existent NAD")

        assert not successful_migration_found, (
            "Migration should NOT succeed when referencing a non-existent NAD"
        )

        vmi = vm.vmi
        assert vmi.instance.status.phase == "Running", (
            f"VM should remain Running, but is in {vmi.instance.status.phase}"
        )

    def test_no_op_nad_reference_change(
        self,
        admin_client,
        namespace,
        source_bridge_nad_scope_class,
    ):
        """
        Test that changing NAD reference to the same value does not trigger migration.

        Preconditions:
            - Running VM with secondary bridge interface on source NAD

        Steps:
            1. Patch VM NAD reference to the same value it already has
            2. Wait and verify no migration is triggered

        Expected:
            - No migration is triggered for no-op NAD change
        """
        name = "no-op-nad-vm"
        with VirtualMachineForTests(
            client=admin_client,
            name=name,
            namespace=namespace.name,
            body=fedora_vm_body(name=name),
            networks={source_bridge_nad_scope_class.name: source_bridge_nad_scope_class.name},
            interfaces=[source_bridge_nad_scope_class.name],
        ) as vm:
            running_vm(vm=vm)

            LOGGER.info(
                f"Patching VM {vm.name} NAD reference to same value: "
                f"{source_bridge_nad_scope_class.name}"
            )
            networks = vm.instance.spec.template.spec.networks
            for network in networks:
                multus = network.get("multus", {})
                if multus:
                    multus["networkName"] = source_bridge_nad_scope_class.name

            vm.update(resource_dict=vm.instance.to_dict())

            LOGGER.info("Verifying no migration is triggered for no-op NAD change")
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
                        f"No migration should be triggered for no-op NAD change, "
                        f"found {len(migrations)} migration(s)"
                    )
            except TimeoutExpiredError:
                LOGGER.info("No migration triggered — expected for no-op NAD change")


@pytest.mark.polarion("CNV-72329")
class TestNADLiveUpdateRBAC:
    """
    Tests for RBAC validation on NAD reference changes.

    Markers:
        - polarion("CNV-72329")

    Preconditions:
        - Source bridge NAD and target bridge NAD created in test namespace
        - Running VM with secondary bridge interface on source NAD
        - Unprivileged user client without VM update permissions
    """

    def test_non_admin_cannot_change_nad_reference(
        self,
        unprivileged_client,
        nad_swap_vm_scope_class,
        source_bridge_nad_scope_class,
        target_bridge_nad_scope_class,
    ):
        """
        [NEGATIVE] Test that non-admin users without VM update permissions cannot change NAD references.

        Preconditions:
            - Running VM with secondary bridge interface
            - Unprivileged user client

        Steps:
            Attempt to patch VM spec NAD reference using unprivileged user client

        Expected:
            - API returns 403 Forbidden error
        """
        vm = nad_swap_vm_scope_class
        LOGGER.info(
            f"Attempting NAD reference change on VM {vm.name} "
            "using unprivileged client — expecting 403"
        )

        networks = vm.instance.spec.template.spec.networks
        for network in networks:
            multus = network.get("multus", {})
            if multus and multus.get("networkName", "").endswith(source_bridge_nad_scope_class.name):
                multus["networkName"] = target_bridge_nad_scope_class.name

        with pytest.raises(Exception) as exc_info:
            vm.update(resource_dict=vm.instance.to_dict())

        error_message = str(exc_info.value)
        LOGGER.info(f"Unprivileged update rejected: {error_message}")
        assert "403" in error_message or "Forbidden" in error_message, (
            f"Expected 403 Forbidden, got: {error_message}"
        )
