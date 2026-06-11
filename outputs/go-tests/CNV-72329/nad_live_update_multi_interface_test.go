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
// Group 3: Multiple NAD References (Scenarios 009-010)
var _ = Describe(SIG("NAD reference live update - multiple interfaces", decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		sourceNAD1Name  = "mi-source-1"
		sourceNAD2Name  = "mi-source-2"
		targetNAD1Name  = "mi-target-1"
		targetNAD2Name  = "mi-target-2"
		sourceIP        = "10.1.4.100"
		subnetMask      = "/24"
		iface1Name      = "net1"
		iface2Name      = "net2"
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

		By("Creating 4 bridge NADs (2 source, 2 target)")
		for _, nadConfig := range []struct {
			name   string
			bridge string
		}{
			{sourceNAD1Name, "mi-src-br1"},
			{sourceNAD2Name, "mi-src-br2"},
			{targetNAD1Name, "mi-tgt-br1"},
			{targetNAD2Name, "mi-tgt-br2"},
		} {
			nad := libnet.NewBridgeNetAttachDef(nadConfig.name, nadConfig.bridge)
			_, err := libnet.CreateNetAttachDef(context.Background(), testNamespace, nad)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	Context("when multiple NAD references are updated simultaneously", Ordered, func() {
		const vmName = "multi-nad-vm"
		var vm *v1.VirtualMachine

		BeforeAll(func() {
			nodes := libnode.GetAllSchedulableNodes(virtClient)
			Expect(len(nodes.Items)).To(BeNumerically(">=", 2))

			By("Creating VM with 2 secondary interfaces on source NADs")
			networkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth0",
					cloudinit.WithAddresses(sourceIP+subnetMask),
				),
			)
			Expect(err).ToNot(HaveOccurred())

			vmiSpec := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(iface1Name)),
				libvmi.WithNetwork(libvmi.MultusNetwork(iface1Name, sourceNAD1Name)),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(iface2Name)),
				libvmi.WithNetwork(libvmi.MultusNetwork(iface2Name, sourceNAD2Name)),
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

		It("[test_id:TS-CNV-72329-009]should trigger a single migration for all NAD changes", func() {
			By("Patching both NAD references simultaneously")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNAD1Name),
				patch.WithReplace("/spec/template/spec/networks/1/multus/networkName", targetNAD2Name),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, err = virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for migration to complete")
			vmi := getVMI(virtClient, testNamespace, vmName)
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))

			By("Verifying only one migration was triggered")
			migrations, err := virtClient.VirtualMachineInstanceMigration(testNamespace).List(context.Background(), metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(migrations.Items).To(HaveLen(1), "only one migration should be triggered for simultaneous NAD changes")
		})
	})

	Context("when only some interfaces have NAD references changed", Ordered, func() {
		const vmName = "partial-nad-vm"
		var vm *v1.VirtualMachine

		BeforeAll(func() {
			nodes := libnode.GetAllSchedulableNodes(virtClient)
			Expect(len(nodes.Items)).To(BeNumerically(">=", 2))

			By("Creating VM with 2 secondary interfaces")
			networkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth0",
					cloudinit.WithAddresses(sourceIP+subnetMask),
				),
			)
			Expect(err).ToNot(HaveOccurred())

			vmiSpec := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(iface1Name)),
				libvmi.WithNetwork(libvmi.MultusNetwork(iface1Name, sourceNAD1Name)),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(iface2Name)),
				libvmi.WithNetwork(libvmi.MultusNetwork(iface2Name, sourceNAD2Name)),
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

		It("[test_id:TS-CNV-72329-010]should update changed interfaces and preserve unchanged ones", func() {
			By("Changing only the first interface NAD reference")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNAD1Name),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, err = virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for migration to complete")
			vmi := getVMI(virtClient, testNamespace, vmName)
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))

			By("Verifying first interface references target NAD")
			updatedVMI := getVMI(virtClient, testNamespace, vmName)
			var net1Name, net2Name string
			for _, net := range updatedVMI.Spec.Networks {
				if net.Name == iface1Name && net.Multus != nil {
					net1Name = net.Multus.NetworkName
				}
				if net.Name == iface2Name && net.Multus != nil {
					net2Name = net.Multus.NetworkName
				}
			}
			Expect(net1Name).To(ContainSubstring(targetNAD1Name),
				"changed interface should reference target NAD")

			By("Verifying second interface still references original NAD")
			Expect(net2Name).To(ContainSubstring(sourceNAD2Name),
				"unchanged interface should preserve original NAD reference")
		})
	})
}))
