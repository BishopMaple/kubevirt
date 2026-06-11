"""
NAD Reference Live Update - Hotplug Integration Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
"""


class TestNadSwapWithHotplugOperations:
    """
    Tests for NAD swap coexistence with hotplug/hotunplug operations.

    Markers:
        - tier2

    Preconditions:
        - LiveUpdateNADRef feature gate enabled
        - VM rollout strategy set to LiveUpdate with LiveMigrate workload update
        - Source NAD, target NAD, and hotplug NAD created with distinct bridges
        - VM running with secondary interface on source NAD
    """

    __test__ = False

    def test_nad_swap_with_concurrent_interface_hotplug(self):
        """
        Test that NAD swap works alongside interface hotplug operation.

        Steps:
            1. Patch VM to add new interface (hotplug) and change existing NAD reference simultaneously
            2. Wait for migration and hotplug to complete

        Expected:
            - VM has the new hotplugged interface AND the swapped NAD reference
            - Both operations succeed without conflict
        """
        pass

    def test_nad_swap_with_concurrent_interface_hotunplug(self):
        """
        Test that NAD swap works alongside interface hotunplug operation.

        Preconditions:
            - VM running with 2 secondary interfaces on separate source NADs

        Steps:
            1. Patch VM to remove one interface and change NAD reference on the other simultaneously
            2. Wait for operations to complete

        Expected:
            - Removed interface is gone from VMI spec
            - Remaining interface has the swapped NAD reference
        """
        pass
