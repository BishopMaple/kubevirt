"""
Shared fixtures for CNV-76508 Cross-Cluster Live Migration Proxy tests.

These fixtures provide common setup for CCLM proxy test scenarios.
"""

import logging

import pytest

LOGGER = logging.getLogger(__name__)


@pytest.fixture(scope="session")
def cclm_feature_gate_enabled(admin_client):
    """
    Verify DecentralizedLiveMigration feature gate is enabled.

    This fixture validates that the required feature gate is active
    before any CCLM tests run.

    Yields:
        bool: True if feature gate is enabled
    """
    from ocp_resources.kubevirt import KubeVirt

    kubevirt_resources = list(KubeVirt.get(client=admin_client))
    assert kubevirt_resources, "No KubeVirt CR found in cluster"
    kubevirt_cr = kubevirt_resources[0]
    feature_gates = (
        kubevirt_cr.instance.spec.get("configuration", {})
        .get("developerConfiguration", {})
        .get("featureGates", [])
    )
    assert "DecentralizedLiveMigration" in feature_gates, (
        "DecentralizedLiveMigration feature gate is not enabled in KubeVirt CR. "
        f"Current feature gates: {feature_gates}"
    )
    LOGGER.info("DecentralizedLiveMigration feature gate is enabled")
    yield True


@pytest.fixture(scope="session")
def cross_cluster_network_configured(admin_client, cclm_feature_gate_enabled):
    """
    Verify crossClusterNetwork is configured in KubeVirt CR.

    Yields:
        str: The cross-cluster network name
    """
    from ocp_resources.kubevirt import KubeVirt

    kubevirt_resources = list(KubeVirt.get(client=admin_client))
    kubevirt_cr = kubevirt_resources[0]
    migration_config = (
        kubevirt_cr.instance.spec.get("configuration", {})
        .get("migrations", {})
    )
    cross_cluster_network = migration_config.get("crossClusterNetwork")
    assert cross_cluster_network, (
        "crossClusterNetwork is not configured in KubeVirt CR MigrationConfiguration"
    )
    LOGGER.info(f"crossClusterNetwork configured: {cross_cluster_network}")
    yield cross_cluster_network
