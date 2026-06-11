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
NAD Reference Live Update - Feature Gate Control Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Scenarios:
  - TS-CNV-72329-014: RestartRequired when FG disabled
  - TS-CNV-72329-015: NAD swap works after enabling FG at runtime
*/

var _ = Describe(SIG("NAD name live update extended"), decorators.RequiresTwoSchedulableNodes, Serial, func() {
	const (
		sourceNAD = "nad-1"
		targetNAD = "nad-2"

		timeoutInterval = 300 * time.Second
		pollingInterval = 5 * time.Second
	)

	Context("with LiveUpdateNADRef feature gate disabled", Ordered, func() {
		var (
			ctx           context.Context
			testNamespace string
			vm            *v1.VirtualMachine
			vmi           *v1.VirtualMachineInstance
		)

		const vmName = "test-vm-fg-disabled"

		BeforeAll(func() {
			ctx = context.Background()
			testNamespace = testsuite.GetTestNamespace(nil)

			By("Disabling LiveUpdateNADRef feature gate")
			config.DisableFeatureGate("LiveUpdateNADRef")
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

			By("Creating and starting VM on source NAD")
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
		})

		/*
		Preconditions:
		    - LiveUpdateNADRef feature gate explicitly DISABLED
		    - VM rollout strategy set to LiveUpdate
		    - Source NAD and target NAD created
		    - VM running with secondary interface on source NAD

		Steps:
		    1. Patch VM spec to change NAD reference from source to target
		    2. Query VM conditions for RestartRequired
		    3. Verify VMI spec still references old NAD

		Expected:
		    - RestartRequired condition is set to True on VM
		    - No MigrationRequired condition (no auto-migration triggered)
		    - VMI spec networks still reference the original source NAD
		*/
		It("[test_id:TS-CNV-72329-014] should require restart when NAD reference changes with feature gate disabled", func() {
			By("Patching VM spec to change NAD reference")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNAD),
			).GeneratePayload()
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			_, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				ctx, vmName, types.JSONPatchType, patchData, metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying RestartRequired condition IS set")
			Eventually(matcher.ThisVM(vm), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineRestartRequired))

			By("Verifying VMI spec still references old NAD")
			vmiObj, err := kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
				ctx, vmName, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			for _, net := range vmiObj.Spec.Networks {
				if net.Multus != nil {
					ExpectWithOffset(1, net.Multus.NetworkName).To(Equal(sourceNAD),
						"VMI should still reference the original NAD when feature gate is disabled")
				}
			}
		})
	})

	Context("NAD swap works after enabling feature gate at runtime", Ordered, func() {
		var (
			ctx           context.Context
			testNamespace string
			vm            *v1.VirtualMachine
			vmi           *v1.VirtualMachineInstance
		)

		const vmName = "test-vm-fg-runtime"

		BeforeAll(func() {
			ctx = context.Background()
			testNamespace = testsuite.GetTestNamespace(nil)

			By("Starting with LiveUpdateNADRef feature gate disabled")
			config.DisableFeatureGate("LiveUpdateNADRef")
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

			By("Creating and starting VM on source NAD")
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
		})

		/*
		Preconditions:
		    - LiveUpdateNADRef feature gate initially DISABLED
		    - VM rollout strategy set to LiveUpdate
		    - Source NAD and target NAD created
		    - VM running with secondary interface on source NAD

		Steps:
		    1. Enable LiveUpdateNADRef feature gate at runtime
		    2. Wait for config to propagate to virt-controller
		    3. Patch VM spec to change NAD reference from source to target
		    4. Observe VMI conditions

		Expected:
		    - Feature gate toggle takes effect without cluster restart
		    - MigrationRequired condition appears (migration triggered after FG enabled at runtime)
		*/
		It("[test_id:TS-CNV-72329-015] should support NAD swap after feature gate is enabled at runtime", func() {
			By("Enabling LiveUpdateNADRef feature gate at runtime")
			config.EnableFeatureGate("LiveUpdateNADRef")

			By("Waiting for config to propagate to virt-controller")
			config.WaitForConfigToBePropagatedToComponent("virt-controller", "",
				config.FeatureGateEnabled("LiveUpdateNADRef"), timeoutInterval)

			By("Patching NAD reference after FG enabled at runtime")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", targetNAD),
			).GeneratePayload()
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			_, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				ctx, vmName, types.JSONPatchType, patchData, metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying MigrationRequired condition appears (migration triggered)")
			Eventually(matcher.ThisVMI(vmi), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))
		})
	})
})
