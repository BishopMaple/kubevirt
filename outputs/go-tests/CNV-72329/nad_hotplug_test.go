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
	"kubevirt.io/kubevirt/tests/libmigration"
	"kubevirt.io/kubevirt/tests/libnet"
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/libvmops"
	"kubevirt.io/kubevirt/tests/libwait"
	"kubevirt.io/kubevirt/tests/testsuite"
)

/*
NAD Reference Hotplug Tests — Core Scenarios

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Preconditions:
    - OCP 4.22+ cluster with OVN-Kubernetes CNI
    - OpenShift Virtualization 4.22+ operator installed
    - Minimum 2 schedulable worker nodes for live migration
    - Multus CNI enabled for secondary network attachment
    - LiveUpdateNADRef feature gate enabled (Beta default)
*/

var _ = Describe("[CNV-72329] NAD reference hotplug", decorators.SigNetwork, Serial, func() {
	Context("NAD reference change on running VM", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx        context.Context
			namespace  string
			err        error
			vm         *v1.VirtualMachine
			vmi        *v1.VirtualMachineInstance
			sourceNADName string
			targetNADName string
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Creating source bridge NAD")
			sourceNADName = "source-bridge-nad"
			sourceNAD := libnet.NewBridgeNetAttachDef(sourceNADName, "source-br0")
			_, err = libnet.CreateNetAttachDef(ctx, namespace, sourceNAD)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating target bridge NAD")
			targetNADName = "target-bridge-nad"
			targetNAD := libnet.NewBridgeNetAttachDef(targetNADName, "target-br0")
			_, err = libnet.CreateNetAttachDef(ctx, namespace, targetNAD)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating VM with secondary network interface on source NAD")
			vmiSpec := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", sourceNADName)),
			)
			vm = libvmops.StartVirtualMachine(vmiSpec)
			vmi = libwait.WaitUntilVMIReady(ctx, vmi, console.LoginToFedora)
		})

		/*
		Preconditions:
		    - Running VM with secondary interface on source bridge NAD

		Steps:
		    1. Patch VM spec to change networkName from source NAD to target NAD

		Expected:
		    - VM spec patch is accepted without error
		    - VM spec reflects the new NAD reference after patch
		*/
		It("[test_id:TS-CNV72329-001] should successfully change the NAD reference on a running VM", func() {
			By("Patching VM to change NAD reference to target NAD")
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "%s"}]`,
				targetNADName,
			)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying VM spec reflects new NAD reference")
			updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			var foundNetwork bool
			for _, net := range updatedVM.Spec.Template.Spec.Networks {
				if net.Multus != nil && net.Name == "secondary" {
					ExpectWithOffset(1, net.Multus.NetworkName).To(Equal(targetNADName))
					foundNetwork = true
					break
				}
			}
			ExpectWithOffset(1, foundNetwork).To(BeTrue(), "secondary network not found in VM spec")
		})

		/*
		Preconditions:
		    - Running VM with secondary interface on source bridge NAD

		Steps:
		    1. Patch VM spec to change networkName to target NAD
		    2. Continuously monitor VM phase during NAD change

		Expected:
		    - VM phase remains Running throughout NAD change operation
		    - VM is not stopped, paused, or restarted during NAD change
		*/
		It("[test_id:TS-CNV72329-002] should keep VM in Running phase during NAD reference change", func() {
			By("Patching VM to change NAD reference")
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "%s"}]`,
				targetNADName,
			)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying VM remains in Running phase throughout NAD change")
			Consistently(func() v1.VirtualMachinePrintableStatus {
				updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				return updatedVM.Status.PrintableStatus
			}, 30*time.Second, 5*time.Second).Should(Equal(v1.VirtualMachineStatusRunning))
		})

		/*
		[NEGATIVE]
		Preconditions:
		    - Running VM with secondary interface on source bridge NAD

		Steps:
		    1. Patch VM NAD reference to a non-existent NAD name
		    2. Wait for controller to evaluate the change

		Expected:
		    - VMI condition or event indicates NAD not found
		    - VM is not left in an undefined or crashed state
		*/
		It("[test_id:TS-CNV72329-003] should reject NAD change to non-existent NAD", func() {
			By("Patching VM NAD reference to non-existent NAD")
			patchData := `[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "non-existent-nad"}]`
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			// NAD change may be accepted at API level but fail at controller level

			By("Verifying VMI shows error condition for invalid NAD")
			Eventually(func() bool {
				currentVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, cond := range currentVMI.Status.Conditions {
					if cond.Reason == "NetworkNotReady" || cond.Reason == "NADNotFound" {
						return true
					}
				}
				return false
			}, 60*time.Second, 5*time.Second).Should(BeTrue(), "expected VMI condition indicating invalid NAD")

			By("Verifying VM is not in a crashed state")
			updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, updatedVM.Status.PrintableStatus).ToNot(Equal(v1.VirtualMachineStatusCrashLoopBackOff))
		})
	})

	Context("Migration triggered by NAD change", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx           context.Context
			namespace     string
			err           error
			vm            *v1.VirtualMachine
			vmi           *v1.VirtualMachineInstance
			sourceNADName string
			targetNADName string
			originalNode  string
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Creating source bridge NAD")
			sourceNADName = "source-bridge-nad"
			sourceNAD := libnet.NewBridgeNetAttachDef(sourceNADName, "source-br0")
			_, err = libnet.CreateNetAttachDef(ctx, namespace, sourceNAD)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating target bridge NAD")
			targetNADName = "target-bridge-nad"
			targetNAD := libnet.NewBridgeNetAttachDef(targetNADName, "target-br0")
			_, err = libnet.CreateNetAttachDef(ctx, namespace, targetNAD)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating VM with secondary network interface on source NAD")
			vmiSpec := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", sourceNADName)),
			)
			vm = libvmops.StartVirtualMachine(vmiSpec)
			vmi = libwait.WaitUntilVMIReady(ctx, vmi, console.LoginToFedora)

			By("Recording original node")
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			originalNode = vmi.Status.NodeName
		})

		/*
		Preconditions:
		    - Running VM with secondary interface on source bridge NAD

		Steps:
		    1. Patch VM NAD reference to target NAD
		    2. Wait for VirtualMachineInstanceMigration object to appear

		Expected:
		    - VMIM object is created with VMIName matching the VM
		    - Migration completes successfully (phase Succeeded)
		*/
		It("[test_id:TS-CNV72329-005] should trigger a live migration when NAD reference is changed", func() {
			By("Patching VM NAD reference to target NAD")
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "%s"}]`,
				targetNADName,
			)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying migration is triggered")
			Eventually(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, mig := range migrations.Items {
					if mig.Spec.VMIName == vm.Name {
						return true
					}
				}
				return false
			}, 60*time.Second, 5*time.Second).Should(BeTrue(), "expected VMIM to be created after NAD change")

			By("Waiting for migration to complete successfully")
			Eventually(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, mig := range migrations.Items {
					if mig.Spec.VMIName == vm.Name && mig.Status.Phase == v1.MigrationSucceeded {
						return true
					}
				}
				return false
			}, libmigration.MigrationWaitTime, 5*time.Second).Should(BeTrue(), "expected migration to succeed")
		})

		/*
		Preconditions:
		    - Running VM with secondary interface on source bridge NAD
		    - Original node name recorded before NAD change

		Steps:
		    1. Patch VM NAD reference to target NAD
		    2. Wait for migration to complete

		Expected:
		    - VMI runs on a different node after NAD change and migration
		*/
		It("[test_id:TS-CNV72329-006] should migrate VM to a different node after NAD reference change", func() {
			By("Patching NAD reference and waiting for migration")
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "%s"}]`,
				targetNADName,
			)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Waiting for migration to complete")
			Eventually(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, mig := range migrations.Items {
					if mig.Spec.VMIName == vm.Name && mig.Status.Phase == v1.MigrationSucceeded {
						return true
					}
				}
				return false
			}, libmigration.MigrationWaitTime, 5*time.Second).Should(BeTrue())

			By("Verifying VMI migrated to different node")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, updatedVMI.Status.NodeName).ToNot(Equal(originalNode),
				"VMI should have migrated to a different node")
		})

		/*
		Preconditions:
		    - Running VM with secondary interface on source bridge NAD

		Steps:
		    1. Patch VM NAD reference to target NAD
		    2. Check for MigrationRequired condition on VM
		    3. Wait for migration to complete

		Expected:
		    - MigrationRequired condition is set after NAD reference change
		    - MigrationRequired condition is cleared after migration completes
		*/
		It("[test_id:TS-CNV72329-007] should set and clear MigrationRequired condition during NAD change", func() {
			By("Patching VM NAD reference")
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "%s"}]`,
				targetNADName,
			)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying MigrationRequired condition is set")
			Eventually(func() k8sv1.ConditionStatus {
				updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, cond := range updatedVM.Status.Conditions {
					if cond.Type == v1.VirtualMachineMigrationRequired {
						return cond.Status
					}
				}
				return k8sv1.ConditionFalse
			}, 60*time.Second, 5*time.Second).Should(Equal(k8sv1.ConditionTrue),
				"MigrationRequired condition should be set after NAD change")

			By("Waiting for migration and verifying condition cleared")
			Eventually(func() bool {
				updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, cond := range updatedVM.Status.Conditions {
					if cond.Type == v1.VirtualMachineMigrationRequired && cond.Status == k8sv1.ConditionTrue {
						return false
					}
				}
				return true
			}, libmigration.MigrationWaitTime, 5*time.Second).Should(BeTrue(),
				"MigrationRequired condition should be cleared after migration completes")
		})
	})

	Context("Interface identity preservation after NAD hotplug", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx               context.Context
			namespace         string
			err               error
			vm                *v1.VirtualMachine
			vmi               *v1.VirtualMachineInstance
			sourceNADName     string
			targetNADName     string
			originalIfaceName string
			originalMAC       string
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Creating source bridge NAD")
			sourceNADName = "source-bridge-nad"
			sourceNAD := libnet.NewBridgeNetAttachDef(sourceNADName, "source-br0")
			_, err = libnet.CreateNetAttachDef(ctx, namespace, sourceNAD)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating target bridge NAD")
			targetNADName = "target-bridge-nad"
			targetNAD := libnet.NewBridgeNetAttachDef(targetNADName, "target-br0")
			_, err = libnet.CreateNetAttachDef(ctx, namespace, targetNAD)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Creating VM with secondary network interface on source NAD")
			vmiSpec := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", sourceNADName)),
			)
			vm = libvmops.StartVirtualMachine(vmiSpec)
			vmi = libwait.WaitUntilVMIReady(ctx, vmi, console.LoginToFedora)

			By("Recording original interface name and MAC address")
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			for _, iface := range vmi.Status.Interfaces {
				if iface.Name == "secondary" {
					originalIfaceName = iface.InterfaceName
					originalMAC = iface.MAC
					break
				}
			}
			ExpectWithOffset(1, originalIfaceName).ToNot(BeEmpty(), "secondary interface name should be recorded")
			ExpectWithOffset(1, originalMAC).ToNot(BeEmpty(), "secondary interface MAC should be recorded")
		})

		/*
		Preconditions:
		    - Running VM with secondary interface on source bridge NAD
		    - Guest interface name recorded before NAD change

		Steps:
		    1. Patch VM NAD reference to target NAD
		    2. Wait for migration to complete
		    3. Query guest interface name after migration

		Expected:
		    - Guest interface name is identical before and after NAD change
		*/
		It("[test_id:TS-CNV72329-010] should preserve guest interface name after NAD reference change and migration", func() {
			By("Patching VM NAD reference to target NAD")
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "%s"}]`,
				targetNADName,
			)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Waiting for migration to complete")
			Eventually(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, mig := range migrations.Items {
					if mig.Spec.VMIName == vm.Name && mig.Status.Phase == v1.MigrationSucceeded {
						return true
					}
				}
				return false
			}, libmigration.MigrationWaitTime, 5*time.Second).Should(BeTrue())

			By("Verifying interface name preserved after migration")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			var currentIfaceName string
			for _, iface := range updatedVMI.Status.Interfaces {
				if iface.Name == "secondary" {
					currentIfaceName = iface.InterfaceName
					break
				}
			}
			ExpectWithOffset(1, currentIfaceName).To(Equal(originalIfaceName),
				"guest interface name should be preserved after NAD change and migration")
		})

		/*
		Preconditions:
		    - Running VM with secondary interface on source bridge NAD
		    - MAC address of secondary interface recorded before NAD change

		Steps:
		    1. Patch VM NAD reference to target NAD
		    2. Wait for migration to complete
		    3. Query MAC address of secondary interface after migration

		Expected:
		    - MAC address is identical before and after NAD change
		*/
		It("[test_id:TS-CNV72329-011] should preserve MAC address after NAD reference change and migration", func() {
			By("Patching VM NAD reference to target NAD")
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "%s"}]`,
				targetNADName,
			)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Waiting for migration to complete")
			Eventually(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, mig := range migrations.Items {
					if mig.Spec.VMIName == vm.Name && mig.Status.Phase == v1.MigrationSucceeded {
						return true
					}
				}
				return false
			}, libmigration.MigrationWaitTime, 5*time.Second).Should(BeTrue())

			By("Verifying MAC address preserved after migration")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			var currentMAC string
			for _, iface := range updatedVMI.Status.Interfaces {
				if iface.Name == "secondary" {
					currentMAC = iface.MAC
					break
				}
			}
			ExpectWithOffset(1, currentMAC).To(Equal(originalMAC),
				"MAC address should be preserved after NAD change and migration")
		})
	})
})
