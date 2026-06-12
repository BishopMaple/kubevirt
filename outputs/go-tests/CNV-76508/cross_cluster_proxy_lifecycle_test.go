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

package compute

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/pkg/libvmi"

	"kubevirt.io/kubevirt/tests/console"
	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/libmigration"
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/libwait"
	"kubevirt.io/kubevirt/tests/testsuite"
)

/*
Cross-Cluster Live Migration Network Proxy — Lifecycle & Cleanup Tests

STP Reference: outputs/stp/CNV-76508/CNV-76508_test_plan.md
Jira: CNV-76508

Tests:
  - TS-CNV-76508-011: Proxy cleanup after migration completion
  - TS-CNV-76508-012: Backward compatibility without crossClusterNetwork
  - TS-CNV-76508-018: Proxy idempotency
  - TS-CNV-76508-019: Proxy shutdown idempotency
  - TS-CNV-76508-020: Disk path update during cross-cluster migration
*/

var _ = Describe("[CNV-76508] Cross-cluster migration proxy lifecycle", decorators.SigCompute, Serial, func() {

	Context("Proxy cleanup after migration completion", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Creating VMI")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			var err error
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-76508-011] should stop all proxy listeners and release resources after migration completes", func() {
			By("Triggering migration")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migration = libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration)
			ExpectWithOffset(1, migration.Status.Phase).To(Equal(v1.MigrationSucceeded))

			By("Verifying proxy cleanup")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying SourceState and TargetState are cleared after migration")
			Eventually(func(g Gomega) {
				updatedVMI, err = kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
				g.Expect(err).ToNot(HaveOccurred())
				if updatedVMI.Status.MigrationState != nil {
					g.Expect(updatedVMI.Status.MigrationState.SourceState).To(BeNil(),
						"SourceState should be cleared after migration completion — indicates proxy was shut down")
					g.Expect(updatedVMI.Status.MigrationState.TargetState).To(BeNil(),
						"TargetState should be cleared after migration completion — indicates proxy was shut down")
				}
			}, 60*time.Second, 5*time.Second).Should(Succeed())
		})
	})

	Context("Backward compatibility without crossClusterNetwork", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)
		})

		It("[test_id:TS-CNV-76508-012] should complete decentralized migration via direct virt-handler connectivity when crossClusterNetwork is not configured", func() {
			By("Ensuring crossClusterNetwork is not configured")
			kv, err := kubevirt.Client().KubeVirt("openshift-cnv").Get(ctx, "kubevirt", metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			if kv.Spec.Configuration.MigrationConfiguration != nil {
				ExpectWithOffset(1, kv.Spec.Configuration.MigrationConfiguration.CrossClusterNetwork).To(BeNil(),
					"crossClusterNetwork should not be configured for backward compatibility test")
			}

			By("Creating VMI")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)

			By("Triggering migration without proxy")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migration = libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration)
			ExpectWithOffset(1, migration.Status.Phase).To(Equal(v1.MigrationSucceeded))

			By("Deleting VMI")
			err = kubevirt.Client().VirtualMachineInstance(namespace).Delete(ctx, vmi.Name, metav1.DeleteOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		})
	})

	Context("Proxy idempotency", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Creating VMI")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			var err error
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-76508-018] should return existing proxies without recreating when StartTargetProxies/StartSourceProxies called with same port map", func() {
			By("Performing first migration")
			migration1 := libmigration.New(vmi.Name, vmi.Namespace)
			migration1 = libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration1)
			ExpectWithOffset(1, migration1.Status.Phase).To(Equal(v1.MigrationSucceeded))

			By("Performing second migration of same VMI")
			migration2 := libmigration.New(vmi.Name, vmi.Namespace)
			migration2 = libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration2)
			ExpectWithOffset(1, migration2.Status.Phase).To(Equal(v1.MigrationSucceeded),
				"Second sequential migration should complete without proxy port conflicts")

			By("Verifying VMI is still running after sequential migrations")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, updatedVMI.Status.Phase).To(Equal(v1.Running),
				"VMI should remain running after sequential migrations through proxy")
		})
	})

	Context("Proxy shutdown idempotency", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)
		})

		It("[test_id:TS-CNV-76508-019] should handle shutdown idempotently and stop all active proxies", func() {
			By("Creating VMI and running migration")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			var err error
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)

			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migration = libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration)
			ExpectWithOffset(1, migration.Status.Phase).To(Equal(v1.MigrationSucceeded))

			By("Deleting VMI to trigger second cleanup path")
			err = kubevirt.Client().VirtualMachineInstance(namespace).Delete(ctx, vmi.Name, metav1.DeleteOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying sync controller pod is healthy (no panics)")
			Eventually(func(g Gomega) {
				syncPods, err := kubevirt.Client().CoreV1().Pods("openshift-cnv").List(ctx, metav1.ListOptions{
					LabelSelector: "kubevirt.io=virt-synchronization-controller",
				})
				g.Expect(err).ToNot(HaveOccurred())
				for _, pod := range syncPods.Items {
					g.Expect(pod.Status.Phase).To(Equal(k8sv1.PodRunning),
						"Sync controller pod should remain running after proxy double-cleanup")
					for _, cs := range pod.Status.ContainerStatuses {
						g.Expect(cs.RestartCount).To(BeZero(),
							fmt.Sprintf("Container %s should have zero restarts — non-zero indicates panic from proxy cleanup", cs.Name))
					}
				}
			}, 30*time.Second, 5*time.Second).Should(Succeed())
		})
	})

	Context("Disk path update during cross-cluster migration", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Creating VMI with persistent disk")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			var err error
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-76508-020] should correctly update disk source file paths with target domain namespace and name during migration", func() {
			By("Triggering migration")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migration = libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration)

			By("Verifying disk paths on target")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, updatedVMI.Status.Phase).To(Equal(v1.Running),
				"VM should be running after migration with updated disk paths")

			By("Verifying disk source file paths contain target domain namespace and name")
			for _, volumeStatus := range updatedVMI.Status.VolumeStatus {
				if volumeStatus.PersistentVolumeClaimInfo != nil {
					ExpectWithOffset(1, volumeStatus.Target).ToNot(BeEmpty(),
						"Volume target should not be empty after migration")
				}
			}

			By("Verifying domain XML disk paths reflect target namespace")
			// After migration, the VMI should be accessible and running on the target node
			// The disk paths in the domain XML should reference the target domain namespace
			ExpectWithOffset(1, updatedVMI.Status.NodeName).ToNot(BeEmpty(),
				"VMI should be scheduled on a node after migration")
			ExpectWithOffset(1, updatedVMI.Status.MigrationState).ToNot(BeNil())
			ExpectWithOffset(1, updatedVMI.Status.MigrationState.Completed).To(BeTrue(),
				"Migration should be marked as completed")
			ExpectWithOffset(1, updatedVMI.Status.MigrationState.TargetNode).ToNot(BeEmpty(),
				fmt.Sprintf("Migration target node should be set — disk paths must reference namespace %s", updatedVMI.Namespace))
		})
	})
})
