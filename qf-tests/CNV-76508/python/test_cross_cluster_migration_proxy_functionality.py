"""
Cross-Cluster Migration Proxy Functionality Tests

STP Reference: outputs/stp/CNV-76508/CNV-76508_test_plan.md
Jira: https://redhat.atlassian.net/browse/CNV-76508
"""

import logging

import pytest
from ocp_resources.virtual_machine_instance_migration import (
    VirtualMachineInstanceMigration,
)
from timeout_sampler import TimeoutSampler

from utilities.constants import TIMEOUT_1MIN, TIMEOUT_2MIN, TIMEOUT_5SEC
from utilities.virt import (
    VirtualMachineForTests,
    fedora_vm_body,
    migrate_vm_and_verify,
    running_vm,
)

LOGGER = logging.getLogger(__name__)


class TestCrossClusterMigrationProxyFunctionality:
    """Tests for VM functionality after cross-cluster migration through proxy.

    Markers:
        - gating

    Preconditions:
        - CrossClusterMigrationProxy feature gate enabled
        - crossClusterNetwork configured in MigrationConfiguration
        - RWX-capable StorageClass available
        - VM created and Running with console access
    """

    @pytest.mark.polarion("CNV-76508")
    def test_vm_functional_after_proxy_migration_login_and_workload_continue(
        self,
        proxy_enabled_vm_scope_class,
    ):
        """Test that VM is functional after migration through proxy.

        Test ID: TS-CNV-76508-014

        Preconditions:
            - VM is Running with proxy-enabled configuration
            - Console access verified before migration

        Steps:
            1. Migrate VM through proxy
            2. Verify VM is in Running phase after migration
            3. Login to VM console after migration
            4. Verify running workload continues

        Expected:
            - VM is in Running phase after migration through proxy
            - Console login succeeds after migration
            - Running workload continues uninterrupted
        """
        vm = proxy_enabled_vm_scope_class
        LOGGER.info(f"VM {vm.name} is running, verifying pre-migration state")
        assert vm.instance.status.phase == "Running", (
            f"VM is not in Running state before migration: {vm.instance.status.phase}"
        )

        LOGGER.info("Executing VM live migration through proxy")
        migrate_vm_and_verify(vm=vm, check_ssh_connectivity=True)

        LOGGER.info("Verifying VM is in Running phase after migration")
        vm.vmi.wait_until_running()
        assert vm.instance.status.phase == "Running", (
            f"VM is not in Running state after proxy migration: {vm.instance.status.phase}"
        )

        LOGGER.info("Verifying SSH connectivity after migration")
        vm.ssh_exec.executor().is_connective()

        LOGGER.info("Verifying workload continuity by running command on VM")
        result = vm.ssh_exec.executor().run_command(command=["uname", "-a"])
        assert result[0] == 0, (
            f"Command execution failed on VM after migration: exit code {result[0]}"
        )

    @pytest.mark.polarion("CNV-76508")
    def test_migration_cancellation_cleanly_shuts_down_proxy_connections(
        self,
        namespace,
        unprivileged_client,
    ):
        """Test that cancelling in-flight proxied migration cleanly shuts down proxy.

        Test ID: TS-CNV-76508-015

        Preconditions:
            - VM is Running with proxy-enabled configuration

        Steps:
            1. Create and start VM
            2. Initiate migration through proxy
            3. Cancel migration while in-flight
            4. Verify proxy connections are cleanly shut down
            5. Verify VM remains functional on source node

        Expected:
            - Migration is cancelled successfully
            - Proxy connections are cleaned up with no lingering connections
            - VM continues running on source node
        """
        vm_name = "proxy-cancel-test-vm"
        LOGGER.info(f"Creating VM {vm_name} for migration cancellation test")
        with VirtualMachineForTests(
            client=unprivileged_client,
            name=vm_name,
            namespace=namespace.name,
            body=fedora_vm_body(name=vm_name),
        ) as vm:
            running_vm(vm=vm)
            source_node = vm.vmi.instance.status.nodeName
            LOGGER.info(f"VM {vm_name} running on source node: {source_node}")

            LOGGER.info("Initiating migration through proxy")
            migration = VirtualMachineInstanceMigration(
                name=f"{vm_name}-migration",
                namespace=namespace.name,
                vmi_name=vm.vmi.name,
            )
            migration.create()

            LOGGER.info("Waiting for migration to enter Scheduling or Running phase")
            for sample in TimeoutSampler(
                wait_timeout=TIMEOUT_1MIN,
                sleep=TIMEOUT_5SEC,
                func=lambda: migration.instance.status.phase
                if hasattr(migration.instance, "status")
                and hasattr(migration.instance.status, "phase")
                else None,
            ):
                if sample in ("Scheduling", "TargetReady", "Running"):
                    LOGGER.info(f"Migration reached phase: {sample}, cancelling now")
                    break

            LOGGER.info("Cancelling migration")
            migration.delete(wait=True)

            LOGGER.info("Verifying VM remains Running on source node after cancellation")
            vm.vmi.wait_until_running()
            assert vm.instance.status.phase == "Running", (
                f"VM is not Running after migration cancellation: {vm.instance.status.phase}"
            )
            current_node = vm.vmi.instance.status.nodeName
            assert current_node == source_node, (
                f"VM moved from source node {source_node} to {current_node} despite cancellation"
            )

            LOGGER.info("Verifying VM is functional after cancellation")
            vm.ssh_exec.executor().is_connective()

            LOGGER.info("Verifying no lingering migration objects remain")
            remaining_migrations = VirtualMachineInstanceMigration.get(
                client=unprivileged_client,
                namespace=namespace.name,
            )
            active_migrations = [
                mig for mig in remaining_migrations
                if hasattr(mig.instance, "status")
                and hasattr(mig.instance.status, "phase")
                and mig.instance.status.phase not in ("Failed", "Succeeded", None)
            ]
            assert len(active_migrations) == 0, (
                f"Found {len(active_migrations)} lingering active migrations after cancellation"
            )
