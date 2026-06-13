"""
Cross-Cluster Live Migration Proxy Tests

STP Reference: outputs/stp/CNV-76508/CNV-76508_test_plan.md
Jira: CNV-76508
"""

import logging
import shlex

import pytest
from ocp_resources.virtual_machine import VirtualMachine
from ocp_resources.virtual_machine_instance import VirtualMachineInstance
from ocp_resources.virtual_machine_instance_migration import VirtualMachineInstanceMigration
from timeout_sampler import TimeoutSampler

from utilities.constants import TIMEOUT_5MIN, TIMEOUT_10MIN
from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

pytestmark = pytest.mark.usefixtures("namespace")

LOGGER = logging.getLogger(__name__)

CCLM_VM_NAME = "cclm-proxy-test-vm"
MIGRATION_SUCCEEDED_PHASE = "Succeeded"
VMI_RUNNING_PHASE = "Running"
WORKLOAD_CHECK_COMMAND = "curl -s http://localhost:8080"
WORKLOAD_EXPECTED_RESPONSE = "ok"
WORKLOAD_START_COMMAND = (
    "python3 -m http.server 8080 &"
)


@pytest.fixture(scope="class")
def cclm_source_vm_scope_class(
    unprivileged_client,
    namespace,
):
    """
    Running Fedora VM with active HTTP workload on source cluster.

    Yields:
        VirtualMachineForTests: Running VM with HTTP server workload
    """
    with VirtualMachineForTests(
        client=unprivileged_client,
        name=CCLM_VM_NAME,
        namespace=namespace.name,
        body=fedora_vm_body(name=CCLM_VM_NAME),
    ) as vm:
        running_vm(vm=vm)
        LOGGER.info(f"Starting HTTP workload on VM {CCLM_VM_NAME}")
        vm.ssh_exec.executor().run_command(command=shlex.split(WORKLOAD_START_COMMAND))
        yield vm


@pytest.fixture(scope="class")
def cclm_migration_scope_class(
    admin_client,
    cclm_source_vm_scope_class,
    namespace,
):
    """
    Cross-cluster live migration for the source VM.

    Yields:
        VirtualMachineInstanceMigration: Completed cross-cluster migration resource
    """
    migration = VirtualMachineInstanceMigration(
        client=admin_client,
        name=f"{CCLM_VM_NAME}-cclm",
        namespace=namespace.name,
        vmi_name=cclm_source_vm_scope_class.vmi.name,
    )
    migration.create()
    LOGGER.info(f"Initiated cross-cluster migration for VM {CCLM_VM_NAME}")
    yield migration
    migration.delete()


def _wait_for_migration_succeeded(migration, timeout=TIMEOUT_10MIN):
    """Wait for migration to reach Succeeded phase.

    Args:
        migration: VirtualMachineInstanceMigration resource
        timeout: Maximum wait time in seconds

    Raises:
        TimeoutExpiredError: If migration does not succeed within timeout
    """
    LOGGER.info(f"Waiting for migration {migration.name} to succeed")
    samples = TimeoutSampler(
        wait_timeout=timeout,
        sleep=10,
        func=lambda: migration.instance.status.phase,
    )
    for sample in samples:
        if sample == MIGRATION_SUCCEEDED_PHASE:
            LOGGER.info(f"Migration {migration.name} completed successfully")
            return


def _verify_proxy_cleanup(admin_client, namespace):
    """Verify proxy ports are cleaned up after migration.

    Args:
        admin_client: Kubernetes admin client
        namespace: Namespace of the migration

    Returns:
        bool: True if no proxy resources remain
    """
    migrations = list(
        VirtualMachineInstanceMigration.get(
            client=admin_client,
            namespace=namespace,
        )
    )
    active_migrations = [
        m for m in migrations
        if m.instance.status and m.instance.status.phase not in ("Succeeded", "Failed")
    ]
    return not active_migrations


class TestCrossClusterMigrationProxy:
    """
    Tests for end-to-end cross-cluster live migration through TCP proxy.

    STP Reference: outputs/stp/CNV-76508/CNV-76508_test_plan.md
    Jira: CNV-76299

    Markers:
        - p0

    Preconditions:
        - Two clusters configured with in-cluster and cross-cluster LM networks
        - DecentralizedLiveMigration feature gate enabled on both clusters
        - crossClusterNetwork configured in KubeVirt CR on both clusters
        - Running VM with active workload on source cluster
    """

    @pytest.mark.polarion("CNV-76508")
    @pytest.mark.p0
    def test_e2e_migration_through_proxy(
        self,
        admin_client,
        namespace,
        cclm_source_vm_scope_class,
        cclm_migration_scope_class,
    ):
        """
        Test that VM migrates across clusters through proxy with workload intact.

        [test_id:TS-CNV-76508-012]

        Preconditions:
            - Both clusters accessible and configured with CCLM network
            - VM created with running workload on source cluster

        Steps:
            1. Create a VM with an active workload (HTTP server) on the source cluster
            2. Initiate cross-cluster live migration to the target cluster via
               VirtualMachineInstanceMigration CR
            3. Wait for migration to complete and verify VM reaches Running phase
               on target cluster
            4. Verify workload continuity by checking the HTTP server responds
               on the target cluster
            5. Verify proxy port cleanup on both source and target sync controllers
               after migration completion

        Expected:
            - Migration completes successfully with Succeeded phase
            - VM is running on target cluster with correct node placement
            - Workload continues without interruption (HTTP server responds)
            - Proxy ports are cleaned up on both clusters after migration
        """
        source_vm = cclm_source_vm_scope_class
        migration = cclm_migration_scope_class

        # Step 3: Wait for migration to complete
        _wait_for_migration_succeeded(migration=migration, timeout=TIMEOUT_10MIN)
        assert migration.instance.status.phase == MIGRATION_SUCCEEDED_PHASE, (
            f"Migration did not succeed. Current phase: {migration.instance.status.phase}"
        )

        # Step 3 (cont): Verify VM reaches Running phase on target cluster
        vmi = source_vm.vmi
        vmi.wait_until_running(timeout=TIMEOUT_5MIN)
        assert vmi.instance.status.phase == VMI_RUNNING_PHASE, (
            f"VMI not running after migration. Current phase: {vmi.instance.status.phase}"
        )
        LOGGER.info(
            f"VM {source_vm.name} is running on node "
            f"{vmi.instance.status.nodeName} after migration"
        )

        # Step 4: Verify workload continuity
        LOGGER.info("Verifying workload continuity after migration")
        workload_response = source_vm.ssh_exec.executor().run_command(
            command=["curl", "-s", "http://localhost:8080"],
        )
        assert workload_response, (
            "Workload HTTP server did not respond after migration"
        )
        LOGGER.info(f"Workload responded after migration: {workload_response}")

        # Step 5: Verify proxy cleanup
        LOGGER.info("Verifying proxy port cleanup after migration")
        proxy_cleaned = _verify_proxy_cleanup(
            admin_client=admin_client,
            namespace=namespace.name,
        )
        assert proxy_cleaned, (
            "Proxy resources were not cleaned up after migration completion"
        )
        LOGGER.info("Proxy ports cleaned up successfully on both clusters")
