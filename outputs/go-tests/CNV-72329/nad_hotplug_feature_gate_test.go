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
	"kubevirt.io/kubevirt/pkg/virt-config/featuregate"

	"kubevirt.io/kubevirt/tests/console"
	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/libkubevirt/config"
	"kubevirt.io/kubevirt/tests/libmigration"
	"kubevirt.io/kubevirt/tests/libnet"
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/libvmops"
	"kubevirt.io/kubevirt/tests/libwait"
	"kubevirt.io/kubevirt/tests/testsuite"
)

/*
NAD Reference Hotplug Tests — Feature Gate and Edge Cases

STP Reference: outputs/stp/CNV-72329/CNV-72329_test_plan.md
Jira: CNV-72329

Preconditions:
    - OCP 4.22+ cluster with OVN-Kubernetes CNI
    - OpenShift Virtualization 4.22+ operator installed
    - Minimum 2 schedulable worker nodes for live migration
    - Multus CNI enabled for secondary network attachment
    - Two bridge-type NADs (source and target) deployed in test namespace
*/

var _ = Describe("[CNV-72329] NAD reference hotplug feature gate behavior", decorators.SigNetwork, Serial, func() {
	Context("LiveUpdateNADRef feature gate controls", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx           context.Context
			namespace     string
			err           error
			vm            *v1.VirtualMachine
			vmi           *v1.VirtualMachineInstance
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
		})

		/*
		Preconditions:
		    - LiveUpdateNADRef feature gate explicitly enabled
		    - Running VM with secondary interface on source bridge NAD

		Steps:
		    1. Patch VM NAD reference to target NAD
		    2. Check for migration trigger vs restart condition

		Expected:
		    - NAD change triggers migration (not restart) when feature gate is enabled
		    - No RestartRequired condition set
		*/
		It("[test_id:TS-CNV72329-012] should perform live NAD update when LiveUpdateNADRef feature gate is enabled", func() {
			By("Ensuring LiveUpdateNADRef feature gate is enabled")
			config.EnableFeatureGate(featuregate.LiveUpdateNADRef)

			By("Creating VM with secondary network interface on source NAD")
			vmiSpec := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", sourceNADName)),
			)
			vm = libvmops.StartVirtualMachine(vmiSpec)
			vmi = libwait.WaitUntilVMIReady(ctx, vmi, console.LoginToFedora)

			By("Patching VM NAD reference to target NAD")
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "%s"}]`,
				targetNADName,
			)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying migration is triggered (not restart)")
			Eventually(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, mig := range migrations.Items {
					if mig.Spec.VMIName == vm.Name {
						return true
					}
				}
				return false
			}, 60*time.Second, 5*time.Second).Should(BeTrue(), "migration should be triggered when feature gate is enabled")

			By("Verifying no RestartRequired condition is set")
			updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			for _, cond := range updatedVM.Status.Conditions {
				if cond.Type == v1.VirtualMachineRestartRequired {
					ExpectWithOffset(1, cond.Status).To(Equal(k8sv1.ConditionFalse),
						"RestartRequired should not be set when feature gate is enabled")
				}
			}
		})

		/*
		[NEGATIVE]
		Preconditions:
		    - LiveUpdateNADRef feature gate explicitly disabled
		    - Running VM with secondary interface on source bridge NAD

		Steps:
		    1. Patch VM NAD reference to target NAD
		    2. Check for RestartRequired condition

		Expected:
		    - RestartRequired condition is set when feature gate is disabled
		    - No migration is triggered
		*/
		It("[test_id:TS-CNV72329-013] should require VM restart for NAD change when LiveUpdateNADRef is disabled", func() {
			By("Disabling LiveUpdateNADRef feature gate")
			config.DisableFeatureGate(featuregate.LiveUpdateNADRef)

			By("Creating VM with secondary network interface on source NAD")
			vmiSpec := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", sourceNADName)),
			)
			vm = libvmops.StartVirtualMachine(vmiSpec)
			vmi = libwait.WaitUntilVMIReady(ctx, vmi, console.LoginToFedora)

			By("Patching VM NAD reference to target NAD")
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "%s"}]`,
				targetNADName,
			)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying RestartRequired condition is set")
			Eventually(func() k8sv1.ConditionStatus {
				updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, cond := range updatedVM.Status.Conditions {
					if cond.Type == v1.VirtualMachineRestartRequired {
						return cond.Status
					}
				}
				return k8sv1.ConditionFalse
			}, 60*time.Second, 5*time.Second).Should(Equal(k8sv1.ConditionTrue),
				"RestartRequired condition should be set when feature gate is disabled")

			By("Verifying no migration is triggered")
			Consistently(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, mig := range migrations.Items {
					if mig.Spec.VMIName == vm.Name {
						return true
					}
				}
				return false
			}, 30*time.Second, 5*time.Second).Should(BeFalse(),
				"no migration should be triggered when feature gate is disabled")

			By("Re-enabling LiveUpdateNADRef feature gate for subsequent tests")
			config.EnableFeatureGate(featuregate.LiveUpdateNADRef)
		})
	})

	Context("NAD-only vs structural changes", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx           context.Context
			namespace     string
			err           error
			vm            *v1.VirtualMachine
			vmi           *v1.VirtualMachineInstance
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
		})

		/*
		Preconditions:
		    - LiveUpdateNADRef feature gate enabled (default)
		    - Running VM with secondary interface on source bridge NAD

		Steps:
		    1. Patch only the networkName field (no binding or interface changes)
		    2. Check for migration trigger vs restart condition

		Expected:
		    - NAD name-only change triggers migration, not restart
		    - No RestartRequired condition set
		*/
		It("[test_id:TS-CNV72329-014] should not require restart when only NAD name changes", func() {
			By("Creating VM with secondary bridge interface on source NAD")
			vmiSpec := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", sourceNADName)),
			)
			vm = libvmops.StartVirtualMachine(vmiSpec)
			vmi = libwait.WaitUntilVMIReady(ctx, vmi, console.LoginToFedora)

			By("Patching only the NAD name (no binding or interface changes)")
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "%s"}]`,
				targetNADName,
			)
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying migration is triggered (not restart required)")
			Eventually(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, mig := range migrations.Items {
					if mig.Spec.VMIName == vm.Name {
						return true
					}
				}
				return false
			}, 60*time.Second, 5*time.Second).Should(BeTrue(), "NAD-only change should trigger migration")

			By("Verifying no RestartRequired condition")
			updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			for _, cond := range updatedVM.Status.Conditions {
				if cond.Type == v1.VirtualMachineRestartRequired {
					ExpectWithOffset(1, cond.Status).To(Equal(k8sv1.ConditionFalse),
						"RestartRequired should not be set for NAD-only change")
				}
			}
		})

		/*
		[NEGATIVE]
		Preconditions:
		    - Running VM with bridge binding on secondary interface

		Steps:
		    1. Patch VM to change interface binding type (e.g., bridge to masquerade)
		    2. Check for RestartRequired condition

		Expected:
		    - Interface binding change sets RestartRequired condition
		    - No migration triggered for binding changes
		*/
		It("[test_id:TS-CNV72329-015] should require restart when interface binding type changes", func() {
			By("Creating VM with secondary bridge interface")
			vmiSpec := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", sourceNADName)),
			)
			vm = libvmops.StartVirtualMachine(vmiSpec)
			vmi = libwait.WaitUntilVMIReady(ctx, vmi, console.LoginToFedora)

			By("Changing interface binding type from bridge to masquerade")
			patchData := `[{"op": "replace", "path": "/spec/template/spec/domain/devices/interfaces/1/bridge", "value": null}, {"op": "add", "path": "/spec/template/spec/domain/devices/interfaces/1/masquerade", "value": {}}]`
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying RestartRequired condition is set for binding change")
			Eventually(func() k8sv1.ConditionStatus {
				updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, cond := range updatedVM.Status.Conditions {
					if cond.Type == v1.VirtualMachineRestartRequired {
						return cond.Status
					}
				}
				return k8sv1.ConditionFalse
			}, 60*time.Second, 5*time.Second).Should(Equal(k8sv1.ConditionTrue),
				"RestartRequired should be set for interface binding change")

			By("Verifying no migration triggered for binding changes")
			Consistently(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, mig := range migrations.Items {
					if mig.Spec.VMIName == vm.Name {
						return true
					}
				}
				return false
			}, 30*time.Second, 5*time.Second).Should(BeFalse(),
				"no migration should be triggered for binding type changes")
		})

		/*
		[NEGATIVE]
		Preconditions:
		    - Running VM with secondary network interface

		Steps:
		    1. Remove secondary network from VM spec entirely
		    2. Check for RestartRequired condition

		Expected:
		    - Network removal sets RestartRequired condition
		    - No migration triggered for structural network changes
		*/
		It("[test_id:TS-CNV72329-016] should require restart when a network interface is removed", func() {
			By("Creating VM with secondary network interface")
			vmiSpec := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", sourceNADName)),
			)
			vm = libvmops.StartVirtualMachine(vmiSpec)
			vmi = libwait.WaitUntilVMIReady(ctx, vmi, console.LoginToFedora)

			By("Removing secondary network from VM spec")
			patchData := `[{"op": "remove", "path": "/spec/template/spec/networks/1"}, {"op": "remove", "path": "/spec/template/spec/domain/devices/interfaces/1"}]`
			vm, err = kubevirt.Client().VirtualMachine(namespace).Patch(ctx, vm.Name, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying RestartRequired condition is set for network removal")
			Eventually(func() k8sv1.ConditionStatus {
				updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, cond := range updatedVM.Status.Conditions {
					if cond.Type == v1.VirtualMachineRestartRequired {
						return cond.Status
					}
				}
				return k8sv1.ConditionFalse
			}, 60*time.Second, 5*time.Second).Should(Equal(k8sv1.ConditionTrue),
				"RestartRequired should be set for network interface removal")

			By("Verifying no migration triggered for structural changes")
			Consistently(func() bool {
				migrations, err := kubevirt.Client().VirtualMachineInstanceMigration(namespace).List(ctx, metav1.ListOptions{})
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				for _, mig := range migrations.Items {
					if mig.Spec.VMIName == vm.Name {
						return true
					}
				}
				return false
			}, 30*time.Second, 5*time.Second).Should(BeFalse(),
				"no migration should be triggered for network removal")
		})
	})

	Context("NAD name resolution formats", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx           context.Context
			namespace     string
			err           error
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
		})

		/*
		Preconditions:
		    - Running VM with secondary interface referencing source NAD
		    - Target NAD available in same namespace

		Steps:
		    1. Patch VM NAD reference using namespace-qualified format (namespace/nad-name)
		    2. Wait for migration to complete

		Expected:
		    - NAD change with namespace/name format triggers migration successfully
		    - Migration completes and VM is functional
		*/
		It("[test_id:TS-CNV72329-017] should handle NAD change with namespace-qualified NAD name", func() {
			By("Creating VM with secondary network interface on source NAD")
			vmiSpec := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", sourceNADName)),
			)
			vm := libvmops.StartVirtualMachine(vmiSpec)
			vmi := libwait.WaitUntilVMIReady(ctx, vmiSpec, console.LoginToFedora)
			_ = vmi

			By("Patching VM NAD reference using namespace-qualified format")
			qualifiedNADName := fmt.Sprintf("%s/%s", namespace, targetNADName)
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/template/spec/networks/1/multus/networkName", "value": "%s"}]`,
				qualifiedNADName,
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
			}, libmigration.MigrationWaitTime, 5*time.Second).Should(BeTrue(),
				"migration should complete for namespace-qualified NAD name")

			By("Verifying VM is functional after migration")
			updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, updatedVM.Status.PrintableStatus).To(Equal(v1.VirtualMachineStatusRunning))
		})

		/*
		Preconditions:
		    - Running VM with secondary interface referencing source NAD
		    - Target NAD available in same namespace

		Steps:
		    1. Patch VM NAD reference using unqualified NAD name (no namespace prefix)
		    2. Wait for migration to complete

		Expected:
		    - NAD change with unqualified name triggers migration successfully
		    - Migration completes and VM is functional
		*/
		It("[test_id:TS-CNV72329-018] should handle NAD change with unqualified NAD name", func() {
			By("Creating VM with secondary network interface on source NAD")
			vmiSpec := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithBridgeBinding("secondary")),
				libvmi.WithNetwork(libvmi.MultusNetwork("secondary", sourceNADName)),
			)
			vm := libvmops.StartVirtualMachine(vmiSpec)
			vmi := libwait.WaitUntilVMIReady(ctx, vmiSpec, console.LoginToFedora)
			_ = vmi

			By("Patching VM NAD reference using unqualified name")
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
			}, libmigration.MigrationWaitTime, 5*time.Second).Should(BeTrue(),
				"migration should complete for unqualified NAD name")

			By("Verifying VM is functional after migration")
			updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, updatedVM.Status.PrintableStatus).To(Equal(v1.VirtualMachineStatusRunning))
		})
	})

	Context("Pod-network-only VM", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			err       error
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)
		})

		/*
		[NEGATIVE]
		Preconditions:
		    - LiveUpdateNADRef feature gate enabled
		    - Running VM with only pod network (no secondary interfaces)

		Steps:
		    1. Verify VM status conditions
		    2. Verify VM connectivity via pod network

		Expected:
		    - No NAD-related conditions (MigrationRequired, RestartRequired) on pod-network-only VM
		    - VM functions normally with feature gate enabled
		*/
		It("[test_id:TS-CNV72329-021] should not affect VM with only pod network (no secondary interfaces)", func() {
			By("Creating VM with only pod network (no secondary interfaces)")
			vmiSpec := libvmifact.NewFedora(
				libvmi.WithInterface(*v1.DefaultMasqueradeNetworkInterface()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			vm := libvmops.StartVirtualMachine(vmiSpec)
			vmi := libwait.WaitUntilVMIReady(ctx, vmiSpec, console.LoginToFedora)
			_ = vmi

			By("Verifying no NAD-related conditions on pod-network-only VM")
			updatedVM, err := kubevirt.Client().VirtualMachine(namespace).Get(ctx, vm.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			for _, cond := range updatedVM.Status.Conditions {
				if cond.Type == v1.VirtualMachineMigrationRequired {
					ExpectWithOffset(1, cond.Status).To(Equal(k8sv1.ConditionFalse),
						"MigrationRequired should not be set on pod-network-only VM")
				}
				if cond.Type == v1.VirtualMachineRestartRequired {
					ExpectWithOffset(1, cond.Status).To(Equal(k8sv1.ConditionFalse),
						"RestartRequired should not be set on pod-network-only VM")
				}
			}

			By("Verifying VM is running and functional")
			ExpectWithOffset(1, updatedVM.Status.PrintableStatus).To(Equal(v1.VirtualMachineStatusRunning),
				"pod-network-only VM should function normally with feature gate enabled")
		})
	})
})
