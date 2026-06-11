/*
 * This file is part of the KubeVirt project
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

	"kubevirt.io/client-go/kubecli"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/pkg/apimachinery/patch"
	"kubevirt.io/kubevirt/pkg/libvmi"
	libvmici "kubevirt.io/kubevirt/pkg/libvmi/cloudinit"
	"kubevirt.io/kubevirt/pkg/pointer"
	"kubevirt.io/kubevirt/tests/console"
	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/framework/matcher"
	"kubevirt.io/kubevirt/tests/libkubevirt"
	"kubevirt.io/kubevirt/tests/libkubevirt/config"
	"kubevirt.io/kubevirt/tests/libnet"
	"kubevirt.io/kubevirt/tests/libnet/cloudinit"
	"kubevirt.io/kubevirt/tests/libnode"
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/testsuite"
)

// CNV-72329: NAD Reference Live Update for Secondary VM Networks
// Group 7: Invalid/Edge Cases (Scenarios 021, 023)
var _ = Describe(SIG("NAD reference live update - edge cases", decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		sourceNADName   = "ec-source-nad"
		sourceBridge    = "ec-src-br"
		sourceIP        = "10.1.8.100"
		subnetMask      = "/24"
		ifaceName       = "net1"
		pollingInterval = 2 * time.Second
		timeoutInterval = 5 * time.Minute
	)
	var (
		testNamespace string
		virtClient    kubecli.KubevirtClient
	)

	BeforeEach(func() {
		virtClient = kubevirt.Client()
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
			time.Minute,
		)

		testNamespace = testsuite.GetTestNamespace(nil)

		By("Creating source bridge NAD")
		sourceNAD := libnet.NewBridgeNetAttachDef(sourceNADName, sourceBridge)
		_, err = libnet.CreateNetAttachDef(context.Background(), testNamespace, sourceNAD)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("when NAD reference is changed to the same value", func() {
		const vmName = "noop-nad-vm"
		var vm *v1.VirtualMachine

		BeforeEach(func() {
			nodes := libnode.GetAllSchedulableNodes(virtClient)
			Expect(len(nodes.Items)).To(BeNumerically(">=", 2))

			By("Creating VM with secondary bridge interface")
			networkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth0",
					cloudinit.WithAddresses(sourceIP+subnetMask),
				),
			)
			Expect(err).ToNot(HaveOccurred())

			vmiSpec := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(ifaceName)),
				libvmi.WithNetwork(libvmi.MultusNetwork(ifaceName, sourceNADName)),
				libvmi.WithNodeAffinityFor(nodes.Items[0].Name),
				libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudNetworkData(networkData)),
			)
			vm = libvmi.NewVirtualMachine(vmiSpec, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err = virtClient.VirtualMachine(testNamespace).Create(context.Background(), vm, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))

			Expect(console.LoginToAlpine(getVMI(virtClient, testNamespace, vmName))).To(Succeed())
		})

		It("[test_id:TS-CNV-72329-021]should be a no-op with no migration triggered when NAD unchanged", func() {
			By("Patching NAD reference to the same value")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", sourceNADName),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, err = virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying no migration is triggered for the no-op NAD change")
			Consistently(func() int {
				migrations, err := virtClient.VirtualMachineInstanceMigration(testNamespace).List(
					context.Background(), metav1.ListOptions{},
				)
				Expect(err).ToNot(HaveOccurred())
				return len(migrations.Items)
			}, 30*time.Second, pollingInterval).Should(Equal(0),
				"no migration should be triggered for no-op NAD change")
		})
	})

	Context("when NAD swap is attempted on a VM with no secondary interfaces", func() {
		const vmName = "no-secondary-vm"
		var vm *v1.VirtualMachine

		BeforeEach(func() {
			By("Creating VM with only pod network (no secondary interfaces)")
			vmiSpec := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(*v1.DefaultMasqueradeNetworkInterface()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			vm = libvmi.NewVirtualMachine(vmiSpec, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			var err error
			vm, err = virtClient.VirtualMachine(testNamespace).Create(context.Background(), vm, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))
		})

		It("[test_id:TS-CNV-72329-023]should reject or ignore NAD swap on VM without secondary interfaces", func() {
			By("Attempting to add a NAD reference to a VM with only pod network")
			// Try to replace the pod network with a Multus network — this should fail
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus", map[string]string{
					"networkName": sourceNADName,
				}),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, patchErr := virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)

			By("Verifying the operation is rejected or VM remains stable")
			if patchErr != nil {
				// Patch rejected — expected behavior
				return
			}

			// If patch was accepted, verify VM is still stable
			Consistently(func() bool {
				updatedVMI, err := virtClient.VirtualMachineInstance(testNamespace).Get(
					context.Background(), vmName, metav1.GetOptions{},
				)
				if err != nil {
					return false
				}
				return updatedVMI.Status.Phase == v1.Running
			}, 30*time.Second, pollingInterval).Should(BeTrue(),
				"VM should remain stable after invalid NAD swap attempt")
		})
	})
}))
