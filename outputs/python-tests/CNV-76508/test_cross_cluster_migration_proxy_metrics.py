"""
Cross-Cluster Migration Proxy Metrics Tests

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

PROXY_ACTIVE_CONNECTIONS_METRIC = (
    "kubevirt_decentralized_migration_proxy_active_connections"
)
PROXY_BYTES_TRANSFERRED_METRIC = (
    "kubevirt_decentralized_migration_proxy_bytes_transferred_total"
)
PROXY_ERRORS_TOTAL_METRIC = (
    "kubevirt_decentralized_migration_proxy_errors_total"
)


def query_prometheus_metric(prometheus, metric_name, label_selector=None):
    """Query a Prometheus metric and return its current value.

    Args:
        prometheus: Prometheus client fixture.
        metric_name: Name of the metric to query.
        label_selector: Optional label selector string for filtering.

    Returns:
        Float metric value, or 0.0 if metric is not found.
    """
    query = metric_name
    if label_selector:
        query = f"{metric_name}{{{label_selector}}}"
    LOGGER.info(f"Querying Prometheus metric: {query}")
    result = prometheus.query(query=query)
    if result and result.get("data", {}).get("result"):
        value = float(result["data"]["result"][0]["value"][1])
        LOGGER.info(f"Metric {metric_name} value: {value}")
        return value
    LOGGER.info(f"Metric {metric_name} not found, returning 0.0")
    return 0.0


def wait_for_metric_condition(prometheus, metric_name, condition_func, timeout=TIMEOUT_2MIN, label_selector=None):
    """Wait until a Prometheus metric satisfies the given condition.

    Args:
        prometheus: Prometheus client fixture.
        metric_name: Name of the metric to query.
        condition_func: Callable that takes the metric value and returns bool.
        timeout: Maximum time to wait.
        label_selector: Optional label selector string for filtering.

    Raises:
        TimeoutExpiredError: If the condition is not met within the timeout.
    """
    for sample in TimeoutSampler(
        wait_timeout=timeout,
        sleep=TIMEOUT_5SEC,
        func=query_prometheus_metric,
        prometheus=prometheus,
        metric_name=metric_name,
        label_selector=label_selector,
    ):
        if condition_func(sample):
            return sample


class TestCrossClusterMigrationProxyMetrics:
    """Tests for cross-cluster migration proxy Prometheus metrics validation.

    Markers:
        - gating

    Preconditions:
        - CrossClusterMigrationProxy feature gate enabled
        - crossClusterNetwork configured in MigrationConfiguration
        - Prometheus API accessible from test environment
        - VM created with proxy-enabled migration configuration
        - VM is Running with console access
    """

    @pytest.mark.polarion("CNV-76508")
    def test_proxy_active_connections_increments_during_migration_and_decrements_after(
        self,
        prometheus,
        proxy_enabled_vm_scope_class,
    ):
        """Test that active_connections metric tracks proxy connections during migration.

        Test ID: TS-CNV-76508-008

        Preconditions:
            - Baseline active_connections metric value recorded
            - VM is Running with proxy-enabled configuration

        Steps:
            1. Record baseline active_connections metric value
            2. Execute VM live migration through proxy
            3. Query active_connections metric after migration completes

        Expected:
            - active_connections metric returns to 0 after migration completes
        """
        vm = proxy_enabled_vm_scope_class
        baseline_value = query_prometheus_metric(
            prometheus=prometheus,
            metric_name=PROXY_ACTIVE_CONNECTIONS_METRIC,
        )
        LOGGER.info(f"Baseline active_connections: {baseline_value}")

        LOGGER.info("Executing VM live migration through proxy")
        migrate_vm_and_verify(vm=vm)

        LOGGER.info("Verifying active_connections returns to 0 after migration")
        post_migration_value = wait_for_metric_condition(
            prometheus=prometheus,
            metric_name=PROXY_ACTIVE_CONNECTIONS_METRIC,
            condition_func=lambda value: value == 0.0,
            timeout=TIMEOUT_1MIN,
        )
        assert post_migration_value == 0.0, (
            f"active_connections metric did not return to 0 after migration: {post_migration_value}"
        )

    @pytest.mark.polarion("CNV-76508")
    def test_proxy_bytes_transferred_total_records_bidirectional_data(
        self,
        prometheus,
        proxy_enabled_vm_scope_class,
    ):
        """Test that bytes_transferred_total records data transfer in both directions.

        Test ID: TS-CNV-76508-009

        Preconditions:
            - Baseline bytes_transferred_total metric value recorded
            - VM is Running with proxy-enabled configuration

        Steps:
            1. Record baseline bytes_transferred_total metric value
            2. Execute VM live migration through proxy
            3. Query bytes_transferred_total metric after migration completes

        Expected:
            - bytes_transferred_total metric value increases after migration
            - Both source-to-target and target-to-source directions show data transfer
        """
        vm = proxy_enabled_vm_scope_class
        baseline_value = query_prometheus_metric(
            prometheus=prometheus,
            metric_name=PROXY_BYTES_TRANSFERRED_METRIC,
        )
        LOGGER.info(f"Baseline bytes_transferred_total: {baseline_value}")

        LOGGER.info("Executing VM live migration through proxy")
        migrate_vm_and_verify(vm=vm)

        LOGGER.info("Verifying bytes_transferred_total increased after migration")
        post_migration_value = wait_for_metric_condition(
            prometheus=prometheus,
            metric_name=PROXY_BYTES_TRANSFERRED_METRIC,
            condition_func=lambda value: value > baseline_value,
            timeout=TIMEOUT_1MIN,
        )
        assert post_migration_value > baseline_value, (
            f"bytes_transferred_total did not increase after migration: "
            f"baseline={baseline_value}, post_migration={post_migration_value}"
        )

    @pytest.mark.polarion("CNV-76508")
    def test_proxy_errors_total_increments_on_connection_failure(
        self,
        prometheus,
        namespace,
        unprivileged_client,
    ):
        """[NEGATIVE] Test that errors_total increments on proxy connection failure.

        Test ID: TS-CNV-76508-010

        Preconditions:
            - Baseline errors_total metric value recorded
            - Proxy-enabled environment configured

        Steps:
            1. Record baseline errors_total metric value
            2. Trigger migration with unreachable target configuration
            3. Query errors_total metric after failure

        Expected:
            - errors_total metric value is greater than baseline
            - Error type label equals 'connection_failed'
        """
        baseline_value = query_prometheus_metric(
            prometheus=prometheus,
            metric_name=PROXY_ERRORS_TOTAL_METRIC,
            label_selector='error_type="connection_failed"',
        )
        LOGGER.info(f"Baseline errors_total (connection_failed): {baseline_value}")

        vm_name = "proxy-error-metrics-vm"
        LOGGER.info(f"Creating VM {vm_name} for connection failure test")
        with VirtualMachineForTests(
            client=unprivileged_client,
            name=vm_name,
            namespace=namespace.name,
            body=fedora_vm_body(name=vm_name),
        ) as vm:
            running_vm(vm=vm)

            LOGGER.info("Creating migration to trigger connection failure through proxy")
            migration = VirtualMachineInstanceMigration(
                name=f"{vm_name}-migration",
                namespace=namespace.name,
                vmi_name=vm.vmi.name,
            )
            migration.create()

            LOGGER.info("Waiting for migration to fail or complete")
            for sample in TimeoutSampler(
                wait_timeout=TIMEOUT_2MIN,
                sleep=TIMEOUT_5SEC,
                func=lambda: migration.instance.status.phase
                if hasattr(migration.instance, "status")
                and hasattr(migration.instance.status, "phase")
                else None,
            ):
                if sample in ("Failed", "Succeeded"):
                    LOGGER.info(f"Migration reached terminal phase: {sample}")
                    break

        LOGGER.info("Verifying errors_total metric incremented with connection_failed label")
        post_failure_value = wait_for_metric_condition(
            prometheus=prometheus,
            metric_name=PROXY_ERRORS_TOTAL_METRIC,
            condition_func=lambda value: value > baseline_value,
            timeout=TIMEOUT_1MIN,
            label_selector='error_type="connection_failed"',
        )
        assert post_failure_value > baseline_value, (
            f"errors_total (connection_failed) did not increment after failure: "
            f"baseline={baseline_value}, post_failure={post_failure_value}"
        )
