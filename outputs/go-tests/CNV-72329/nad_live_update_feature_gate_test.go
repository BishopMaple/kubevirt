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
Live Update NAD Reference Tests — Feature Gate and Boundary Validation

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Scenarios covered:
  - TS-CNV-72329-007: Feature gate controls restart vs migration behavior (Tier 1, P1)
  - TS-CNV-72329-008: Non-NAD network changes still require restart (Tier 1, P1)
*/

var _ = Describe(SIG("[CNV-72329] Live Update NAD Reference - Feature Gate", decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		pollingInterval = 2 * time.Second
		timeoutInterval = 5 * time.Minute
	)

	var testNamespace string

	Context("Feature gate controls restart vs migration behavior", func() {
		It("[test_id:TS-CNV-72329-007] should require restart when feature gate disabled and trigger migration when enabled", func() {
			By("Disabling LiveUpdateNADRef feature gate")
			config.DisableFeatureGate("LiveUpdateNADRef")

			virtClient := kubevirt.Client()

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

			By("Creating source and target bridge NADs")
			sourceNAD := libnet.NewBridgeNetAttachDef("source-nad-fg", "br-src-fg")
			_, err = libnet.CreateNetAttachDef(context.Background(), testNamespace, sourceNAD)
			Expect(err).ToNot(HaveOccurred())

			targetNAD := libnet.NewBridgeNetAttachDef("target-nad-fg", "br-tgt-fg")
			_, err = libnet.CreateNetAttachDef(context.Background(), testNamespace, targetNAD)
			Expect(err).ToNot(HaveOccurred())

			By("Creating VM with secondary interface on source NAD")
			vmi := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("net1")),
				libvmi.WithNetwork(libvmi.MultusNetwork("net1", "source-nad-fg")),
			)
			vm := libvmi.NewVirtualMachine(vmi, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Create(context.Background(), vm, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for VMI to be ready")
			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))

			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(console.LoginToFedora(vmi)).To(Succeed())

			By("Patching VM NAD reference with feature gate disabled")
			patchPayload, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", "target-nad-fg"),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchPayload, metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying RestartRequired condition is set when feature gate is disabled")
			Eventually(func() bool {
				vm, err = kubevirt.Client().VirtualMachine(testNamespace).Get(
					context.Background(), vm.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				for _, condition := range vm.Status.Conditions {
					if condition.Type == v1.VirtualMachineRestartRequired {
						return true
					}
				}
				return false
			}, 30*time.Second, pollingInterval).Should(BeTrue(),
				"RestartRequired condition should be set when feature gate is disabled")

			By("Re-enabling LiveUpdateNADRef feature gate")
			config.EnableFeatureGate("LiveUpdateNADRef")

			currentKv = libkubevirt.GetCurrentKv(virtClient)
			config.WaitForConfigToBePropagatedToComponent(
				"kubevirt.io=virt-controller",
				currentKv.ResourceVersion,
				config.ExpectResourceVersionToBeLessEqualThanConfigVersion,
				time.Minute)

			By("Verifying migration is triggered after feature gate is re-enabled")
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))
		})
	})

	Context("Non-NAD network changes still require restart", func() {
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
		})

		It("[test_id:TS-CNV-72329-008] should require restart for non-NAD network field changes", func() {
			By("Creating bridge NAD")
			nad := libnet.NewBridgeNetAttachDef("nad-nonnad", "br-nonnad")
			_, err := libnet.CreateNetAttachDef(context.Background(), testNamespace, nad)
			Expect(err).ToNot(HaveOccurred())

			By("Creating VM with secondary bridge interface")
			vmi := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("net1")),
				libvmi.WithNetwork(libvmi.MultusNetwork("net1", "nad-nonnad")),
			)
			vm := libvmi.NewVirtualMachine(vmi, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Create(context.Background(), vm, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for VMI to be ready")
			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))

			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Patching VM to change interface model type (a non-NAD network field)")
			patchPayload, err := patch.New(
				patch.WithReplace("/spec/template/spec/domain/devices/interfaces/0/model", "e1000e"),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchPayload, metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying RestartRequired condition is set for non-NAD change")
			Eventually(func() bool {
				vm, err = kubevirt.Client().VirtualMachine(testNamespace).Get(
					context.Background(), vm.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				for _, condition := range vm.Status.Conditions {
					if condition.Type == v1.VirtualMachineRestartRequired {
						return true
					}
				}
				return false
			}, 30*time.Second, pollingInterval).Should(BeTrue(),
				"RestartRequired condition should be set for non-NAD network changes")

			By("Verifying no migration is triggered for non-NAD change")
			Consistently(func() bool {
				vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
					context.Background(), vm.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				return vmi.Status.MigrationState == nil
			}, 15*time.Second, pollingInterval).Should(BeTrue(),
				"no migration should be triggered for non-NAD network changes")
		})
	})
}))
