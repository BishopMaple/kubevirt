"""
Live Update NAD Reference - Upgrade Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""


def test_existing_vms_work_after_fg_enablement():
    """
    Test that existing VMs work correctly after upgrading to version with LiveUpdateNADRef FG.

    Preconditions:
        - LiveUpdateNADRef feature gate initially disabled
        - Two bridge NADs (NAD-A and NAD-B) in test namespace
        - Running VM with secondary interface on NAD-A (created before FG enablement)
        - LiveUpdateNADRef feature gate enabled

    Steps:
        1. Change NAD reference on pre-existing VM from NAD-A to NAD-B
        2. Wait for migration to complete

    Expected:
        - Migration succeeds on pre-existing VM
    """
    pass


test_existing_vms_work_after_fg_enablement.__test__ = False
