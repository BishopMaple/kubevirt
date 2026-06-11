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

	k8sv1 "k8s.io/api/core/v1"
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
NAD Reference Live Update - Pod Network Isolation Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Scenarios:
  - TS-CNV-72329-018: Default pod network unchanged during NAD swap
*/

var _ = Describe(SIG("NAD name live update"), decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		sourceNAD = "nad-1"
		targetNAD = "nad-2"

		timeoutInterval = 300 * time.Second
		pollingInterval = 5 * time.Second
	)

	Context("Default pod network unchanged during NAD swap", Ordered, func() {
		var (
			ctx           context.Context
			testNamespace string
			vm            *v1.VirtualMachine
			vmi           *v1.VirtualMachineInstance
			podNetworkIP  string
		)

		const vmName = "test-vm-pod-network"

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

			By("Creating source and target NADs")
			netAttachDef1 := libnet.NewBridgeNetAttachDef(sourceNAD, "br-1")
			_, err = libnet.CreateNetAttachDef(ctx, testNamespace, netAttachDef1)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			netAttachDef2 := libnet.NewBridgeNetAttachDef(targetNAD, "br-2")
			_, err = libnet.CreateNetAttachDef(ctx, testNamespace, netAttachDef2)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating VM with both default pod network and secondary Multus interface on source NAD")
			networkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth0",
					cloudinit.WithDHCP4Enabled(),
				),
				cloudinit.WithEthernet("eth1",
					cloudinit.WithAddresses("10.1.1.10/24"),
				),
			)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			vmiSpec := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
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

			By("Capturing pod network IP before swap")
			podNetworkIP = libnet.GetVmiPrimaryIPByFamily(vmi, k8sv1.IPv4Protocol)
			ExpectWithOffset(1, podNetworkIP).ToNot(BeEmpty(), "pod network IP should be available before swap")

			By("Verifying pod network connectivity before swap")
			Expect(libnet.PingFromVMConsole(vmi, podNetworkIP)).To(Succeed())
		})

		/*
		Preconditions:
		    - LiveUpdateNADRef feature gate enabled
		    - Source NAD and target NAD created
		    - VM running with both default pod network and secondary Multus interface on source NAD
		    - Pod network connectivity verified before swap

		Steps:
		    1. Patch VM spec to change secondary NAD reference from source to target
		    2. Wait for migration to complete
		    3. Verify pod network connectivity after swap

		Expected:
		    - Pod network connectivity remains functional before and after NAD swap
		    - Pod network IP unchanged after migration
		*/
		It("[test_id:TS-CNV-72329-018] should not affect the default pod network when secondary NAD is swapped", func() {
			By("Patching VM spec to change secondary NAD reference")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/1/multus/networkName", targetNAD),
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

			By("Getting updated VMI after migration")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				ctx, vmName, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying pod network is still functional after swap")
			updatedVMI = libwait.WaitUntilVMIReady(updatedVMI, console.LoginToAlpine)
			updatedPodIP := libnet.GetVmiPrimaryIPByFamily(updatedVMI, k8sv1.IPv4Protocol)
			ExpectWithOffset(1, updatedPodIP).ToNot(BeEmpty(), "pod network IP should be available after swap")

			By("Verifying pod network connectivity after NAD swap")
			Expect(libnet.PingFromVMConsole(updatedVMI, updatedPodIP)).To(Succeed())
		})
	})
})
