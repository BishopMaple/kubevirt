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
	"kubevirt.io/kubevirt/tests/libnet"
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/libvmops"
	"kubevirt.io/kubevirt/tests/libwait"
	"kubevirt.io/kubevirt/tests/testsuite"
)

/*
Live Update NAD Reference Tests - Core Behavior

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
*/

var _ = Describe("[CNV-72329] Live Update NAD Reference", decorators.SigNetwork, Serial, func() {
	const (
		nadNameA = "nad-vlan100"
		nadNameB = "nad-vlan200"
	)

	Context("NAD reference change on running VM triggers VMI sync and migration", Ordered, decorators.OncePerOrderedCleanup, func() {
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

			By("Creating NAD-A with VLAN 100")
			nadA := libnet.NewBridgeNetAttachDef(namespace, nadNameA)
			_, err = kubevirt.Client().NetworkClient().NetworkAttachmentDefinitions(namespace).Create(ctx, nadA, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating NAD-B with VLAN 200")
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

			By("Starting VM and waiting for it to be Running")
			vm = libvmops.StartVirtualMachine(vm)

			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-72329-001] should trigger VMI spec sync and automatic live migration when NAD reference is changed", func() {
			By("Updating VM network reference from NAD-A to NAD-B")
			patchData := fmt.Sprintf(`[{"op":"replace","path":"/spec/template/spec/networks/1/multus/networkName","value":"%s"}]`, nadNameB)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying VMI spec reflects new NAD reference")
			Eventually(func() string {
				vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				return vmi.Spec.Networks[1].Multus.NetworkName
			}, 30*time.Second, 5*time.Second).Should(Equal(nadNameB))

			By("Verifying migration is triggered and completes")
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

	Context("VMI spec networks updated to reflect new NAD reference", Ordered, decorators.OncePerOrderedCleanup, func() {
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

		It("[test_id:TS-CNV-72329-006] should update VMI spec networks to match VM spec after NAD reference change", func() {
			By("Recording current VMI network reference")
			Expect(vmi.Spec.Networks[1].Multus.NetworkName).To(Equal(nadNameA))

			By("Changing NAD reference on VM spec")
			patchData := fmt.Sprintf(`[{"op":"replace","path":"/spec/template/spec/networks/1/multus/networkName","value":"%s"}]`, nadNameB)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying VMI spec networks are updated")
			Eventually(func() string {
				vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				return vmi.Spec.Networks[1].Multus.NetworkName
			}, 30*time.Second, 5*time.Second).Should(Equal(nadNameB))
		})
	})

	Context("Migration evaluator triggers immediate migration for NAD mismatch", Ordered, decorators.OncePerOrderedCleanup, func() {
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

		It("[test_id:TS-CNV-72329-007] should trigger immediate migration when NAD reference differs between VMI spec and pod network status", func() {
			By("Changing NAD reference from NAD-A to NAD-B")
			patchData := fmt.Sprintf(`[{"op":"replace","path":"/spec/template/spec/networks/1/multus/networkName","value":"%s"}]`, nadNameB)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying migration is created promptly after VMI spec update")
			Eventually(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, m := range migrations.Items {
					if m.Spec.VMIName == vm.Name {
						return true
					}
				}
				return false
			}, 60*time.Second, 5*time.Second).Should(BeTrue())

			By("Verifying migration completes successfully")
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
})

// Compile-time interface satisfaction checks
var _ = k8sv1.Pod{}
