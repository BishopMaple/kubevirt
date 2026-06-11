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
// Group 2: Feature Gate Behavior (Scenarios 006-008)
var _ = Describe(SIG("NAD reference live update - feature gate behavior", decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		sourceNADName   = "fg-source-nad"
		targetNADName   = "fg-target-nad"
		sourceBridge    = "fg-src-br"
		targetBridge    = "fg-tgt-br"
		sourceIP        = "10.1.3.100"
		subnetMask      = "/24"
		ifaceName       = "net1"
		pollingInterval = 2 * time.Second
		timeoutInterval = 5 * time.Minute
	)
	var (
		testNamespace string
		virtClient    kubecli.KubevirtClient
	)

	Context("with LiveUpdateNADRef feature gate enabled", Ordered, func() {
		const vmName = "fg-enabled-vm"
		var vm *v1.VirtualMachine

		BeforeAll(func() {
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
		})

		It("[test_id:TS-CNV-72329-006]should NOT set RestartRequired when NAD reference changes", func() {
			By("Patching VM NAD reference to target NAD")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNADName),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, err = virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying RestartRequired condition is NOT set")
			Consistently(func(g Gomega) {
				updatedVM, err := virtClient.VirtualMachine(testNamespace).Get(context.Background(), vm.Name, metav1.GetOptions{})
				g.Expect(err).ToNot(HaveOccurred())
				for _, condition := range updatedVM.Status.Conditions {
					if condition.Type == v1.VirtualMachineRestartRequired {
						g.Expect(condition.Status).ToNot(Equal("True"),
							"RestartRequired should NOT be True when LiveUpdateNADRef is enabled")
					}
				}
			}, 30*time.Second, pollingInterval).Should(Succeed())

			By("Verifying migration is triggered instead (live update path)")
			vmi, err := virtClient.VirtualMachineInstance(testNamespace).Get(context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))
		})
	})

	Context("with LiveUpdateNADRef feature gate disabled", Ordered, func() {
		const vmName = "fg-disabled-vm"
		var vm *v1.VirtualMachine

		BeforeAll(func() {
			virtClient = kubevirt.Client()
			config.DisableFeatureGate("LiveUpdateNADRef")

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
			_, err := libnet.CreateNetAttachDef(context.Background(), testNamespace, sourceNAD)
			Expect(err).NotTo(HaveOccurred())

			targetNAD := libnet.NewBridgeNetAttachDef(targetNADName, targetBridge)
			_, err = libnet.CreateNetAttachDef(context.Background(), testNamespace, targetNAD)
			Expect(err).NotTo(HaveOccurred())

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

			Expect(console.LoginToAlpine(
				getVMI(virtClient, testNamespace, vmName),
			)).To(Succeed())
		})

		It("[test_id:TS-CNV-72329-007]should set RestartRequired when NAD reference changes", func() {
			By("Patching VM NAD reference")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNADName),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, err = virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying RestartRequired condition IS set")
			Eventually(func() bool {
				updatedVM, err := virtClient.VirtualMachine(testNamespace).Get(context.Background(), vm.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				for _, condition := range updatedVM.Status.Conditions {
					if condition.Type == v1.VirtualMachineRestartRequired &&
						condition.Status == "True" {
						return true
					}
				}
				return false
			}, timeoutInterval, pollingInterval).Should(BeTrue(),
				"RestartRequired should be set when LiveUpdateNADRef is disabled")

			By("Verifying no migration is triggered")
			migrations, err := virtClient.VirtualMachineInstanceMigration(testNamespace).List(context.Background(), metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(migrations.Items).To(BeEmpty(), "no migration should be triggered when feature gate is disabled")
		})

		It("[test_id:TS-CNV-72329-008]should retain old NAD reference in VMI spec when feature gate disabled", func() {
			By("Recording original NAD name from VMI spec")
			vmi := getVMI(virtClient, testNamespace, vmName)
			Expect(vmi.Spec.Networks).ToNot(BeEmpty())

			var originalNADName string
			for _, net := range vmi.Spec.Networks {
				if net.Multus != nil {
					originalNADName = net.Multus.NetworkName
					break
				}
			}
			Expect(originalNADName).ToNot(BeEmpty())

			By("Patching VM NAD reference to a new value")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNADName),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, err = virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying VMI spec still references original NAD")
			Consistently(func() string {
				updatedVMI := getVMI(virtClient, testNamespace, vmName)
				for _, net := range updatedVMI.Spec.Networks {
					if net.Multus != nil {
						return net.Multus.NetworkName
					}
				}
				return ""
			}, 30*time.Second, pollingInterval).Should(Equal(originalNADName),
				"VMI spec should retain old NAD reference when feature gate is disabled")
		})
	})
}))

func getVMI(virtClient kubecli.KubevirtClient, namespace, name string) *v1.VirtualMachineInstance {
	vmi, err := virtClient.VirtualMachineInstance(namespace).Get(context.Background(), name, metav1.GetOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return vmi
}
