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
// Group 6: VM Controller NAD Sync (Scenarios 019-020)
var _ = Describe(SIG("NAD reference live update - VM controller sync", decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		sourceNADName   = "vc-source-nad"
		targetNADName   = "vc-target-nad"
		sourceBridge    = "vc-src-br"
		targetBridge    = "vc-tgt-br"
		sourceIP        = "10.1.7.100"
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

		By("Creating source and target bridge NADs")
		sourceNAD := libnet.NewBridgeNetAttachDef(sourceNADName, sourceBridge)
		_, err = libnet.CreateNetAttachDef(context.Background(), testNamespace, sourceNAD)
		Expect(err).NotTo(HaveOccurred())

		targetNAD := libnet.NewBridgeNetAttachDef(targetNADName, targetBridge)
		_, err = libnet.CreateNetAttachDef(context.Background(), testNamespace, targetNAD)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("when VM spec NAD reference is updated with feature gate enabled", Ordered, func() {
		const vmName = "vm-ctrl-sync-vm"
		var vm *v1.VirtualMachine

		BeforeAll(func() {
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

		It("[test_id:TS-CNV-72329-019]should sync updated NAD reference to VMI spec", func() {
			By("Patching VM NAD reference to target NAD")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNADName),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, err = virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying VMI spec is updated with new NAD reference")
			Eventually(func() string {
				vmi := getVMI(virtClient, testNamespace, vmName)
				for _, net := range vmi.Spec.Networks {
					if net.Multus != nil && net.Name == ifaceName {
						return net.Multus.NetworkName
					}
				}
				return ""
			}, 30*time.Second, pollingInterval).Should(ContainSubstring(targetNADName),
				"VMI spec should be synced with the updated NAD reference from VM spec")
		})
	})

	Context("when VM spec has pod network changes alongside NAD changes", Ordered, func() {
		const vmName = "vm-ctrl-podnet-vm"
		var vm *v1.VirtualMachine

		BeforeAll(func() {
			nodes := libnode.GetAllSchedulableNodes(virtClient)
			Expect(len(nodes.Items)).To(BeNumerically(">=", 2))

			By("Creating VM with default pod network + secondary bridge interface")
			networkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth0",
					cloudinit.WithAddresses(sourceIP+subnetMask),
				),
			)
			Expect(err).ToNot(HaveOccurred())

			vmiSpec := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(*v1.DefaultMasqueradeNetworkInterface()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
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

		It("[test_id:TS-CNV-72329-020]should not affect pod network when syncing secondary NAD changes", func() {
			By("Recording pod network configuration before NAD swap")
			vmi := getVMI(virtClient, testNamespace, vmName)
			var originalPodNetworkName string
			for _, net := range vmi.Spec.Networks {
				if net.Pod != nil {
					originalPodNetworkName = net.Name
					break
				}
			}
			Expect(originalPodNetworkName).ToNot(BeEmpty(), "pod network should exist")

			By("Performing NAD swap on secondary interface")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/1/multus/networkName", targetNADName),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, err = virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for migration to process")
			vmi = getVMI(virtClient, testNamespace, vmName)
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))

			By("Verifying pod network is unchanged")
			updatedVMI := getVMI(virtClient, testNamespace, vmName)
			var podNetworkFound bool
			for _, net := range updatedVMI.Spec.Networks {
				if net.Pod != nil {
					Expect(net.Name).To(Equal(originalPodNetworkName),
						"pod network name should be unchanged after NAD sync")
					podNetworkFound = true
					break
				}
			}
			Expect(podNetworkFound).To(BeTrue(), "pod network should still exist after NAD sync")

			By("Verifying secondary network updated correctly")
			for _, net := range updatedVMI.Spec.Networks {
				if net.Name == ifaceName && net.Multus != nil {
					Expect(net.Multus.NetworkName).To(ContainSubstring(targetNADName),
						"secondary NAD reference should be updated")
				}
			}
		})
	})
}))
