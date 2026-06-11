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
Live Update NAD Reference Tests - Edge Cases

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329
*/

var _ = Describe("[CNV-72329] Live Update NAD Reference - Edge Cases", decorators.SigNetwork, Serial, func() {
	const (
		nadNameA = "nad-edge-vlan100"
		nadNameB = "nad-edge-vlan200"
	)

	Context("Pod network not affected by syncNetworks", Ordered, decorators.OncePerOrderedCleanup, func() {
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

			By("Creating VM with pod network + secondary interface attached to NAD-A")
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

		It("[test_id:TS-CNV-72329-015] should not modify pod network configuration during syncNetworks", func() {
			By("Recording pod network configuration from VMI spec")
			originalPodNetwork := vmi.Spec.Networks[0]
			Expect(originalPodNetwork.Pod).ToNot(BeNil(), "First network should be pod network")

			originalPodInterface := findInterfaceByName(vmi.Spec.Domain.Devices.Interfaces, "default")
			Expect(originalPodInterface).ToNot(BeNil(), "Default interface should exist")

			By("Changing secondary interface NAD from NAD-A to NAD-B")
			patchData := fmt.Sprintf(`[{"op":"replace","path":"/spec/template/spec/networks/1/multus/networkName","value":"%s"}]`, nadNameB)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Waiting for migration to complete")
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

			By("Verifying pod network configuration is unchanged after NAD change")
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			Expect(vmi.Spec.Networks[0].Name).To(Equal(originalPodNetwork.Name))
			Expect(vmi.Spec.Networks[0].Pod).ToNot(BeNil(), "Pod network should still be present")

			postChangePodInterface := findInterfaceByName(vmi.Spec.Domain.Devices.Interfaces, "default")
			Expect(postChangePodInterface).ToNot(BeNil(), "Default interface should still exist")
			Expect(postChangePodInterface.Masquerade).ToNot(BeNil(), "Masquerade binding should be unchanged")
		})
	})

	Context("NAD reference change with ordinal interface naming", Ordered, decorators.OncePerOrderedCleanup, func() {
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

			By("Creating VM with ordinal interface naming scheme enabled")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(*v1.DefaultMasqueradeNetworkInterface()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", nadNameA)),
				libvmi.WithAutoAttachNetworkInterfaceToOrdinalName(),
			)
			vm = libvmi.NewVirtualMachine(vmi)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Create(ctx, vm, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			vm = libvmops.StartVirtualMachine(vm)

			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-72329-020] should correctly handle NAD reference change with ordinal interface naming scheme", func() {
			By("Changing NAD reference on ordinally-named interface")
			patchData := fmt.Sprintf(`[{"op":"replace","path":"/spec/template/spec/networks/1/multus/networkName","value":"%s"}]`, nadNameB)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying VMI spec updated with correct ordinal interface mapping")
			Eventually(func() string {
				vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				return vmi.Spec.Networks[1].Multus.NetworkName
			}, 30*time.Second, 5*time.Second).Should(Equal(nadNameB))

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

// findInterfaceByName returns the interface with the given name, or nil if not found.
func findInterfaceByName(interfaces []v1.Interface, name string) *v1.Interface {
	for i := range interfaces {
		if interfaces[i].Name == name {
			return &interfaces[i]
		}
	}
	return nil
}
