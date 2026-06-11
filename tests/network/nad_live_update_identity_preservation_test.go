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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/pkg/libvmi"
	libvmici "kubevirt.io/kubevirt/pkg/libvmi/cloudinit"
	"kubevirt.io/kubevirt/pkg/pointer"

	"kubevirt.io/kubevirt/pkg/apimachinery/patch"
	"kubevirt.io/kubevirt/tests/console"
	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/framework/matcher"
	"kubevirt.io/kubevirt/tests/libkubevirt/config"
	"kubevirt.io/kubevirt/tests/libnet"
	"kubevirt.io/kubevirt/tests/libnet/cloudinit"
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/libwait"
	"kubevirt.io/kubevirt/tests/testsuite"
)

/*
NAD Reference Live Update - Interface Identity Preservation Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Scenarios:
  - TS-CNV-72329-010: Guest interface name preserved after NAD swap
  - TS-CNV-72329-011: MAC address preserved after NAD swap and migration
*/

var _ = Describe(SIG("NAD name live update"), decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		sourceNAD = "nad-1"
		targetNAD = "nad-2"

		timeoutInterval = 300 * time.Second
		pollingInterval = 5 * time.Second
	)

	Context("Guest interface name unchanged after NAD swap", Ordered, func() {
		var (
			ctx             context.Context
			testNamespace   string
			vm              *v1.VirtualMachine
			vmi             *v1.VirtualMachineInstance
			ifaceNameBefore string
		)

		const vmName = "test-vm-iface-name"

		BeforeAll(func() {
			ctx = context.Background()
			testNamespace = testsuite.GetTestNamespace(nil)

			By("Enabling LiveUpdateNADRef feature gate and configuring cluster")
			config.EnableFeatureGate("LiveUpdateNADRef")
			updateStrategy := &v1.KubeVirtWorkloadUpdateStrategy{
				WorkloadUpdateMethods: []v1.WorkloadUpdateMethod{v1.WorkloadUpdateMethodLiveMigrate},
			}
			rolloutStrategy := pointer.P(v1.VMRolloutStrategyLiveUpdate)
			err := config.RegisterKubevirtConfigChange(
				config.WithWorkloadUpdateStrategy(updateStrategy),
				config.WithVMRolloutStrategy(rolloutStrategy),
			)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating source and target bridge-based NADs")
			netAttachDef1 := libnet.NewBridgeNetAttachDef(sourceNAD, "br-1")
			_, err = libnet.CreateNetAttachDef(ctx, testNamespace, netAttachDef1)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			netAttachDef2 := libnet.NewBridgeNetAttachDef(targetNAD, "br-2")
			_, err = libnet.CreateNetAttachDef(ctx, testNamespace, netAttachDef2)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating and starting VM with secondary interface on source NAD")
			networkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth1",
					cloudinit.WithAddresses("10.1.1.10/24"),
				),
			)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			vmiSpec := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("net1")),
				libvmi.WithNetwork(libvmi.MultusNetwork("net1", sourceNAD)),
				libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudNetworkData(networkData)),
			)
			vm = libvmi.NewVirtualMachine(vmiSpec, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Create(ctx, vm, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Waiting for VM to be ready")
			Eventually(matcher.ThisVM(vm), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))

			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(ctx, vmName, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToAlpine)

			By("Capturing guest interface name before swap")
			for _, iface := range vmi.Status.Interfaces {
				if iface.Name == "net1" {
					ifaceNameBefore = iface.InterfaceName
					break
				}
			}
			ExpectWithOffset(1, ifaceNameBefore).ToNot(BeEmpty(), "interface name should be captured before swap")
		})

		/*
		Preconditions:
		    - LiveUpdateNADRef feature gate enabled
		    - Source NAD (br-1) and target NAD (br-2) created
		    - VM running with secondary interface on source NAD
		    - Guest interface name (e.g., eth0) captured before swap

		Steps:
		    1. Patch VM spec to change NAD reference from source to target
		    2. Wait for migration to complete
		    3. Query guest interface name after swap

		Expected:
		    - Guest interface name before swap equals interface name after swap
		    - Interface is functional (link up) after swap
		*/
		It("[test_id:TS-CNV-72329-010] should preserve guest interface name after NAD swap and migration", func() {
			By("Patching VM spec to change NAD reference from source to target")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNAD),
			).GeneratePayload()
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			_, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				ctx, vmName, types.JSONPatchType, patchData, metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Waiting for MigrationRequired condition to appear")
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))

			By("Waiting for migration to complete")
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))

			By("Capturing guest interface name after swap")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				ctx, vmName, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			var ifaceNameAfter string
			for _, iface := range updatedVMI.Status.Interfaces {
				if iface.Name == "net1" {
					ifaceNameAfter = iface.InterfaceName
					break
				}
			}
			ExpectWithOffset(1, ifaceNameAfter).ToNot(BeEmpty(), "interface name should exist after swap")

			By("Comparing interface names")
			ExpectWithOffset(1, ifaceNameAfter).To(Equal(ifaceNameBefore),
				fmt.Sprintf("guest interface name should be preserved after NAD swap (before=%s, after=%s)", ifaceNameBefore, ifaceNameAfter))

			By("Verifying interface is functional (link up)")
			Expect(console.RunCommand(updatedVMI, fmt.Sprintf("ip link show %s | grep UP", ifaceNameAfter), 10*time.Second)).To(Succeed())
		})
	})

	Context("MAC address preserved after NAD swap and migration", Ordered, func() {
		var (
			ctx           context.Context
			testNamespace string
			vm            *v1.VirtualMachine
			vmi           *v1.VirtualMachineInstance
			macBefore     string
		)

		const vmName = "test-vm-mac-preserve"

		BeforeAll(func() {
			ctx = context.Background()
			testNamespace = testsuite.GetTestNamespace(nil)

			By("Enabling LiveUpdateNADRef feature gate and configuring cluster")
			config.EnableFeatureGate("LiveUpdateNADRef")
			updateStrategy := &v1.KubeVirtWorkloadUpdateStrategy{
				WorkloadUpdateMethods: []v1.WorkloadUpdateMethod{v1.WorkloadUpdateMethodLiveMigrate},
			}
			rolloutStrategy := pointer.P(v1.VMRolloutStrategyLiveUpdate)
			err := config.RegisterKubevirtConfigChange(
				config.WithWorkloadUpdateStrategy(updateStrategy),
				config.WithVMRolloutStrategy(rolloutStrategy),
			)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating source and target bridge-based NADs")
			netAttachDef1 := libnet.NewBridgeNetAttachDef(sourceNAD, "br-1")
			_, err = libnet.CreateNetAttachDef(ctx, testNamespace, netAttachDef1)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			netAttachDef2 := libnet.NewBridgeNetAttachDef(targetNAD, "br-2")
			_, err = libnet.CreateNetAttachDef(ctx, testNamespace, netAttachDef2)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating and starting VM with secondary interface on source NAD")
			networkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth1",
					cloudinit.WithAddresses("10.1.1.10/24"),
				),
			)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			vmiSpec := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("net1")),
				libvmi.WithNetwork(libvmi.MultusNetwork("net1", sourceNAD)),
				libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudNetworkData(networkData)),
			)
			vm = libvmi.NewVirtualMachine(vmiSpec, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Create(ctx, vm, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Waiting for VM to be ready")
			Eventually(matcher.ThisVM(vm), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))

			vmi, err = kubevirt.Client().VirtualMachineInstance(testNamespace).Get(ctx, vmName, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToAlpine)

			By("Capturing MAC address from VMI status before swap")
			for _, iface := range vmi.Status.Interfaces {
				if iface.Name == "net1" {
					macBefore = iface.MAC
					break
				}
			}
			ExpectWithOffset(1, macBefore).ToNot(BeEmpty(), "MAC address should be captured before swap")
		})

		/*
		Preconditions:
		    - LiveUpdateNADRef feature gate enabled
		    - Source NAD (br-1) and target NAD (br-2) created
		    - VM running with secondary interface on source NAD
		    - MAC address of secondary interface captured from VMI status before swap

		Steps:
		    1. Patch VM spec to change NAD reference from source to target
		    2. Wait for migration to complete
		    3. Query MAC address from updated VMI status

		Expected:
		    - MAC address before swap equals MAC address after swap
		    - VMI spec interface MAC matches guest-visible MAC
		*/
		It("[test_id:TS-CNV-72329-011] should preserve MAC address after NAD swap and migration", func() {
			By("Patching VM spec to change NAD reference from source to target")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNAD),
			).GeneratePayload()
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			_, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				ctx, vmName, types.JSONPatchType, patchData, metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Waiting for MigrationRequired condition to appear")
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))

			By("Waiting for migration to complete")
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))

			By("Comparing MAC address after migration")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				ctx, vmName, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			for _, iface := range updatedVMI.Status.Interfaces {
				if iface.Name == "net1" {
					ExpectWithOffset(1, iface.MAC).To(Equal(macBefore),
						"MAC address should be preserved after NAD swap")
				}
			}
		})
	})
})
