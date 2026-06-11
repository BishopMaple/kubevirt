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
Live Update NAD Reference Tests — Negative / Error Handling

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Scenarios covered:
  - TS-CNV-72329-010: [NEGATIVE] Non-existent NAD reference handling (Tier 1, P1)
*/

var _ = Describe(SIG("[CNV-72329] Live Update NAD Reference - Negative", Serial, func() {
	const (
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
	})

	Context("[NEGATIVE] Non-existent NAD reference handling", func() {
		It("[test_id:TS-CNV-72329-010] should handle non-existent NAD reference gracefully", func() {
			By("Creating valid bridge NAD")
			validNAD := libnet.NewBridgeNetAttachDef("valid-nad-neg", "br-valid-neg")
			_, err := libnet.CreateNetAttachDef(context.Background(), testNamespace, validNAD)
			Expect(err).ToNot(HaveOccurred())

			By("Creating VM with secondary interface on valid NAD")
			vmi := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("net1")),
				libvmi.WithNetwork(libvmi.MultusNetwork("net1", "valid-nad-neg")),
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

			By("Patching VM NAD reference to a non-existent NAD name")
			patchPayload, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", "does-not-exist"),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchPayload, metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying VM remains running and stable after invalid NAD reference")
			Consistently(func(g Gomega) {
				vm, err = kubevirt.Client().VirtualMachine(testNamespace).Get(
					context.Background(), vm.Name, metav1.GetOptions{})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(vm.Status.PrintableStatus).To(Equal(v1.VirtualMachineStatusRunning))
			}, 15*time.Second, pollingInterval).Should(Succeed(),
				"VM should remain running after setting invalid NAD reference")

			By("Verifying an error condition or warning event is generated for invalid NAD")
			hasErrorSignal := false

			// Check for error-related conditions on the VM
			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			for _, condition := range vm.Status.Conditions {
				if condition.Type == v1.VirtualMachineRestartRequired ||
					(condition.Status == "True" && condition.Reason != "") {
					hasErrorSignal = true
					break
				}
			}

			// Also check for warning events related to the VM
			if !hasErrorSignal {
				events, eventErr := kubevirt.Client().CoreV1().Events(testNamespace).List(
					context.Background(), metav1.ListOptions{
						FieldSelector: "involvedObject.name=" + vm.Name,
					})
				Expect(eventErr).ToNot(HaveOccurred())
				for _, event := range events.Items {
					if event.Type == "Warning" {
						hasErrorSignal = true
						break
					}
				}
			}

			Expect(hasErrorSignal).To(BeTrue(),
				"system should report an error condition or warning event for non-existent NAD")

			By("Verifying VMI remains in Running phase")
			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(vmi.Status.Phase).To(Equal(v1.Running),
				"VMI should remain in Running phase after invalid NAD reference")
		})
	})
}))
