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
	libvmici "kubevirt.io/kubevirt/pkg/libvmi/cloudinit"
	"kubevirt.io/kubevirt/pkg/pointer"
	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/framework/matcher"
	"kubevirt.io/kubevirt/tests/libkubevirt"
	"kubevirt.io/kubevirt/tests/libkubevirt/config"
	"kubevirt.io/kubevirt/tests/libnet"
	"kubevirt.io/kubevirt/tests/libnet/cloudinit"
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/testsuite"
)

var _ = Describe(SIG("NAD name live update extended", decorators.RequiresTwoSchedulableNodes, Serial, func() {
	// Preconditions:
	// - Multi-node cluster with at least 2 schedulable nodes
	// - Multus CNI with bridge plugin available
	//
	// Steps:
	// - Various scenarios testing NAD reference live update behavior
	//
	// Expected:
	// - NAD reference changes handled correctly based on feature gate state
	const (
		pollingInterval = 2 * time.Second
		timeoutInterval = 5 * time.Minute
	)

	var testNamespace string

	Context("with LiveUpdateNADRef feature gate enabled", func() {
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

		It("should update multiple NAD references simultaneously via migration", func() {
			// Preconditions:
			// - LiveUpdateNADRef feature gate enabled
			// - VMRolloutStrategy set to LiveUpdate
			// - Three bridge-based NADs and two target NADs created
			//
			// Steps:
			// 1. Create 5 bridge-based NADs (3 source + 2 target)
			// 2. Create and start VM with 3 secondary interfaces
			// 3. Patch VM to change NAD references for 2 of 3 interfaces
			// 4. Wait for migration condition to appear and resolve
			// 5. Verify VMI spec reflects updated NAD references
			//
			// Expected:
			// - Migration is triggered
			// - Two NAD references updated, third unchanged
			// - VMI spec networks match updated VM spec

			const (
				nad1       = "multi-nad-1"
				nad2       = "multi-nad-2"
				nad3       = "multi-nad-3"
				targetNad1 = "multi-target-nad-1"
				targetNad2 = "multi-target-nad-2"
				br1        = "mbr-1"
				br2        = "mbr-2"
				br3        = "mbr-3"
				br4        = "mbr-4"
				br5        = "mbr-5"
			)

			By("Creating source and target NADs")
			for _, nadDef := range []struct {
				name   string
				bridge string
			}{
				{nad1, br1}, {nad2, br2}, {nad3, br3},
				{targetNad1, br4}, {targetNad2, br5},
			} {
				netAttachDef := libnet.NewBridgeNetAttachDef(nadDef.name, nadDef.bridge)
				_, err := libnet.CreateNetAttachDef(context.Background(), testNamespace, netAttachDef)
				Expect(err).NotTo(HaveOccurred())
			}

			By("Creating a VM with 3 secondary interfaces")
			const (
				ifaceName1 = "net1"
				ifaceName2 = "net2"
				ifaceName3 = "net3"
				vmName     = "multi-nad-vm"
				ip1        = "10.1.1.50/24"
			)

			networkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth0", cloudinit.WithAddresses(ip1)),
			)
			Expect(err).ToNot(HaveOccurred())

			vmi := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(ifaceName1)),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(ifaceName2)),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(ifaceName3)),
				libvmi.WithNetwork(libvmi.MultusNetwork(ifaceName1, nad1)),
				libvmi.WithNetwork(libvmi.MultusNetwork(ifaceName2, nad2)),
				libvmi.WithNetwork(libvmi.MultusNetwork(ifaceName3, nad3)),
				libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudNetworkData(networkData)),
			)

			vm := libvmi.NewVirtualMachine(vmi, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Create(
				context.Background(), vm, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))

			By("Patching VM to change 2 of 3 NAD references")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNad1),
				patch.WithReplace("/spec/template/spec/networks/1/multus/networkName", targetNad2),
			).GeneratePayload()
			Expect(err).NotTo(HaveOccurred())

			_, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				context.Background(), vmName, types.JSONPatchType, patchData, metav1.PatchOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for migration condition to appear")
			Eventually(func() (*v1.VirtualMachineInstance, error) {
				return kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
					context.Background(), vmName, metav1.GetOptions{})
			}, timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))

			By("Waiting for migration to complete")
			Eventually(func() (*v1.VirtualMachineInstance, error) {
				return kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
					context.Background(), vmName, metav1.GetOptions{})
			}, timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))

			By("Verifying VMI spec has updated NAD references")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vmName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Find networks by interface name
			networksByName := map[string]v1.Network{}
			for _, net := range updatedVMI.Spec.Networks {
				networksByName[net.Name] = net
			}

			Expect(networksByName[ifaceName1].Multus.NetworkName).To(Equal(targetNad1),
				"first NAD reference should be updated to target-nad-1")
			Expect(networksByName[ifaceName2].Multus.NetworkName).To(Equal(targetNad2),
				"second NAD reference should be updated to target-nad-2")
			Expect(networksByName[ifaceName3].Multus.NetworkName).To(Equal(nad3),
				"third NAD reference should remain unchanged")
		})

		It("should not set RestartRequired condition when NAD reference changes", func() {
			// Preconditions:
			// - LiveUpdateNADRef feature gate enabled
			// - VMRolloutStrategy set to LiveUpdate
			// - VM running with a secondary Multus network
			//
			// Steps:
			// 1. Create source and target NADs
			// 2. Create and start VM with secondary interface
			// 3. Change NAD reference on VM spec
			// 4. Verify RestartRequired condition is NOT set
			//
			// Expected:
			// - VM does NOT get RestartRequired condition (live update handles it)
			// - Migration is triggered instead of requiring restart

			const (
				srcNAD  = "no-restart-src"
				dstNAD  = "no-restart-dst"
				br1     = "nrb-1"
				br2     = "nrb-2"
				vmName  = "no-restart-vm"
				netName = "net1"
			)

			By("Creating NADs")
			for _, nadDef := range []struct {
				name   string
				bridge string
			}{
				{srcNAD, br1}, {dstNAD, br2},
			} {
				netAttachDef := libnet.NewBridgeNetAttachDef(nadDef.name, nadDef.bridge)
				_, err := libnet.CreateNetAttachDef(context.Background(), testNamespace, netAttachDef)
				Expect(err).NotTo(HaveOccurred())
			}

			By("Creating and starting VM")
			networkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth0", cloudinit.WithAddresses("10.1.1.60/24")),
			)
			Expect(err).ToNot(HaveOccurred())

			vmi := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(netName)),
				libvmi.WithNetwork(libvmi.MultusNetwork(netName, srcNAD)),
				libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudNetworkData(networkData)),
			)
			vm := libvmi.NewVirtualMachine(vmi, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Create(
				context.Background(), vm, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))

			By("Changing NAD reference")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", dstNAD),
			).GeneratePayload()
			Expect(err).NotTo(HaveOccurred())

			_, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				context.Background(), vmName, types.JSONPatchType, patchData, metav1.PatchOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying RestartRequired condition is NOT set")
			Consistently(matcher.ThisVM(vm), 30*time.Second, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineRestartRequired))
		})
	})

	Context("with LiveUpdateNADRef feature gate disabled", func() {
		BeforeEach(func() {
			virtClient := kubevirt.Client()
			config.DisableFeatureGate("LiveUpdateNADRef")

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

		It("should require restart when NAD reference changes", func() {
			// Preconditions:
			// - LiveUpdateNADRef feature gate disabled
			// - VMRolloutStrategy set to LiveUpdate
			// - VM running with a secondary Multus network
			//
			// Steps:
			// 1. Create source and target NADs
			// 2. Create and start VM with secondary interface
			// 3. Change NAD reference on VM spec
			// 4. Verify RestartRequired condition IS set on the VM
			//
			// Expected:
			// - VM gets RestartRequired condition
			// - No automatic migration triggered
			// - VMI spec still references old NAD

			const (
				srcNAD  = "fg-off-src"
				dstNAD  = "fg-off-dst"
				br1     = "fob-1"
				br2     = "fob-2"
				vmName  = "fg-off-vm"
				netName = "net1"
			)

			By("Creating NADs")
			for _, nadDef := range []struct {
				name   string
				bridge string
			}{
				{srcNAD, br1}, {dstNAD, br2},
			} {
				netAttachDef := libnet.NewBridgeNetAttachDef(nadDef.name, nadDef.bridge)
				_, err := libnet.CreateNetAttachDef(context.Background(), testNamespace, netAttachDef)
				Expect(err).NotTo(HaveOccurred())
			}

			By("Creating and starting VM")
			networkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth0", cloudinit.WithAddresses("10.1.1.70/24")),
			)
			Expect(err).ToNot(HaveOccurred())

			vmi := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding(netName)),
				libvmi.WithNetwork(libvmi.MultusNetwork(netName, srcNAD)),
				libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudNetworkData(networkData)),
			)
			vm := libvmi.NewVirtualMachine(vmi, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Create(
				context.Background(), vm, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			Eventually(matcher.ThisVM(vm)).WithTimeout(timeoutInterval).WithPolling(pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))

			By("Changing NAD reference")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", dstNAD),
			).GeneratePayload()
			Expect(err).NotTo(HaveOccurred())

			_, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				context.Background(), vmName, types.JSONPatchType, patchData, metav1.PatchOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying RestartRequired condition IS set")
			Eventually(matcher.ThisVM(vm), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineRestartRequired))

			By("Verifying VMI spec still references old NAD")
			vmiObj, err := kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				context.Background(), vmName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			for _, net := range vmiObj.Spec.Networks {
				if net.Multus != nil {
					Expect(net.Multus.NetworkName).To(Equal(srcNAD),
						"VMI should still reference the original NAD when feature gate is disabled")
				}
			}
		})
	})
}))
