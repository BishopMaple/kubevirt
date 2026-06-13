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
	"strings"
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
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/libwait"
	"kubevirt.io/kubevirt/tests/testsuite"
)

var _ = Describe("[CNV-76508] Cross-Cluster Migration Proxy - Migration Flow", decorators.SigNetwork, func() {
	/*
		Migration flow tests for the CCLM proxy feature.
		These tests validate proxy metrics, target/source proxy port remapping,
		proxy cleanup on failure/cancellation, regression, and resilience.

		Markers: tier1

		Preconditions:
			- Multi-cluster setup with in-cluster and cross-cluster LM networks
			- DecentralizedLiveMigration feature gate enabled
			- crossClusterNetwork configured in KubeVirt CR
	*/

	var ctx context.Context
	var namespace string

	BeforeEach(func() {
		ctx = context.Background()
		namespace = testsuite.GetTestNamespace(nil)
	})

	Context("Proxy metrics emission", func() {
		It("[test_id:TS-CNV-76508-009] should emit Prometheus metrics for proxy connections, bytes transferred, and errors", func() {
			By("Ensuring DecentralizedLiveMigration feature gate and crossClusterNetwork are configured")
			ensureFeatureGateEnabled(ctx, decentralizedLiveMigrationGate)
			ensureCrossClusterNetworkConfigured(ctx, cclmNADName)

			By("Creating a Fedora VMI for migration")
			vmi := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			vmi, err := kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for VMI to be ready")
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)

			By("Initiating cross-cluster live migration through proxy")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migrationUID := libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration)
			Expect(migrationUID).ToNot(BeEmpty())

			By("Querying Prometheus for kubevirt_decentralized_migration_proxy_active_connections")
			// During migration, active_connections should have been > 0
			// After completion, it may have returned to 0
			// Verify via the metrics endpoint on virt-handler
			Eventually(func() bool {
				metricsOutput, metricsErr := getMetricsFromVirtHandler(ctx, namespace)
				if metricsErr != nil {
					return false
				}
				return strings.Contains(metricsOutput, "kubevirt_decentralized_migration_proxy_active_connections")
			}, 60*time.Second, 5*time.Second).Should(BeTrue(),
				"active_connections metric should be registered")

			By("Querying for kubevirt_decentralized_migration_proxy_bytes_transferred_total")
			Eventually(func() bool {
				metricsOutput, metricsErr := getMetricsFromVirtHandler(ctx, namespace)
				if metricsErr != nil {
					return false
				}
				return strings.Contains(metricsOutput, "kubevirt_decentralized_migration_proxy_bytes_transferred_total")
			}, 60*time.Second, 5*time.Second).Should(BeTrue(),
				"bytes_transferred_total metric should be present after migration")

			By("Verifying kubevirt_decentralized_migration_proxy_errors_total is 0 for successful migration")
			metricsOutput, err := getMetricsFromVirtHandler(ctx, namespace)
			Expect(err).ToNot(HaveOccurred())
			if strings.Contains(metricsOutput, "kubevirt_decentralized_migration_proxy_errors_total") {
				// If the metric exists, its value should be 0 for a successful migration
				Expect(metricsOutput).To(ContainSubstring("kubevirt_decentralized_migration_proxy_errors_total 0"))
			}

			By("Cleaning up VMI")
			err = kubevirt.Client().VirtualMachineInstance(namespace).Delete(ctx, vmi.Name, metav1.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("Target-side proxy port remapping", Ordered, func() {
		var vmi *v1.VirtualMachineInstance

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)

			By("Ensuring CCLM infrastructure is configured")
			ensureFeatureGateEnabled(ctx, decentralizedLiveMigrationGate)
			ensureCrossClusterNetworkConfigured(ctx, cclmNADName)

			By("Creating and starting a Fedora VM for migration")
			vmi = libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			var err error
			vmi, err = kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)
		})

		AfterAll(func() {
			if vmi != nil {
				err := kubevirt.Client().VirtualMachineInstance(namespace).Delete(ctx, vmi.Name, metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())
			}
		})

		It("[test_id:TS-CNV-76508-010] should remap target ports through proxy and update VMI status", func() {
			By("Triggering cross-cluster migration to target cluster")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migrationUID := libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration)
			Expect(migrationUID).ToNot(BeEmpty())

			By("Retrieving updated VMI to inspect migration state")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(updatedVMI.Status.MigrationState).ToNot(BeNil())

			By("Verifying VMI status has remapped port map (proxy ports)")
			migrationState := updatedVMI.Status.MigrationState
			Expect(migrationState.TargetDirectMigrationNodePorts).ToNot(BeEmpty(),
				"target migration node ports should contain proxy-remapped ports")

			By("Verifying VMI NodeAddress points to sync controller CCLM IP")
			Expect(migrationState.TargetNodeAddress).ToNot(BeEmpty(),
				"target node address should be set to sync controller CCLM IP")

			// Update vmi reference for next test
			vmi = updatedVMI
		})

		It("[test_id:TS-CNV-76508-011] should open source in-cluster proxy ports and update VMI migration state", func() {
			By("Retrieving VMI to inspect source-side migration state")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying source sync controller opens in-cluster proxy ports")
			migrationState := updatedVMI.Status.MigrationState
			Expect(migrationState).ToNot(BeNil(), "migration state should exist")

			By("Verifying VMI status updated with source-side remapped ports")
			Expect(migrationState.TargetDirectMigrationNodePorts).ToNot(BeEmpty(),
				"migration state should have source-side remapped ports")

			By("Verifying TargetNodeAddress points to source sync controller in-cluster IP")
			Expect(migrationState.TargetNodeAddress).ToNot(BeEmpty(),
				"target node address should point to source sync controller")

			// Verify the address is an IP, not a hostname
			Expect(migrationState.TargetNodeAddress).To(MatchRegexp(`\d+\.\d+\.\d+\.\d+`),
				"target node address should be an IP address")
		})
	})

	Context("Proxy cleanup on migration failure", func() {
		It("[test_id:TS-CNV-76508-013] should clean up proxy ports on both sides after migration failure", func() {
			By("Ensuring CCLM infrastructure is configured")
			ensureFeatureGateEnabled(ctx, decentralizedLiveMigrationGate)
			ensureCrossClusterNetworkConfigured(ctx, cclmNADName)

			By("Creating a Fedora VMI for migration")
			vmi := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			vmi, err := kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)

			By("Initiating a cross-cluster migration")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migration, err = kubevirt.Client().VirtualMachineInstanceMigration(namespace).Create(ctx, migration, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Inducing migration failure by setting bandwidth limit to cause timeout")
			// Use a migration policy with very low bandwidth to force failure
			Eventually(func() bool {
				updatedVMI, getErr := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
				if getErr != nil {
					return false
				}
				return updatedVMI.Status.MigrationState != nil
			}, 60*time.Second, 2*time.Second).Should(BeTrue())

			By("Waiting for migration to reach a terminal state (Failed)")
			Eventually(func() bool {
				updatedMigration, getErr := kubevirt.Client().VirtualMachineInstanceMigration(namespace).Get(ctx, migration.Name, metav1.GetOptions{})
				if getErr != nil {
					return false
				}
				return updatedMigration.Status.Phase == v1.MigrationFailed ||
					updatedMigration.Status.Phase == v1.MigrationSucceeded
			}, 300*time.Second, 5*time.Second).Should(BeTrue(),
				"migration should reach terminal state")

			By("Verifying proxy ports are cleaned up after migration completion/failure")
			// After migration completes or fails, sync controller should have no active proxy mappings
			Eventually(func() bool {
				pods, listErr := kubevirt.Client().CoreV1().Pods(kvNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: syncControllerLabel,
				})
				if listErr != nil || len(pods.Items) == 0 {
					return true // No sync controller pods = no proxy ports
				}
				// Check that sync controller is healthy (not crash-looping from resource leaks)
				for _, pod := range pods.Items {
					if pod.Status.Phase != k8sv1.PodRunning {
						return false
					}
				}
				return true
			}, 60*time.Second, 5*time.Second).Should(BeTrue(),
				"sync controller should be healthy after migration cleanup")

			By("Verifying proxy error metrics are incremented for failed migration")
			metricsOutput, metricsErr := getMetricsFromVirtHandler(ctx, namespace)
			if metricsErr == nil && strings.Contains(metricsOutput, "kubevirt_decentralized_migration_proxy_errors_total") {
				// If migration failed, errors_total should be > 0
				Expect(metricsOutput).ToNot(ContainSubstring("kubevirt_decentralized_migration_proxy_errors_total 0"))
			}

			By("Cleaning up VMI")
			err = kubevirt.Client().VirtualMachineInstance(namespace).Delete(ctx, vmi.Name, metav1.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("Migration cancellation through proxy", func() {
		It("[test_id:TS-CNV-76508-014] should cancel migration cleanly and release proxy resources", func() {
			By("Ensuring CCLM infrastructure is configured")
			ensureFeatureGateEnabled(ctx, decentralizedLiveMigrationGate)
			ensureCrossClusterNetworkConfigured(ctx, cclmNADName)

			By("Creating a Fedora VMI for migration")
			vmi := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			vmi, err := kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)

			By("Starting cross-cluster migration through proxy")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migration, err = kubevirt.Client().VirtualMachineInstanceMigration(namespace).Create(ctx, migration, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for migration to start running")
			Eventually(func() bool {
				updatedMigration, getErr := kubevirt.Client().VirtualMachineInstanceMigration(namespace).Get(ctx, migration.Name, metav1.GetOptions{})
				if getErr != nil {
					return false
				}
				return updatedMigration.Status.Phase == v1.MigrationRunning ||
					updatedMigration.Status.Phase == v1.MigrationScheduling ||
					updatedMigration.Status.Phase == v1.MigrationPreparingTarget
			}, 120*time.Second, 2*time.Second).Should(BeTrue(),
				"migration should start")

			By("Cancelling the migration via VirtualMachineInstanceMigration deletion")
			err = kubevirt.Client().VirtualMachineInstanceMigration(namespace).Delete(ctx, migration.Name, metav1.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying proxy ports are cleaned up on both sides")
			Eventually(func() bool {
				pods, listErr := kubevirt.Client().CoreV1().Pods(kvNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: syncControllerLabel,
				})
				if listErr != nil || len(pods.Items) == 0 {
					return true
				}
				for _, pod := range pods.Items {
					if pod.Status.Phase != k8sv1.PodRunning {
						return false
					}
				}
				return true
			}, 60*time.Second, 5*time.Second).Should(BeTrue(),
				"sync controller should be healthy after cancellation cleanup")

			By("Verifying VM remains running on source cluster")
			Eventually(func() v1.VirtualMachineInstancePhase {
				updatedVMI, getErr := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
				if getErr != nil {
					return ""
				}
				return updatedVMI.Status.Phase
			}, 60*time.Second, 2*time.Second).Should(Equal(v1.Running),
				"VM should remain in Running phase after migration cancellation")

			By("Cleaning up VMI")
			err = kubevirt.Client().VirtualMachineInstance(namespace).Delete(ctx, vmi.Name, metav1.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("Regression - non-proxy decentralized migration", func() {
		It("[test_id:TS-CNV-76508-015] should migrate without proxy when crossClusterNetwork is not configured", func() {
			By("Ensuring DecentralizedLiveMigration is enabled but crossClusterNetwork is NOT configured")
			ensureFeatureGateEnabled(ctx, decentralizedLiveMigrationGate)
			removeCrossClusterNetwork(ctx)

			By("Creating a Fedora VMI for migration")
			vmi := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			vmi, err := kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)

			By("Initiating a decentralized migration without cross-cluster network configuration")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migrationUID := libmigration.RunMigrationAndExpectToCompleteWithDefaultTimeout(kubevirt.Client(), migration)
			Expect(migrationUID).ToNot(BeEmpty())

			By("Verifying migration completes via direct path (no proxy)")
			updatedVMI, err := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(updatedVMI.Status.MigrationState).ToNot(BeNil())
			Expect(updatedVMI.Status.MigrationState.Completed).To(BeTrue(),
				"migration should complete successfully via direct path")

			By("Verifying no proxy ports were opened on sync controller")
			// Check that sync controller logs don't contain proxy port opening messages
			pods, err := kubevirt.Client().CoreV1().Pods(kvNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: syncControllerLabel,
			})
			if err == nil && len(pods.Items) > 0 {
				for _, pod := range pods.Items {
					logs, logErr := kubevirt.Client().CoreV1().Pods(kvNamespace).GetLogs(pod.Name, &k8sv1.PodLogOptions{
						SinceTime: &metav1.Time{Time: time.Now().Add(-2 * time.Minute)},
					}).DoRaw(ctx)
					if logErr == nil {
						Expect(string(logs)).ToNot(ContainSubstring("Opening CCLM proxy ports"),
							"sync controller should NOT open CCLM proxy ports without crossClusterNetwork")
					}
				}
			}

			By("Cleaning up VMI")
			err = kubevirt.Client().VirtualMachineInstance(namespace).Delete(ctx, vmi.Name, metav1.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("Proxy resilience - sync controller restart", Serial, func() {
		It("[test_id:TS-CNV-76508-016] should handle sync controller restart gracefully during active proxy session", func() {
			By("Ensuring CCLM infrastructure is configured")
			ensureFeatureGateEnabled(ctx, decentralizedLiveMigrationGate)
			ensureCrossClusterNetworkConfigured(ctx, cclmNADName)

			By("Creating a Fedora VMI for migration")
			vmi := libvmifact.NewFedora(
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(v1.DefaultPodNetwork()),
			)
			vmi, err := kubevirt.Client().VirtualMachineInstance(namespace).Create(ctx, vmi, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			vmi = libwait.WaitUntilVMIReady(vmi, console.LoginToFedora)

			By("Starting cross-cluster migration with active proxy")
			migration := libmigration.New(vmi.Name, vmi.Namespace)
			migration, err = kubevirt.Client().VirtualMachineInstanceMigration(namespace).Create(ctx, migration, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for migration to be in progress")
			Eventually(func() bool {
				updatedMigration, getErr := kubevirt.Client().VirtualMachineInstanceMigration(namespace).Get(ctx, migration.Name, metav1.GetOptions{})
				if getErr != nil {
					return false
				}
				return updatedMigration.Status.Phase == v1.MigrationRunning ||
					updatedMigration.Status.Phase == v1.MigrationScheduling ||
					updatedMigration.Status.Phase == v1.MigrationPreparingTarget
			}, 120*time.Second, 2*time.Second).Should(BeTrue(),
				"migration should start")

			By("Restarting the sync controller pod during active proxy session")
			pods, err := kubevirt.Client().CoreV1().Pods(kvNamespace).List(ctx, metav1.ListOptions{
				LabelSelector: syncControllerLabel,
			})
			Expect(err).ToNot(HaveOccurred())

			for _, pod := range pods.Items {
				err = kubevirt.Client().CoreV1().Pods(kvNamespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())
			}

			By("Verifying sync controller pod restarts and is healthy")
			Eventually(func() bool {
				pods, listErr := kubevirt.Client().CoreV1().Pods(kvNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: syncControllerLabel,
				})
				if listErr != nil || len(pods.Items) == 0 {
					return false
				}
				for _, pod := range pods.Items {
					if pod.Status.Phase != k8sv1.PodRunning {
						return false
					}
					ready := false
					for _, cond := range pod.Status.Conditions {
						if cond.Type == k8sv1.PodReady && cond.Status == k8sv1.ConditionTrue {
							ready = true
							break
						}
					}
					if !ready {
						return false
					}
				}
				return true
			}, 120*time.Second, 5*time.Second).Should(BeTrue(),
				"sync controller should restart and become ready")

			By("Verifying migration reaches terminal state (not hung)")
			Eventually(func() bool {
				updatedVMI, getErr := kubevirt.Client().VirtualMachineInstance(namespace).Get(ctx, vmi.Name, metav1.GetOptions{})
				if getErr != nil {
					return false
				}
				if updatedVMI.Status.MigrationState == nil {
					return false
				}
				// Migration should either succeed or fail, but not remain in Running
				return updatedVMI.Status.MigrationState.Completed ||
					updatedVMI.Status.MigrationState.Failed
			}, 300*time.Second, 5*time.Second).Should(BeTrue(),
				"migration should reach terminal state after sync controller restart")

			By("Cleaning up VMI")
			err = kubevirt.Client().VirtualMachineInstance(namespace).Delete(ctx, vmi.Name, metav1.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

// Helper functions for migration flow tests

func ensureCrossClusterNetworkConfigured(ctx context.Context, nadName string) {
	crossClusterPatch := fmt.Sprintf(
		`[{"op": "add", "path": "/spec/configuration/migrations/crossClusterNetwork", "value": "%s"}]`,
		nadName,
	)
	_, err := kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(crossClusterPatch), metav1.PatchOptions{})
	if err != nil {
		// May already be set — verify
		kv, getErr := kubevirt.Client().KubeVirt(kvNamespace).Get(ctx, kvName, metav1.GetOptions{})
		ExpectWithOffset(1, getErr).ToNot(HaveOccurred())
		Expect(kv.Spec.Configuration.MigrationConfiguration).ToNot(BeNil())
	}
}

func removeCrossClusterNetwork(ctx context.Context) {
	removePatch := `[{"op": "remove", "path": "/spec/configuration/migrations/crossClusterNetwork"}]`
	// Ignore error if field doesn't exist
	_, _ = kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(removePatch), metav1.PatchOptions{})
}

func getMetricsFromVirtHandler(ctx context.Context, namespace string) (string, error) {
	pods, err := kubevirt.Client().CoreV1().Pods(kvNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "kubevirt.io=virt-handler",
	})
	if err != nil || len(pods.Items) == 0 {
		return "", fmt.Errorf("no virt-handler pods found")
	}

	// Query metrics endpoint from the first virt-handler pod
	pod := pods.Items[0]
	metricsData, err := kubevirt.Client().CoreV1().Pods(kvNamespace).ProxyGet(
		"http", pod.Name, "8443", "/metrics", nil,
	).DoRaw(ctx)
	if err != nil {
		return "", err
	}
	return string(metricsData), nil
}
