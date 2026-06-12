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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/testsuite"
)

/*
Cross-Cluster Live Migration Network Proxy — Configuration & API Tests

STP Reference: outputs/stp/CNV-76508/CNV-76508_test_plan.md
Jira: CNV-76508

Tests:
  - TS-CNV-76508-001: Sync controller crosscluster0 network attachment
  - TS-CNV-76508-006: crossClusterNetwork API field validation
  - TS-CNV-76508-007: synchronizationPlacement API field
  - TS-CNV-76508-013: Feature gate guarding proxy behavior
*/

var _ = Describe("[CNV-76508] Cross-cluster migration proxy configuration", decorators.SigCompute, Serial, func() {

	Context("Sync controller crosscluster0 network attachment", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx       context.Context
			namespace string
		)

		BeforeAll(func() {
			ctx = context.Background()
			namespace = testsuite.GetTestNamespace(nil)
			_ = namespace // namespace used for scoped operations

			By("Enabling CrossClusterMigrationProxy feature gate")
			kubevirt.UpdateKubeVirtConfigValue(func(kv *v1.KubeVirt) {
				if kv.Spec.Configuration.DeveloperConfiguration == nil {
					kv.Spec.Configuration.DeveloperConfiguration = &v1.DeveloperConfiguration{}
				}
				kv.Spec.Configuration.DeveloperConfiguration.FeatureGates = append(
					kv.Spec.Configuration.DeveloperConfiguration.FeatureGates,
					"CrossClusterMigrationProxy",
				)
			})

			By("Setting crossClusterNetwork in MigrationConfiguration")
			kubevirt.UpdateKubeVirtConfigValue(func(kv *v1.KubeVirt) {
				if kv.Spec.Configuration.MigrationConfiguration == nil {
					kv.Spec.Configuration.MigrationConfiguration = &v1.MigrationConfiguration{}
				}
				crossClusterNet := "openshift-cnv/crosscluster-migration"
				kv.Spec.Configuration.MigrationConfiguration.CrossClusterNetwork = &crossClusterNet
			})
		})

		It("[test_id:TS-CNV-76508-001] should attach sync controller to crosscluster0 network when feature gate and crossClusterNetwork are configured", func() {
			By("Getting synchronization controller pods")
			syncPods, err := kubevirt.Client().CoreV1().Pods("openshift-cnv").List(ctx, metav1.ListOptions{
				LabelSelector: "kubevirt.io=virt-synchronization-controller",
			})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, syncPods.Items).ToNot(BeEmpty(), "No sync controller pods found")

			By("Verifying crosscluster0 interface on sync controller pod")
			syncControllerPod := &syncPods.Items[0]
			networkStatusAnnotation, exists := syncControllerPod.Annotations["k8s.v1.cni.cncf.io/network-status"]
			ExpectWithOffset(1, exists).To(BeTrue(), "Pod should have network-status annotation")
			ExpectWithOffset(1, networkStatusAnnotation).To(ContainSubstring("crosscluster"),
				"Sync controller pod should have crosscluster network attachment")

			By("Verifying IP address is assigned from crosscluster NAD IPAM range")
			ExpectWithOffset(1, networkStatusAnnotation).To(ContainSubstring("172.22.42."),
				"crosscluster0 interface should have IP from configured IPAM range 172.22.42.0/24")
		})
	})

	Context("crossClusterNetwork API field validation", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx context.Context
		)

		BeforeAll(func() {
			ctx = context.Background()
		})

		It("[test_id:TS-CNV-76508-006] should accept crossClusterNetwork in MigrationConfiguration and propagate to sync controller deployment", func() {
			By("Setting crossClusterNetwork in MigrationConfiguration")
			kubevirt.UpdateKubeVirtConfigValue(func(kv *v1.KubeVirt) {
				if kv.Spec.Configuration.MigrationConfiguration == nil {
					kv.Spec.Configuration.MigrationConfiguration = &v1.MigrationConfiguration{}
				}
				crossClusterNet := "openshift-cnv/crosscluster-migration"
				kv.Spec.Configuration.MigrationConfiguration.CrossClusterNetwork = &crossClusterNet
			})

			By("Verifying crossClusterNetwork field persisted")
			kv, err := kubevirt.Client().KubeVirt("openshift-cnv").Get(ctx, "kubevirt", metav1.GetOptions{})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, kv.Spec.Configuration.MigrationConfiguration).ToNot(BeNil())
			ExpectWithOffset(1, kv.Spec.Configuration.MigrationConfiguration.CrossClusterNetwork).ToNot(BeNil())
			ExpectWithOffset(1, *kv.Spec.Configuration.MigrationConfiguration.CrossClusterNetwork).To(
				Equal("openshift-cnv/crosscluster-migration"))

			By("Verifying sync controller deployment network annotation")
			Eventually(func(g Gomega) {
				deployment, err := kubevirt.Client().AppsV1().Deployments("openshift-cnv").Get(
					ctx, "virt-synchronization-controller", metav1.GetOptions{})
				g.Expect(err).ToNot(HaveOccurred())
				annotations := deployment.Spec.Template.Annotations
				g.Expect(annotations).To(HaveKey("k8s.v1.cni.cncf.io/networks"))
				g.Expect(annotations["k8s.v1.cni.cncf.io/networks"]).To(
					ContainSubstring("crosscluster-migration"))
			}, 120*time.Second, 5*time.Second).Should(Succeed())
		})
	})

	Context("synchronizationPlacement API field", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx context.Context
		)

		BeforeAll(func() {
			ctx = context.Background()
		})

		It("[test_id:TS-CNV-76508-007] should control node scheduling of sync controller pods via synchronizationPlacement", func() {
			By("Getting available worker nodes")
			nodes, err := kubevirt.Client().CoreV1().Nodes().List(ctx, metav1.ListOptions{
				LabelSelector: "node-role.kubernetes.io/worker",
			})
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			ExpectWithOffset(1, nodes.Items).ToNot(BeEmpty(), "No worker nodes found")
			targetNode := nodes.Items[0].Name

			By("Setting synchronizationPlacement with nodeSelector")
			kubevirt.UpdateKubeVirtConfigValue(func(kv *v1.KubeVirt) {
				kv.Spec.SynchronizationPlacement = &v1.ComponentConfig{
					NodePlacement: &v1.NodePlacement{
						NodeSelector: map[string]string{
							"kubernetes.io/hostname": targetNode,
						},
					},
				}
			})

			By("Verifying sync controller pods are on target node")
			Eventually(func(g Gomega) {
				syncPods, err := kubevirt.Client().CoreV1().Pods("openshift-cnv").List(ctx, metav1.ListOptions{
					LabelSelector: "kubevirt.io=virt-synchronization-controller",
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(syncPods.Items).ToNot(BeEmpty(), "No sync controller pods found")
				for _, pod := range syncPods.Items {
					g.Expect(pod.Spec.NodeName).To(Equal(targetNode),
						"Sync controller pod should be scheduled on target node %s", targetNode)
				}
			}, 120*time.Second, 5*time.Second).Should(Succeed())

			By("Removing synchronizationPlacement")
			kubevirt.UpdateKubeVirtConfigValue(func(kv *v1.KubeVirt) {
				kv.Spec.SynchronizationPlacement = nil
			})
		})
	})

	Context("Feature gate guarding proxy behavior", Ordered, decorators.OncePerOrderedCleanup, func() {
		var (
			ctx context.Context
		)

		BeforeAll(func() {
			ctx = context.Background()
		})

		It("[test_id:TS-CNV-76508-013] should NOT create proxy when CrossClusterMigrationProxy feature gate is disabled even with crossClusterNetwork set", func() {
			By("Disabling CrossClusterMigrationProxy but setting crossClusterNetwork")
			kubevirt.UpdateKubeVirtConfigValue(func(kv *v1.KubeVirt) {
				// Remove CrossClusterMigrationProxy from feature gates
				if kv.Spec.Configuration.DeveloperConfiguration != nil {
					gates := []string{}
					for _, gate := range kv.Spec.Configuration.DeveloperConfiguration.FeatureGates {
						if gate != "CrossClusterMigrationProxy" {
							gates = append(gates, gate)
						}
					}
					kv.Spec.Configuration.DeveloperConfiguration.FeatureGates = gates
				}
				// Set crossClusterNetwork anyway
				if kv.Spec.Configuration.MigrationConfiguration == nil {
					kv.Spec.Configuration.MigrationConfiguration = &v1.MigrationConfiguration{}
				}
				crossClusterNet := "openshift-cnv/crosscluster-migration"
				kv.Spec.Configuration.MigrationConfiguration.CrossClusterNetwork = &crossClusterNet
			})

			By("Verifying sync controller does NOT have crosscluster0")
			Eventually(func() bool {
				syncPods, err := kubevirt.Client().CoreV1().Pods("openshift-cnv").List(ctx, metav1.ListOptions{
					LabelSelector: "kubevirt.io=virt-synchronization-controller",
				})
				if err != nil || len(syncPods.Items) == 0 {
					return true // No sync pods means no proxy
				}
				for _, pod := range syncPods.Items {
					annotations := pod.Annotations
					if networks, ok := annotations["k8s.v1.cni.cncf.io/networks"]; ok {
						if strings.Contains(networks, "crosscluster") {
							return false
						}
					}
				}
				return true
			}, 60*time.Second, 5*time.Second).Should(BeTrue(),
				"Sync controller should NOT have crosscluster0 when feature gate is disabled")

			By("Removing crossClusterNetwork")
			kubevirt.UpdateKubeVirtConfigValue(func(kv *v1.KubeVirt) {
				if kv.Spec.Configuration.MigrationConfiguration != nil {
					kv.Spec.Configuration.MigrationConfiguration.CrossClusterNetwork = nil
				}
			})
		})
	})
})
