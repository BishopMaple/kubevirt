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
NAD Reference Live Update - Negative Tests

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Scenarios:
  - TS-CNV-72329-003: Error when target NAD does not exist
  - TS-CNV-72329-007: Migration not triggered when NAD ref unchanged
  - TS-CNV-72329-013: Partial update when one target NAD invalid
*/

var _ = Describe(SIG("NAD name live update"), Serial, func() {
	const (
		sourceNAD = "nad-1"
		targetNAD = "nad-2"

		timeoutInterval = 300 * time.Second
		pollingInterval = 5 * time.Second
	)

	Context("[NEGATIVE] Error when target NAD does not exist", decorators.RequiresTwoSchedulableNodes, Ordered, func() {
		var (
			ctx           context.Context
			testNamespace string
			vm            *v1.VirtualMachine
			vmi           *v1.VirtualMachineInstance
		)

		const vmName = "test-vm-missing-nad"

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

			By("Creating only the source NAD (no target NAD)")
			netAttachDef1 := libnet.NewBridgeNetAttachDef(sourceNAD, "br-1")
			_, err = libnet.CreateNetAttachDef(ctx, testNamespace, netAttachDef1)
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
		})

		/*
		[NEGATIVE]
		Preconditions:
		    - LiveUpdateNADRef feature gate enabled
		    - Source NAD created (br-1), target NAD intentionally NOT created
		    - VM running with secondary interface on source NAD

		Steps:
		    1. Patch VM spec to reference non-existent target NAD
		    2. Observe MigrationRequired condition and migration behavior

		Expected:
		    - Patch is accepted by the API
		    - Migration does not complete successfully (stalls or fails)
		    - VM remains in a recoverable state
		*/
		It("[test_id:TS-CNV-72329-003] should fail or stall migration when target NAD does not exist", func() {
			By("Patching VM to reference non-existent target NAD")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", "non-existent-nad"),
			).GeneratePayload()
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			_, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				ctx, vmName, types.JSONPatchType, patchData, metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying migration stalls or fails due to missing NAD")
			Consistently(func() (*v1.VirtualMachineInstance, error) {
				return kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
					ctx, vmName, metav1.GetOptions{})
			}, 30*time.Second, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))
		})
	})

	Context("[NEGATIVE] Migration not triggered when NAD ref unchanged", Ordered, func() {
		var (
			ctx           context.Context
			testNamespace string
			vm            *v1.VirtualMachine
			vmi           *v1.VirtualMachineInstance
		)

		const vmName = "test-vm-unchanged-nad"

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

			By("Creating source NAD")
			netAttachDef1 := libnet.NewBridgeNetAttachDef(sourceNAD, "br-1")
			_, err = libnet.CreateNetAttachDef(ctx, testNamespace, netAttachDef1)
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
		[NEGATIVE]
		Preconditions:
		    - LiveUpdateNADRef feature gate enabled
		    - Source NAD created (br-1)
		    - VM running with secondary interface on source NAD

		Steps:
		    1. Patch VM spec with the same NAD reference (no actual change)
		    2. Observe VMI conditions over 30-second window

		Expected:
		    - MigrationRequired condition does NOT appear (Consistently MissingOrFalse)
		    - No migration is triggered
		*/
		It("[test_id:TS-CNV-72329-007] should not trigger migration when NAD reference is not changed", func() {
			By("Patching VM with the same NAD reference (no change)")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", sourceNAD),
			).GeneratePayload()
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			_, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				ctx, vmName, types.JSONPatchType, patchData, metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying no migration is triggered")
			Consistently(matcher.ThisVMI(vmi), 30*time.Second, pollingInterval).
				Should(matcher.HaveConditionMissingOrFalse(v1.VirtualMachineInstanceMigrationRequired))
		})
	})

	Context("[NEGATIVE] Partial update when one target NAD invalid", Ordered, func() {
		var (
			ctx           context.Context
			testNamespace string
			vm            *v1.VirtualMachine
		)

		const vmName = "test-vm-partial-nad"

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

			By("Creating 3 NADs: 2 source + 1 target (second target intentionally missing)")
			netAttachDef1 := libnet.NewBridgeNetAttachDef("source-1", "br-src-1")
			_, err = libnet.CreateNetAttachDef(ctx, testNamespace, netAttachDef1)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			netAttachDef2 := libnet.NewBridgeNetAttachDef("source-2", "br-src-2")
			_, err = libnet.CreateNetAttachDef(ctx, testNamespace, netAttachDef2)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			netAttachDef3 := libnet.NewBridgeNetAttachDef("target-1", "br-tgt-1")
			_, err = libnet.CreateNetAttachDef(ctx, testNamespace, netAttachDef3)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			// target-2 intentionally NOT created

			By("Creating VM with 2 secondary interfaces")
			networkData, err := cloudinit.NewNetworkData(
				cloudinit.WithEthernet("eth1",
					cloudinit.WithAddresses("10.1.1.10/24"),
				),
				cloudinit.WithEthernet("eth2",
					cloudinit.WithAddresses("10.1.2.10/24"),
				),
			)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			vmiSpec := libvmifact.NewAlpineWithTestTooling(
				libvmi.WithName(vmName),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("net1")),
				libvmi.WithNetwork(libvmi.MultusNetwork("net1", "source-1")),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("net2")),
				libvmi.WithNetwork(libvmi.MultusNetwork("net2", "source-2")),
				libvmi.WithCloudInitNoCloud(libvmici.WithNoCloudNetworkData(networkData)),
			)
			vm = libvmi.NewVirtualMachine(vmiSpec, libvmi.WithRunStrategy(v1.RunStrategyAlways))
			vm, err = kubevirt.Client().VirtualMachine(testNamespace).Create(ctx, vm, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Waiting for VM to be ready")
			Eventually(matcher.ThisVM(vm), timeoutInterval, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceAgentConnected))
		})

		/*
		[NEGATIVE]
		Preconditions:
		    - LiveUpdateNADRef feature gate enabled
		    - VM running with 2 secondary interfaces on separate source NADs
		    - Only one of two target NADs created (second target intentionally missing)

		Steps:
		    1. Patch VM to change both NAD references (one valid target, one non-existent)
		    2. Observe migration behavior and VM state

		Expected:
		    - Migration either fails atomically or handles the partial case gracefully
		    - VM remains in a recoverable state
		*/
		It("[test_id:TS-CNV-72329-013] should handle partial NAD update when one target NAD is invalid", func() {
			By("Patching VM to change both NAD references: one valid, one non-existent")
			patchData, err := patch.New(
				patch.WithReplace("/spec/template/spec/networks/0/multus/networkName", "target-1"),
				patch.WithReplace("/spec/template/spec/networks/1/multus/networkName", "non-existent-target-2"),
			).GeneratePayload()
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			_, err = kubevirt.Client().VirtualMachine(testNamespace).Patch(
				ctx, vmName, types.JSONPatchType, patchData, metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying VM remains in a recoverable state")
			Consistently(func() error {
				vmObj, err := kubevirt.Client().VirtualMachine(testNamespace).Get(ctx, vmName, metav1.GetOptions{})
				if err != nil {
					return err
				}
				// VM should still exist and be manageable
				ExpectWithOffset(1, vmObj.Name).To(Equal(vmName))
				return nil
			}, 30*time.Second, pollingInterval).Should(Succeed())

			By("Verifying migration stalls due to missing second NAD")
			Consistently(func() (*v1.VirtualMachineInstance, error) {
				return kubevirt.Client().VirtualMachineInstance(testNamespace).Get(
					ctx, vmName, metav1.GetOptions{})
			}, 30*time.Second, pollingInterval).
				Should(matcher.HaveConditionTrue(v1.VirtualMachineInstanceMigrationRequired))
		})
	})
})
