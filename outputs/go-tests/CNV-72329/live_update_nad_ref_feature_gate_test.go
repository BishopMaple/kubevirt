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

	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/pkg/libvmi"

	"kubevirt.io/kubevirt/tests/console"
	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/libkubevirt/config"
	"kubevirt.io/kubevirt/tests/libnet"
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/libvmops"
	"kubevirt.io/kubevirt/tests/libwait"
	"kubevirt.io/kubevirt/tests/testsuite"
)

/*
Live Update NAD Reference Tests - Feature Gate Control

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
*/

const liveUpdateNADRefFeatureGate = "LiveUpdateNADRef"

var _ = Describe("[CNV-72329] Live Update NAD Reference - Feature Gate", decorators.SigNetwork, Serial, func() {
	const (
		nadNameA = "nad-fg-vlan100"
		nadNameB = "nad-fg-vlan200"
	)

	Context("LiveUpdateNADRef FG enabled allows NAD change without restart", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			err       error
			vm        *v1.VirtualMachine
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Verifying LiveUpdateNADRef feature gate is enabled")
			config.EnableFeatureGate(liveUpdateNADRefFeatureGate)

			By("Creating NAD-A")
			nadA := libnet.NewBridgeNetAttachDef(namespace, nadNameA)
			_, err = kubevirt.Client().NetworkClient().NetworkAttachmentDefinitions(namespace).Create(ctx, nadA, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating NAD-B")
			nadB := libnet.NewBridgeNetAttachDef(namespace, nadNameB)
			_, err = kubevirt.Client().NetworkClient().NetworkAttachmentDefinitions(namespace).Create(ctx, nadB, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating VM with secondary interface attached to NAD-A")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(*v1.DefaultMasqueradeNetworkInterface()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", nadNameA)),
			)
			vm = libvmi.NewVirtualMachine(vmi)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Create(ctx, vm, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			vm = libvmops.StartVirtualMachine(vm)

			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-72329-002] should allow NAD reference change without requiring VM restart when FG is enabled", func() {
			By("Changing NAD reference on running VM from NAD-A to NAD-B")
			patchData := fmt.Sprintf(`[{"op":"replace","path":"/spec/template/spec/networks/1/multus/networkName","value":"%s"}]`, nadNameB)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying VM does not require restart")
			Consistently(func() bool {
				vm, err = kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, cond := range vm.Status.Conditions {
					if cond.Type == v1.VirtualMachineRestartRequired && cond.Status == k8sv1.ConditionTrue {
						return true
					}
				}
				return false
			}, 30*time.Second, 5*time.Second).Should(BeFalse())

			By("Verifying NAD change is applied via automatic migration")
			Eventually(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, m := range migrations.Items {
					if m.Spec.VMIName == vm.Name && m.Status.Phase == v1.MigrationSucceeded {
						return true
					}
				}
				return false
			}, 120*time.Second, 5*time.Second).Should(BeTrue())
		})
	})

	Context("[NEGATIVE] LiveUpdateNADRef FG disabled requires VM restart for NAD change", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			err       error
			vm        *v1.VirtualMachine
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Ensuring LiveUpdateNADRef feature gate is disabled")
			config.DisableFeatureGate(liveUpdateNADRefFeatureGate)

			By("Creating NAD-A")
			nadA := libnet.NewBridgeNetAttachDef(namespace, nadNameA)
			_, err = kubevirt.Client().NetworkClient().NetworkAttachmentDefinitions(namespace).Create(ctx, nadA, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating NAD-B")
			nadB := libnet.NewBridgeNetAttachDef(namespace, nadNameB)
			_, err = kubevirt.Client().NetworkClient().NetworkAttachmentDefinitions(namespace).Create(ctx, nadB, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating VM with secondary interface attached to NAD-A")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(*v1.DefaultMasqueradeNetworkInterface()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", nadNameA)),
			)
			vm = libvmi.NewVirtualMachine(vmi)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Create(ctx, vm, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			vm = libvmops.StartVirtualMachine(vm)

			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-72329-003] should require VM restart for NAD change when LiveUpdateNADRef FG is disabled", func() {
			By("Changing NAD reference on running VM from NAD-A to NAD-B")
			patchData := fmt.Sprintf(`[{"op":"replace","path":"/spec/template/spec/networks/1/multus/networkName","value":"%s"}]`, nadNameB)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying VM requires restart")
			Eventually(func() bool {
				vm, err = kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, cond := range vm.Status.Conditions {
					if cond.Type == v1.VirtualMachineRestartRequired && cond.Status == k8sv1.ConditionTrue {
						return true
					}
				}
				return false
			}, 30*time.Second, 5*time.Second).Should(BeTrue())

			By("Verifying no automatic migration was triggered")
			Consistently(func() int {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				count := 0
				for _, m := range migrations.Items {
					if m.Spec.VMIName == vm.Name {
						count++
					}
				}
				return count
			}, 30*time.Second, 5*time.Second).Should(Equal(0))
		})
	})

	Context("IsRestartRequired returns false for NAD-only changes with FG enabled", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			err       error
			vm        *v1.VirtualMachine
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Ensuring LiveUpdateNADRef feature gate is enabled")
			config.EnableFeatureGate(liveUpdateNADRefFeatureGate)

			By("Creating NAD-A")
			nadA := libnet.NewBridgeNetAttachDef(namespace, nadNameA)
			_, err = kubevirt.Client().NetworkClient().NetworkAttachmentDefinitions(namespace).Create(ctx, nadA, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating NAD-B")
			nadB := libnet.NewBridgeNetAttachDef(namespace, nadNameB)
			_, err = kubevirt.Client().NetworkClient().NetworkAttachmentDefinitions(namespace).Create(ctx, nadB, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating VM with secondary interface attached to NAD-A")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(*v1.DefaultMasqueradeNetworkInterface()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", nadNameA)),
			)
			vm = libvmi.NewVirtualMachine(vmi)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Create(ctx, vm, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			vm = libvmops.StartVirtualMachine(vm)

			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-72329-014] should not set RestartRequired for NAD-only changes when FG is enabled", func() {
			By("Changing only the NAD reference on VM (no other spec changes)")
			patchData := fmt.Sprintf(`[{"op":"replace","path":"/spec/template/spec/networks/1/multus/networkName","value":"%s"}]`, nadNameB)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying VM does not receive RestartRequired condition for 30 seconds")
			Consistently(func() bool {
				vm, err = kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, cond := range vm.Status.Conditions {
					if cond.Type == v1.VirtualMachineRestartRequired && cond.Status == k8sv1.ConditionTrue {
						return true
					}
				}
				return false
			}, 30*time.Second, 5*time.Second).Should(BeFalse())
		})
	})
})
