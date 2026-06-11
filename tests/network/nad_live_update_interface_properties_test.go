/*
 * This file is part of the kubevirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright The KubeVirt Authors.
 *
 */

package network

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/pkg/apimachinery/patch"
	"kubevirt.io/kubevirt/pkg/libvmi"
	"kubevirt.io/kubevirt/pkg/pointer"

	"kubevirt.io/kubevirt/tests/console"
	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/framework/matcher"
	"kubevirt.io/kubevirt/tests/libkubevirt"
	"kubevirt.io/kubevirt/tests/libkubevirt/config"
	"kubevirt.io/kubevirt/tests/libnet"
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/testsuite"
)

/*
Live Update NAD Reference Tests — Interface Property Preservation

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Scenarios covered:
  - TS-CNV-72329-004: Guest interface name and MAC preserved after NAD swap (Tier 1, P1)
*/

var _ = Describe(SIG("[CNV-72329] Live Update NAD Reference - Interface Properties", decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		sourceNADName   = "source-nad-props"
		targetNADName   = "target-nad-props"
		sourceBridge    = "br-src-props"
		targetBridge    = "br-tgt-props"
		pollingInterval = 2 * time.Second
		timeoutInterval = 5 * time.Minute
	)

	var testNamespace string

	BeforeEach(func() {
		virtClient := kubevirt.Client()
		config.EnableFeatureGate("LiveUpdateNADRef")

		updateStrategy := &v1.KubeVirtWorkloadUpdateStrategy{
			WorkloadUpdateMethods: []v1.WorkloadUpdateMethod{v1.WorkloadUpdateMethodLiveMigrate},
		}
		rolloutStrategy := pointer.P(v1.VMRolloutStrategyLiveUpdate)
		err := config.RegisterKubevirtConfigChange(
			config.WithWorkloadUpdateStrategy(updateStrategy),
			config.WithVMRolloutStrategy(rolloutStrategy),
		)
		Expect(err).ToNot(HaveOccurred())

		currentKv := libkubevirt.GetCurrentKv(virtClient)
		config.WaitForConfigToBePropagatedToComponent(
			"kubevirt.io=virt-controller",
			currentKv.ResourceVersion,
			config.ExpectResourceVersionToBeLessEqualThanConfigVersion,
			time.Minute)

		testNamespace = testsuite.GetTestNamespace(nil)

		By("Creating source bridge NAD")
		sourceNAD := libnet.NewBridgeNetAttachDef(sourceNADName, sourceBridge)
		_, err = libnet.CreateNetAttachDef(context.Background(), testNamespace, sourceNAD)
		Expect(err).ToNot(HaveOccurred())

		By("Creating target bridge NAD")
		targetNAD := libnet.NewBridgeNetAttachDef(targetNADName, targetBridge)
		_, err = libnet.CreateNetAttachDef(context.Background(), testNamespace, targetNAD)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("Guest interface name and MAC preserved after NAD swap", func() {
		It("[test_id:TS-CNV-72329-004] should preserve guest interface name and MAC address after NAD swap", func() {
			By("Creating VM with secondary interface on source NAD")
			vmi := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("net1")),
				libvmi.WithNetwork(libvmi.MultusNetwork("net1", sourceNADName)),
			)
			vm := libvmi.NewVirtualMachine(vmi, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err := kubevirt.Client().VirtualMachine(testNamespace).Create(context.Background(), vm, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for VMI to be ready")
			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))

			By("Recording guest interface name and MAC address before NAD swap")
			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			var originalIfaceName, originalMAC string
			for _, iface := range vmi.Status.Interfaces {
				if iface.Name == "net1" {
					originalIfaceName = iface.InterfaceName
					originalMAC = iface.MAC
					break
				}
			}
			Expect(originalIfaceName).ToNot(BeEmpty(), "should find net1 interface name in VMI status")
			Expect(originalMAC).ToNot(BeEmpty(), "should find net1 interface MAC in VMI status")
			By(fmt.Sprintf("Recorded interface name: %s, MAC: %s", originalIfaceName, originalMAC))

			By("Patching VM to change NAD reference to target NAD")
			patchPayload, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNADName),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchPayload, metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for migration to complete")
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))

			By("Verifying guest interface name and MAC are preserved after NAD swap")
			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			var postSwapIfaceName, postSwapMAC string
			for _, iface := range vmi.Status.Interfaces {
				if iface.Name == "net1" {
					postSwapIfaceName = iface.InterfaceName
					postSwapMAC = iface.MAC
					break
				}
			}

			Expect(postSwapIfaceName).To(Equal(originalIfaceName),
				"guest interface name should be preserved after NAD swap")
			Expect(strings.EqualFold(postSwapMAC, originalMAC)).To(BeTrue(),
				fmt.Sprintf("MAC address should be preserved: expected %s, got %s", originalMAC, postSwapMAC))
		})
	})
}))
