package network

import (
	. "github.com/onsi/ginkgo/v2"
)

/*
NAD Reference Live Update - Pod Network Preservation (Regression)

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
Regression: PR #17315 / #17373
*/

var _ = Describe("[CNV-72329] NAD Reference Live Update - Pod Network Preservation", decorators.SigNetwork, Serial, func() {
	/*
	Markers:
	    - sig-network
	    - serial

	Preconditions:
	    - OCP 4.22+ with OpenShift Virtualization 4.22+
	    - LiveUpdateNADRef feature gate enabled (Beta state)
	*/

	Context("pod network preservation with LiveUpdateNADRef FG enabled", Ordered, func() {
		/*
		Preconditions:
		    - LiveUpdateNADRef feature gate enabled (default in Beta)
		*/

		/*
		Preconditions:
		    - LiveUpdateNADRef feature gate enabled
		    - VM spec does not define explicit interfaces or networks arrays

		Steps:
		    1. Create VM with no explicit interfaces or networks (auto-injected pod network only)
		    2. Wait for VM to reach Running state

		Expected:
		    - VMI has auto-injected pod network with masquerade binding
		    - VM is accessible via pod network (console login succeeds)
		*/
		PendingIt("[test_id:TS-CNV-72329-006] should preserve auto-injected pod network when VM has no explicit interfaces", func() {
			Skip("Phase 1: Design only - awaiting implementation")
		})
	})
})
