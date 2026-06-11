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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/pkg/apimachinery/patch"
	"kubevirt.io/kubevirt/pkg/libvmi"
	"kubevirt.io/kubevirt/pkg/pointer"

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
Live Update NAD Reference Tests — Core Functionality

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Scenarios covered:
  - TS-CNV-72329-001: NAD reference update on running VM without restart (Tier 1, P0)
  - TS-CNV-72329-002: Auto-migration triggered after NAD reference change (Tier 1, P0)
*/

var _ = Describe(SIG("[CNV-72329] Live Update NAD Reference - Core", decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		sourceNADName   = "source-nad"
		targetNADName   = "target-nad"
		sourceBridge    = "br-source"
		targetBridge    = "br-target"
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

	Context("NAD reference update on running VM without restart", func() {
		It("[test_id:TS-CNV-72329-001] should update NAD reference without requiring VM restart", func() {
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

			By("Patching VM to change NAD reference to target NAD")
			patchPayload, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNADName),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchPayload, metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for migration to complete (NAD change triggers auto-migration)")
			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))

			By("Verifying VM remains running")
			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.BeRunning())

			By("Verifying RestartRequired condition is NOT set")
			Consistently(func(g Gomega) {
				vm, err = kubevirt.Client().VirtualMachine(testNamespace).Get(
					context.Background(), vm.Name, metav1.GetOptions{})
				g.Expect(err).ToNot(HaveOccurred())
				for _, condition := range vm.Status.Conditions {
					g.Expect(condition.Type).ToNot(Equal(v1.VirtualMachineRestartRequired))
				}
			}, 10*time.Second, pollingInterval).Should(Succeed())

			By("Verifying VM spec reflects updated NAD reference")
			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			for _, net := range vm.Spec.Template.Spec.Networks {
				if net.Name == "net1" && net.Multus != nil {
					Expect(net.Multus.NetworkName).To(Equal(targetNADName),
						"network should reference target NAD after update")
				}
			}
		})
	})

	Context("Auto-migration triggered after NAD reference change", func() {
		It("[test_id:TS-CNV-72329-002] should trigger auto-migration after NAD reference change", func() {
			By("Creating VM with secondary interface on source NAD")
			vmi := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("net1")),
				libvmi.WithNetwork(libvmi.MultusNetwork("net1", sourceNADName)),
			)
			vm := libvmi.NewVirtualMachine(vmi, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err := kubevirt.Client().VirtualMachine(testNamespace).Create(context.Background(), vm, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for VMI to be ready and recording original node")
			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))

			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			originalNode := vmi.Status.NodeName
			Expect(originalNode).ToNot(BeEmpty(), "VMI should be scheduled to a node")

			By("Patching VM NAD reference to target NAD")
			patchPayload, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNADName),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchPayload, metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for migration condition to appear")
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))

			By("Waiting for migration condition to resolve (migration complete)")
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))

			By("Verifying VM landed on different node")
			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(vmi.Status.NodeName).ToNot(Equal(originalNode),
				"VMI should have migrated to a different node after NAD reference change")
		})
	})
}))
