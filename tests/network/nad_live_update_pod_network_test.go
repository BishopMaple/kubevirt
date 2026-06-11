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
Live Update NAD Reference Tests — Pod Network Preservation

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Scenarios covered:
  - TS-CNV-72329-006: Pod network preserved during secondary NAD update (Tier 1, P0)
*/

var _ = Describe(SIG("[CNV-72329] Live Update NAD Reference - Pod Network", decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		sourceNADName   = "source-nad-pod"
		targetNADName   = "target-nad-pod"
		sourceBridge    = "br-src-pod"
		targetBridge    = "br-tgt-pod"
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

	Context("Pod network preserved during secondary NAD update", func() {
		It("[test_id:TS-CNV-72329-006] should preserve pod network connectivity when secondary NAD is updated", func() {
			By("Creating VM with default pod network and secondary interface on source NAD")
			vmi := libvmifact.NewFedora(
				libvmi.WithInterface(*v1.DefaultMasqueradeNetworkInterface()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("net1")),
				libvmi.WithNetwork(libvmi.MultusNetwork("net1", sourceNADName)),
			)
			vm := libvmi.NewVirtualMachine(vmi, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err := kubevirt.Client().VirtualMachine(testNamespace).Create(context.Background(), vm, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for VMI to be ready")
			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))

			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(console.LoginToFedora(vmi)).To(Succeed())

			By("Verifying pod network connectivity before NAD swap")
			Expect(libnet.PingFromVMConsole(vmi, "10.96.0.10")).To(Succeed(),
				"pod network connectivity should work before NAD swap")

			By("Patching VM to change secondary NAD reference to target NAD")
			// The secondary network is at index 1 (index 0 is the pod network)
			patchPayload, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/1/multus/networkName", targetNADName),
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

			By("Verifying pod network interface is still present after secondary NAD swap")
			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			var podNetworkIP string
			for _, iface := range vmi.Status.Interfaces {
				if iface.Name == "default" {
					podNetworkIP = iface.IP
					break
				}
			}
			Expect(podNetworkIP).ToNot(BeEmpty(),
				"pod network interface should still have an IP after secondary NAD swap")

			By("Verifying pod network connectivity after NAD swap and migration")
			Expect(console.LoginToFedora(vmi)).To(Succeed())
			Expect(libnet.PingFromVMConsole(vmi, "10.96.0.10")).To(Succeed(),
				"pod network connectivity should still work after secondary NAD swap")
		})
	})
}))
