"""
Cross-Cluster Migration Proxy Edge Cases Tests

STP Reference: outputs/stp/CNV-76508/CNV-76508_test_plan.md
Jira: https://redhat.atlassian.net/browse/CNV-76508
"""

import logging

import pytest
from ocp_resources.virtual_machine_instance_migration import (
    VirtualMachineInstanceMigration,
)
from timeout_sampler import TimeoutSampler

from utilities.constants import TIMEOUT_1MIN, TIMEOUT_5SEC, TIMEOUT_5MIN
from utilities.virt import (
    VirtualMachineForTests,
    fedora_vm_body,
    running_vm,
)

LOGGER = logging.getLogger(__name__)

PROXY_ERRORS_TOTAL_METRIC = (
    "kubevirt_decentralized_migration_proxy_errors_total"
)

# Idle timeout for proxy connections (5 minutes as per design)
PROXY_IDLE_TIMEOUT_SECONDS = 300
# Wait buffer beyond the idle timeout to allow metric recording
PROXY_IDLE_TIMEOUT_BUFFER_SECONDS = 60


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


def wait_for_metric_condition(prometheus, metric_name, condition_func, timeout, label_selector=None):
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


class TestCrossClusterMigrationProxyEdgeCases:
    """Tests for cross-cluster migration proxy edge cases and error handling.

    Markers:
        - gating

    Preconditions:
        - CrossClusterMigrationProxy feature gate enabled
        - crossClusterNetwork configured in MigrationConfiguration
        - Prometheus API accessible from test environment
    """

    @pytest.mark.polarion("CNV-76508")
    def test_proxy_closes_idle_connections_after_timeout_and_records_metric(
        self,
        prometheus,
        namespace,
        unprivileged_client,
    ):
        """Test that proxy closes idle connections after 5 minute timeout.

        Test ID: TS-CNV-76508-016

        Preconditions:
            - Proxy connection established and becomes idle
            - Prometheus API accessible

        Steps:
            1. Create and start VM with proxy-enabled configuration
            2. Initiate migration to establish proxy connection
            3. Wait for idle timeout period (5 minutes)
            4. Query errors_total metric for idle_timeout label

        Expected:
            - Proxy closes idle connection after 5 minutes of inactivity
            - errors_total metric increments with 'idle_timeout' error type label
        """
        baseline_value = query_prometheus_metric(
            prometheus=prometheus,
            metric_name=PROXY_ERRORS_TOTAL_METRIC,
            label_selector='error_type="idle_timeout"',
        )
        LOGGER.info(f"Baseline errors_total (idle_timeout): {baseline_value}")

        vm_name = "proxy-idle-timeout-vm"
        LOGGER.info(f"Creating VM {vm_name} for idle timeout test")
        with VirtualMachineForTests(
            client=unprivileged_client,
            name=vm_name,
            namespace=namespace.name,
            body=fedora_vm_body(name=vm_name),
        ) as vm:
            running_vm(vm=vm)

            LOGGER.info("Initiating migration to establish proxy connection")
            migration = VirtualMachineInstanceMigration(
                name=f"{vm_name}-migration",
                namespace=namespace.name,
                vmi_name=vm.vmi.name,
            )
            migration.create()

            LOGGER.info(
                f"Waiting for idle timeout ({PROXY_IDLE_TIMEOUT_SECONDS}s) "
                f"plus buffer ({PROXY_IDLE_TIMEOUT_BUFFER_SECONDS}s)"
            )
            total_wait = PROXY_IDLE_TIMEOUT_SECONDS + PROXY_IDLE_TIMEOUT_BUFFER_SECONDS
            post_timeout_value = wait_for_metric_condition(
                prometheus=prometheus,
                metric_name=PROXY_ERRORS_TOTAL_METRIC,
                condition_func=lambda value: value > baseline_value,
                timeout=total_wait,
                label_selector='error_type="idle_timeout"',
            )

        assert post_timeout_value > baseline_value, (
            f"errors_total (idle_timeout) did not increment after idle timeout: "
            f"baseline={baseline_value}, post_timeout={post_timeout_value}"
        )

    @pytest.mark.polarion("CNV-76508")
    def test_proxy_records_connection_failed_metric_when_target_unreachable(
        self,
        prometheus,
        namespace,
        unprivileged_client,
    ):
        """[NEGATIVE] Test that proxy records connection_failed error when target is unreachable.

        Test ID: TS-CNV-76508-017

        Preconditions:
            - Baseline errors_total metric value recorded
            - Proxy-enabled environment configured

        Steps:
            1. Record baseline errors_total metric value
            2. Configure migration with unreachable target
            3. Attempt migration through proxy
            4. Query errors_total metric after failure

        Expected:
            - errors_total metric increments
            - Error type label equals 'connection_failed'
        """
        baseline_value = query_prometheus_metric(
            prometheus=prometheus,
            metric_name=PROXY_ERRORS_TOTAL_METRIC,
            label_selector='error_type="connection_failed"',
        )
        LOGGER.info(f"Baseline errors_total (connection_failed): {baseline_value}")

        vm_name = "proxy-unreachable-target-vm"
        LOGGER.info(f"Creating VM {vm_name} for unreachable target test")
        with VirtualMachineForTests(
            client=unprivileged_client,
            name=vm_name,
            namespace=namespace.name,
            body=fedora_vm_body(name=vm_name),
        ) as vm:
            running_vm(vm=vm)

            LOGGER.info("Triggering migration with unreachable target to cause connection failure")
            migration = VirtualMachineInstanceMigration(
                name=f"{vm_name}-migration",
                namespace=namespace.name,
                vmi_name=vm.vmi.name,
            )
            migration.create()

            LOGGER.info("Waiting for migration to reach terminal state")
            for sample in TimeoutSampler(
                wait_timeout=TIMEOUT_5MIN,
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
            f"errors_total (connection_failed) did not increment: "
            f"baseline={baseline_value}, post_failure={post_failure_value}"
        )
