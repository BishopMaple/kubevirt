"""
Test: {feature_title}

Generated from:
- STP: {stp_reference}
- STD: {test_id}

Purpose: {test_objective_what}
Jira: {jira_issue}
"""

import pytest
from ocp_resources.virtual_machine_instance import VirtualMachineInstance
from ocp_resources.namespace import Namespace
from ocp_resources.pod import Pod
from kubernetes.client.rest import ApiException


# ═══════════════════════════════════════════════════════════════════════════
# FIXTURES (from Gherkin Given steps + Background)
# ═══════════════════════════════════════════════════════════════════════════

@pytest.fixture
def test_namespace():
    """Create and cleanup test namespace"""
    namespace = Namespace(name="{test_namespace_name}")
    namespace.create()
    yield namespace
    namespace.delete()


@pytest.fixture
def running_vmi(test_namespace):
    """Create running VMI for test"""
    vmi = VirtualMachineInstance(
        name="{vmi_name}",
        namespace=test_namespace.name,
        body={
            "spec": {
                # ... from STD test_data.vmi_definition
            }
        }
    )
    vmi.create()
    vmi.wait_until_running(timeout=300)
    yield vmi
    vmi.delete()


# ═══════════════════════════════════════════════════════════════════════════
# HELPER FUNCTIONS (from step patterns)
# ═══════════════════════════════════════════════════════════════════════════

def get_virt_launcher_pod_uid(vmi):
    """Capture virt-launcher pod UID"""
    pod = vmi.virt_launcher_pod
    return pod.instance.metadata.uid


# ═══════════════════════════════════════════════════════════════════════════
# TEST FUNCTIONS (from Gherkin Scenarios)
# ═══════════════════════════════════════════════════════════════════════════

@pytest.mark.{tier}
@pytest.mark.{priority}
def test_{scenario_slug}(running_vmi):
    """
    Test: {scenario_title}

    STD: {test_id}
    Jira: {jira_issue}
    Assertion: {assertion_ids}
    """
    vmi = running_vmi

    # GIVEN: {given_steps} (from fixture)
    assert vmi.status.phase == "Running"

    # WHEN: {when_steps}
    # ... actions here

    # THEN: {then_steps}
    # ... assertions here
