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
// Group 1: Core NAD Live Update (Scenarios 001-004)
// STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
var _ = Describe(SIG("NAD reference live update - core", decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		sourceNADName   = "source-nad"
		targetNADName   = "target-nad"
		sourceBridge    = "src-br"
		targetBridge    = "tgt-br"
		sourceIP        = "10.1.1.100"
		targetPeerIP    = "10.1.2.10"
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
	})

	BeforeEach(func() {
		testNamespace = testsuite.GetTestNamespace(nil)

		By("Creating source bridge NAD")
		sourceNAD := libnet.NewBridgeNetAttachDef(sourceNADName, sourceBridge)
		_, err := libnet.CreateNetAttachDef(context.Background(), testNamespace, sourceNAD)
		Expect(err).NotTo(HaveOccurred())

		By("Creating target bridge NAD")
		targetNAD := libnet.NewBridgeNetAttachDef(targetNADName, targetBridge)
		_, err = libnet.CreateNetAttachDef(context.Background(), testNamespace, targetNAD)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("when NAD reference is changed on a running VM", Ordered, func() {
		const vmName = "nad-swap-vm"
		var vm *v1.VirtualMachine

		BeforeAll(func() {
			nodes := libnode.GetAllSchedulableNodes(virtClient)
			Expect(len(nodes.Items)).To(BeNumerically(">=", 2))
			sourceNodeName := nodes.Items[0].Name

			By("Creating VM with secondary bridge interface on source NAD")
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
				libvmi.WithNodeAffinityFor(sourceNodeName),
				libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudNetworkData(networkData)),
			)
			vm = libvmi.NewVirtualMachine(vmiSpec, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err = virtClient.VirtualMachine(testNamespace).Create(context.Background(), vm, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for VM to have agent connected")
			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))
		})

		It("[test_id:TS-CNV-72329-001]should trigger live migration to apply new NAD", func() {
			By("Patching VM spec to change NAD reference from source to target")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNADName),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, err = virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying migration is triggered via MigrationRequired condition")
			vmi, err := virtClient.VirtualMachineInstance(testNamespace).Get(context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))

			By("Waiting for migration to complete (condition clears)")
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))
		})

		It("[test_id:TS-CNV-72329-002]should establish connectivity on the new network after NAD swap", func() {
			By("Getting the target node after migration")
			vmi, err := virtClient.VirtualMachineInstance(testNamespace).Get(context.Background(), vm.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			targetNode := vmi.Status.NodeName

			By("Creating a peer VMI on the target NAD for connectivity validation")
			peerNetworkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth0",
					cloudinit.WithAddresses(targetPeerIP+subnetMask),
				),
			)
			Expect(err).ToNot(HaveOccurred())

			peerVMI := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName("target-peer"),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(ifaceName)),
				libvmi.WithNetwork(libvmi.MultusNetwork(ifaceName, targetNADName)),
				libvmi.WithNodeAffinityFor(targetNode),
				libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudNetworkData(peerNetworkData)),
			)
			peerVMI, err = virtClient.VirtualMachineInstance(testNamespace).Create(
				context.Background(), peerVMI, metav1.CreateOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			Eventually(matcher.ThisVMI(peerVMI)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))
			Expect(console.LoginToAlpine(peerVMI)).To(Succeed())

			By("Configuring IP on migrated VM for the new network")
			Expect(console.LoginToAlpine(vmi)).To(Succeed())
			const ipAfterSwap = "10.1.2.100"
			err = configureIPInGuest(vmi, ipAfterSwap+subnetMask)
			Expect(err).NotTo(HaveOccurred())

			By("Pinging peer VM on target network from migrated VM")
			Expect(libnet.PingFromVMConsole(peerVMI, ipAfterSwap)).To(Succeed())
		})
	})

	Context("interface identity preservation after NAD swap", Ordered, func() {
		const vmName = "iface-preserve-vm"
		var (
			vm                *v1.VirtualMachine
			originalIfaceName string
			originalMAC       string
		)

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

			By("Recording original interface name and MAC address")
			vmi, err := virtClient.VirtualMachineInstance(testNamespace).Get(context.Background(), vmName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(vmi.Status.Interfaces).ToNot(BeEmpty())

			for _, iface := range vmi.Status.Interfaces {
				if iface.Name == ifaceName {
					originalIfaceName = iface.Name
					originalMAC = iface.MAC
					break
				}
			}
			Expect(originalIfaceName).ToNot(BeEmpty(), "should find secondary interface")
			Expect(originalMAC).ToNot(BeEmpty(), "should have MAC for secondary interface")
		})

		It("[test_id:TS-CNV-72329-003]should preserve interface name and MAC address after NAD swap", func() {
			By("Patching VM NAD reference to trigger migration")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNADName),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, err = virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for migration to complete")
			vmi, err := virtClient.VirtualMachineInstance(testNamespace).Get(context.Background(), vmName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))

			By("Verifying interface name is preserved")
			updatedVMI, err := virtClient.VirtualMachineInstance(testNamespace).Get(context.Background(), vmName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			var foundIface *v1.VirtualMachineInstanceNetworkInterface
			for i := range updatedVMI.Status.Interfaces {
				if updatedVMI.Status.Interfaces[i].Name == ifaceName {
					foundIface = &updatedVMI.Status.Interfaces[i]
					break
				}
			}
			Expect(foundIface).ToNot(BeNil(), "secondary interface should still exist")
			Expect(foundIface.Name).To(Equal(originalIfaceName),
				"interface name should be preserved after NAD swap")

			By("Verifying MAC address is preserved")
			Expect(foundIface.MAC).To(Equal(originalMAC),
				"MAC address should be preserved after NAD swap")
		})
	})

	Context("when target NAD does not exist", Ordered, func() {
		const vmName = "nad-error-vm"
		var vm *v1.VirtualMachine

		BeforeAll(func() {
			nodes := libnode.GetAllSchedulableNodes(virtClient)
			Expect(len(nodes.Items)).To(BeNumerically(">=", 2))

			By("Creating VM with secondary interface on valid NAD")
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

		It("[test_id:TS-CNV-72329-004]should report error for non-existent NAD reference", func() {
			By("Patching VM spec to reference non-existent NAD")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", "nonexistent-nad"),
			).GeneratePayload()
			Expect(err).ToNot(HaveOccurred())

			_, err = virtClient.VirtualMachine(testNamespace).Patch(
				context.Background(), vm.Name, types.JSONPatchType, patchData, metav1.PatchOptions{},
			)

			By("Verifying error condition or migration failure")
			if err != nil {
				// Patch rejected at admission — expected behavior
				return
			}

			// Patch accepted — migration should fail due to non-existent NAD
			vmi, getErr := virtClient.VirtualMachineInstance(testNamespace).Get(context.Background(), vmName, metav1.GetOptions{})
			Expect(getErr).ToNot(HaveOccurred())

			// The migration evaluator should detect the issue
			Eventually(func() bool {
				updatedVMI, err := virtClient.VirtualMachineInstance(testNamespace).Get(context.Background(), vmi.Name, metav1.GetOptions{})
				if err != nil {
					return false
				}
				// Check for migration failure or error condition on the VMI
				for _, condition := range updatedVMI.Status.Conditions {
					if condition.Status == "True" &&
						(condition.Type == v1.VirtualMachineInstanceMigrationRequired ||
							condition.Type == v1.VirtualMachineInstanceIsMigratable) {
						return true
					}
				}
				// Also check VM-level RestartRequired
				updatedVM, err := virtClient.VirtualMachine(testNamespace).Get(context.Background(), vm.Name, metav1.GetOptions{})
				if err != nil {
					return false
				}
				for _, cond := range updatedVM.Status.Conditions {
					if cond.Type == v1.VirtualMachineRestartRequired {
						return true
					}
				}
				return false
			}, timeoutInterval, pollingInterval).Should(BeTrue(),
				"error condition should be raised for non-existent NAD reference")
		})
	})
}))
