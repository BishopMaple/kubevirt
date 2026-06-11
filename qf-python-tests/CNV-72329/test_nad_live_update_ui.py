"""
NAD Reference Live Update - UI Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-82742
PR: https://github.com/kubevirt/kubevirt/pull/16412
"""

import logging

import pytest

from utilities.virt import VirtualMachineForTests, fedora_vm_body, running_vm

LOGGER = logging.getLogger(__name__)

pytestmark = pytest.mark.usefixtures("namespace")


@pytest.mark.polarion("CNV-82742")
class TestNadLiveUpdateUi:
    """
    Tests for NAD reference live update UI behavior.

    Markers:
        - polarion

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - VM running with secondary NAD
        - OpenShift console accessible for UI testing
    """

    def test_ui_nad_reference_edit_shows_pending_changes(
        self,
        vm_with_source_nad_scope_class,
        target_nad_scope_class,
    ):
        """
        Test that UI displays pending changes label when NAD reference is edited.

        Scenario: TS-CNV-72329-021

        Preconditions:
            - VM running with secondary NAD
            - OpenShift console accessible

        Steps:
            1. Patch VM spec to change NAD reference (simulating UI edit)
            2. Verify VM shows pending changes condition before migration

        Expected:
            - Pending changes label appears after NAD edit
        """
        vm = vm_with_source_nad_scope_class
        target_nad = target_nad_scope_class

        LOGGER.info(f"Patching VM {vm.name} NAD reference to simulate UI edit")
        patch_data = [
            {
                "op": "replace",
                "path": "/spec/template/spec/networks/0/multus/networkName",
                "value": target_nad.name,
            },
        ]
        vm.instance.patch(
            body=patch_data,
            content_type="application/json-patch+json",
        )

        LOGGER.info("Checking for pending changes condition on VM")
        vm_instance = vm.instance
        vm_status = vm_instance.status
        conditions = vm_status.get("conditions", [])

        pending_change_detected = False
        for condition in conditions:
            condition_type = condition.get("type", "")
            if "RestartRequired" in condition_type or "MigrationRequired" in condition_type:
                pending_change_detected = True
                LOGGER.info(
                    f"Pending change condition detected: type={condition_type}, "
                    f"status={condition.get('status')}, reason={condition.get('reason')}"
                )
                break

        assert pending_change_detected, (
            f"No pending changes condition found on VM {vm.name} after NAD edit. "
            f"Conditions: {[c.get('type') for c in conditions]}"
        )
