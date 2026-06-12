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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
Cross-Cluster Live Migration Network Proxy — Migration Flow Tests

STP Reference: outputs/stp/CNV-76508/CNV-76508_test_plan.md
Jira: CNV-76508

Tests:
  - TS-CNV-76508-002: VM live migration through proxy
  - TS-CNV-76508-003: Source proxy listener creation on migration0
  - TS-CNV-76508-004: Target proxy listener creation on crosscluster0
  - TS-CNV-76508-005: Proxy port map propagation in VMI migration status
*/

var _ = Describe("[CNV-76508] Cross-cluster migration proxy migration flow", decorators.SigCompute, Serial, func() {

	Context("VM live migration through proxy", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Creating Fedora VMI")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			var err error
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Waiting for VMI to be ready")
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-76508-002] should complete VM live migration successfully through proxy with libvirt and NBD channels", func() {
			By("Triggering live migration")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migration = libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration)

			By("Verifying migration completed through proxy")
			ExpectWithOffset(1, migration.Status.Phase).To(Equal(v1.MigrationSucceeded))

			By("Verifying proxy was used by checking VMI migration state")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, updatedVMI.Status.MigrationState).ToNot(BeNil(),
				"VMI should have migration state after successful migration")
			ExpectWithOffset(1, updatedVMI.Status.MigrationState.Completed).To(BeTrue(),
				"Migration state should indicate completion")
		})
	})

	Context("Source proxy listener creation on migration0", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Creating migration-ready VMI")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			var err error
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-76508-003] should create source proxy listeners on migration0 IP with OS-allocated ports forwarding to target proxy on crosscluster0", func() {
			By("Triggering migration")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migration = libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration)

			By("Inspecting VMI migration state for source proxy port map")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, updatedVMI.Status.MigrationState).ToNot(BeNil(),
				"VMI should have migration state")

			By("Verifying source proxy port map exists with OS-allocated ports")
			migrationState := updatedVMI.Status.MigrationState
			ExpectWithOffset(1, migrationState.SourceState).ToNot(BeNil(),
				"Migration state should contain SourceState with proxy port information")
			ExpectWithOffset(1, migrationState.SourceState.PortMap).ToNot(BeEmpty(),
				"Source proxy port map should not be empty")

			By("Verifying ports are OS-allocated (non-zero, dynamic)")
			for port, mappedPort := range migrationState.SourceState.PortMap {
				ExpectWithOffset(1, mappedPort).ToNot(BeZero(),
					"Port %d should have a non-zero OS-allocated proxy port", port)
			}
		})
	})

	Context("Target proxy listener creation on crosscluster0", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Creating migration-ready VMI")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			var err error
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-76508-004] should create target proxy listeners on crosscluster0 IP forwarding to target virt-handler ports", func() {
			By("Triggering migration")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migration = libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration)

			By("Inspecting target proxy state")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, updatedVMI.Status.MigrationState).ToNot(BeNil(),
				"VMI should have migration state")

			By("Verifying target proxy port map in migration state")
			migrationState := updatedVMI.Status.MigrationState
			ExpectWithOffset(1, migrationState.TargetState).ToNot(BeNil(),
				"Migration state should contain TargetState with proxy port information")
			ExpectWithOffset(1, migrationState.TargetState.PortMap).ToNot(BeEmpty(),
				"Target proxy port map should not be empty")

			By("Verifying forwarding covers all protocol ports")
			// Protocol ports: 0 (control), 49152 (libvirt), 49153 (NBD)
			expectedProtocolPorts := []int{0, 49152, 49153}
			for _, expectedPort := range expectedProtocolPorts {
				_, exists := migrationState.TargetState.PortMap[expectedPort]
				ExpectWithOffset(1, exists).To(BeTrue(),
					"Target proxy port map should include forwarding for port %d", expectedPort)
			}
		})
	})

	Context("Proxy port map propagation in VMI migration status", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
			vmi       *v1.VirtualMachineInstance
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Creating migration-ready VMI")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			var err error
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		It("[test_id:TS-CNV-76508-005] should show proxy ports instead of virt-handler ports in VMI migration status", func() {
			By("Triggering migration")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migration = libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration)

			By("Verifying port map in VMI migration status")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, updatedVMI.Status.MigrationState).ToNot(BeNil(),
				"VMI should have migration state")

			By("Checking migration state port map shows proxy ports (non-standard, OS-allocated)")
			migrationState := updatedVMI.Status.MigrationState
			ExpectWithOffset(1, migrationState.SourceState).ToNot(BeNil(),
				"Source state should be present to show proxy port mapping")

			portMap := migrationState.SourceState.PortMap
			ExpectWithOffset(1, portMap).ToNot(BeEmpty(),
				"Port map should contain proxy port entries")

			By("Verifying port map entries exist for all protocol ports")
			// Standard virt-handler ports are 49152 (libvirt) and 49153 (NBD)
			// Proxy ports should be different (OS-allocated)
			for originalPort, proxyPort := range portMap {
				ExpectWithOffset(1, proxyPort).ToNot(Equal(originalPort),
					"Proxy port %d should differ from original virt-handler port %d", proxyPort, originalPort)
				ExpectWithOffset(1, proxyPort).ToNot(BeZero(),
					"Proxy port for original port %d should be non-zero (OS-allocated)", originalPort)
			}
		})
	})
})
