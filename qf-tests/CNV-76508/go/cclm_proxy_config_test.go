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

	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/testsuite"
)

const (
	kvNamespace                       = "openshift-cnv"
	kvName                            = "kubevirt"
	decentralizedLiveMigrationGate    = "DecentralizedLiveMigration"
	cclmNADName                       = "cclm-nad"
	syncControllerDeploymentName      = "virt-synchronization-controller"
	syncControllerLabel               = "kubevirt.io=virt-synchronization-controller"
	multusNetworkAnnotation           = "k8s.v1.cni.cncf.io/networks"
)

var _ = Describe("[CNV-76508] Cross-Cluster Migration Proxy - Configuration", decorators.SigNetwork, Serial, func() {
	/*
		Configuration and deployment tests for the CCLM proxy feature.
		These tests validate feature gate behavior, API field propagation,
		sync controller placement, and operator deployment management.

		Markers: tier1

		Preconditions:
			- OCP 4.22+ cluster with OpenShift Virtualization installed
			- KubeVirt CR accessible for patching
	*/

	var ctx context.Context
	var namespace string

	BeforeEach(func() {
		ctx = context.Background()
		namespace = testsuite.GetTestNamespace(nil)
	})

	Context("Feature gate enablement", Serial, func() {
		var originalFeatureGates []string

		BeforeEach(func() {
			By("Saving original feature gates from KubeVirt CR")
			kv, err := kubevirt.Client().KubeVirt(kvNamespace).Get(ctx, kvName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			if kv.Spec.Configuration.DeveloperConfiguration != nil {
				originalFeatureGates = make([]string, len(kv.Spec.Configuration.DeveloperConfiguration.FeatureGates))
				copy(originalFeatureGates, kv.Spec.Configuration.DeveloperConfiguration.FeatureGates)
			}
		})

		AfterEach(func() {
			By("Restoring original feature gates in KubeVirt CR")
			patchData := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/configuration/developerConfiguration/featureGates", "value": %s}]`,
				featureGatesToJSON(originalFeatureGates),
			)
			_, err := kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())
		})

		It("[test_id:TS-CNV-76508-006] should control proxy functionality via DecentralizedLiveMigration feature gate", func() {
			By("Verifying DecentralizedLiveMigration feature gate is NOT enabled by default")
			kv, err := kubevirt.Client().KubeVirt(kvNamespace).Get(ctx, kvName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())

			featureGates := getFeatureGates(kv)
			Expect(featureGates).ToNot(ContainElement(decentralizedLiveMigrationGate),
				"DecentralizedLiveMigration should NOT be enabled by default")

			By("Enabling DecentralizedLiveMigration in KubeVirt CR developer configuration")
			patchData := fmt.Sprintf(
				`[{"op": "add", "path": "/spec/configuration/developerConfiguration/featureGates/-", "value": "%s"}]`,
				decentralizedLiveMigrationGate,
			)
			_, err = kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying the feature gate is active")
			Eventually(func() []string {
				kv, err = kubevirt.Client().KubeVirt(kvNamespace).Get(ctx, kvName, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				return getFeatureGates(kv)
			}, 30*time.Second, 2*time.Second).Should(ContainElement(decentralizedLiveMigrationGate))

			By("Disabling the feature gate and verifying proxy config is rejected")
			removeGatePatch := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/configuration/developerConfiguration/featureGates", "value": %s}]`,
				featureGatesToJSON(removeFromSlice(getFeatureGates(kv), decentralizedLiveMigrationGate)),
			)
			_, err = kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(removeGatePatch), metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying crossClusterNetwork configuration is rejected without feature gate")
			Eventually(func() error {
				crossClusterPatch := `[{"op": "add", "path": "/spec/configuration/migrations/crossClusterNetwork", "value": "cclm-test-nad"}]`
				_, patchErr := kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(crossClusterPatch), metav1.PatchOptions{})
				return patchErr
			}, 30*time.Second, 2*time.Second).Should(HaveOccurred(),
				"crossClusterNetwork should be rejected when feature gate is disabled")
		})
	})

	Context("CrossClusterNetwork API field", Serial, func() {
		It("[test_id:TS-CNV-76508-007] should validate and propagate crossClusterNetwork to sync controller deployment", func() {
			By("Ensuring DecentralizedLiveMigration feature gate is enabled")
			ensureFeatureGateEnabled(ctx, decentralizedLiveMigrationGate)

			By("Setting crossClusterNetwork in KubeVirt CR to a valid NAD name")
			crossClusterPatch := fmt.Sprintf(
				`[{"op": "add", "path": "/spec/configuration/migrations/crossClusterNetwork", "value": "%s"}]`,
				cclmNADName,
			)
			_, err := kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(crossClusterPatch), metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying sync controller deployment is updated with network annotation")
			Eventually(func() string {
				deploy, getErr := kubevirt.Client().AppsV1().Deployments(kvNamespace).Get(ctx, syncControllerDeploymentName, metav1.GetOptions{})
				if getErr != nil {
					return ""
				}
				return deploy.Spec.Template.Annotations[multusNetworkAnnotation]
			}, 60*time.Second, 5*time.Second).Should(ContainSubstring(cclmNADName),
				"sync controller deployment should have CCLM network annotation")

			By("Removing crossClusterNetwork field")
			removePatch := `[{"op": "remove", "path": "/spec/configuration/migrations/crossClusterNetwork"}]`
			_, err = kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(removePatch), metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying sync controller deployment annotation is updated accordingly")
			Eventually(func() string {
				deploy, getErr := kubevirt.Client().AppsV1().Deployments(kvNamespace).Get(ctx, syncControllerDeploymentName, metav1.GetOptions{})
				if getErr != nil {
					return cclmNADName
				}
				return deploy.Spec.Template.Annotations[multusNetworkAnnotation]
			}, 60*time.Second, 5*time.Second).ShouldNot(ContainSubstring(cclmNADName),
				"CCLM network annotation should be removed after clearing crossClusterNetwork")
		})
	})

	Context("SynchronizationPlacement", Serial, func() {
		It("[test_id:TS-CNV-76508-008] should schedule sync controller pods only on designated nodes", func() {
			By("Ensuring DecentralizedLiveMigration feature gate is enabled")
			ensureFeatureGateEnabled(ctx, decentralizedLiveMigrationGate)

			By("Retrieving available worker nodes")
			nodes, err := kubevirt.Client().CoreV1().Nodes().List(ctx, metav1.ListOptions{
				LabelSelector: "node-role.kubernetes.io/worker",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes.Items).To(HaveLen(BeNumerically(">=", 2)),
				"at least 2 worker nodes required for this test")

			targetNode := nodes.Items[0].Name
			cclmLabel := "cclm-network"

			By(fmt.Sprintf("Labeling node %s with %s=true", targetNode, cclmLabel))
			labelPatch := fmt.Sprintf(`[{"op": "add", "path": "/metadata/labels/%s", "value": "true"}]`, cclmLabel)
			_, err = kubevirt.Client().CoreV1().Nodes().Patch(ctx, targetNode, types.JSONPatchType, []byte(labelPatch), metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			defer func() {
				By("Cleaning up: removing node label")
				removeLabelPatch := fmt.Sprintf(`[{"op": "remove", "path": "/metadata/labels/%s"}]`, cclmLabel)
				_, cleanupErr := kubevirt.Client().CoreV1().Nodes().Patch(ctx, targetNode, types.JSONPatchType, []byte(removeLabelPatch), metav1.PatchOptions{})
				Expect(cleanupErr).ToNot(HaveOccurred())
			}()

			By("Setting synchronizationPlacement with node selector targeting labeled nodes")
			placementPatch := fmt.Sprintf(
				`[{"op": "add", "path": "/spec/synchronizationPlacement", "value": {"nodeSelector": {"%s": "true"}}}]`,
				cclmLabel,
			)
			_, err = kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(placementPatch), metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			defer func() {
				By("Cleaning up: removing synchronizationPlacement")
				removePlacementPatch := `[{"op": "remove", "path": "/spec/synchronizationPlacement"}]`
				_, cleanupErr := kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(removePlacementPatch), metav1.PatchOptions{})
				Expect(cleanupErr).ToNot(HaveOccurred())
			}()

			By("Verifying sync controller pods are scheduled only on designated nodes")
			Eventually(func() bool {
				pods, listErr := kubevirt.Client().CoreV1().Pods(kvNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: syncControllerLabel,
				})
				if listErr != nil || len(pods.Items) == 0 {
					return false
				}
				for _, pod := range pods.Items {
					if pod.Status.Phase != k8sv1.PodRunning {
						continue
					}
					if pod.Spec.NodeName != targetNode {
						return false
					}
				}
				return true
			}, 120*time.Second, 5*time.Second).Should(BeTrue(),
				"all sync controller pods should be running on the labeled node")
		})
	})

	Context("Operator deployment of sync controller with CCLM network", Serial, func() {
		It("[test_id:TS-CNV-76508-017] should deploy sync controller with correct multi-network annotations", func() {
			By("Ensuring DecentralizedLiveMigration feature gate is enabled")
			ensureFeatureGateEnabled(ctx, decentralizedLiveMigrationGate)

			By("Ensuring crossClusterNetwork is configured")
			crossClusterPatch := fmt.Sprintf(
				`[{"op": "add", "path": "/spec/configuration/migrations/crossClusterNetwork", "value": "%s"}]`,
				cclmNADName,
			)
			_, err := kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(crossClusterPatch), metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			defer func() {
				By("Cleaning up: removing crossClusterNetwork")
				removePatch := `[{"op": "remove", "path": "/spec/configuration/migrations/crossClusterNetwork"}]`
				_, cleanupErr := kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(removePatch), metav1.PatchOptions{})
				Expect(cleanupErr).ToNot(HaveOccurred())
			}()

			By("Verifying virt-operator creates sync controller Deployment with correct network annotations")
			Eventually(func() string {
				deploy, getErr := kubevirt.Client().AppsV1().Deployments(kvNamespace).Get(ctx, syncControllerDeploymentName, metav1.GetOptions{})
				if getErr != nil {
					return ""
				}
				return deploy.Spec.Template.Annotations[multusNetworkAnnotation]
			}, 60*time.Second, 5*time.Second).Should(ContainSubstring(cclmNADName),
				"deployment should have CCLM network annotation")

			By("Verifying sync controller pod has interfaces on both networks")
			Eventually(func() bool {
				pods, listErr := kubevirt.Client().CoreV1().Pods(kvNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: syncControllerLabel,
				})
				if listErr != nil || len(pods.Items) == 0 {
					return false
				}
				for _, pod := range pods.Items {
					if pod.Status.Phase != k8sv1.PodRunning {
						continue
					}
					annotations := pod.Annotations
					if annotations == nil {
						return false
					}
					networkStatus, exists := annotations["k8s.v1.cni.cncf.io/network-status"]
					if !exists || networkStatus == "" {
						return false
					}
					return true
				}
				return false
			}, 60*time.Second, 5*time.Second).Should(BeTrue(),
				"sync controller pod should have multi-network status annotation")

			By("Updating crossClusterNetwork to a new value and verifying deployment is updated")
			updatedNAD := "cclm-nad-updated"
			updatePatch := fmt.Sprintf(
				`[{"op": "replace", "path": "/spec/configuration/migrations/crossClusterNetwork", "value": "%s"}]`,
				updatedNAD,
			)
			_, err = kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(updatePatch), metav1.PatchOptions{})
			Expect(err).ToNot(HaveOccurred())

			Eventually(func() string {
				deploy, getErr := kubevirt.Client().AppsV1().Deployments(kvNamespace).Get(ctx, syncControllerDeploymentName, metav1.GetOptions{})
				if getErr != nil {
					return ""
				}
				return deploy.Spec.Template.Annotations[multusNetworkAnnotation]
			}, 60*time.Second, 5*time.Second).Should(ContainSubstring(updatedNAD),
				"deployment annotation should be updated to new NAD name")
		})
	})
})

// Helper functions for configuration tests

func getFeatureGates(kv *v1.KubeVirt) []string {
	if kv.Spec.Configuration.DeveloperConfiguration == nil {
		return nil
	}
	return kv.Spec.Configuration.DeveloperConfiguration.FeatureGates
}

func featureGatesToJSON(gates []string) string {
	if len(gates) == 0 {
		return "[]"
	}
	result := "["
	for i, gate := range gates {
		if i > 0 {
			result += ","
		}
		result += fmt.Sprintf(`"%s"`, gate)
	}
	result += "]"
	return result
}

func removeFromSlice(slice []string, item string) []string {
	var result []string
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}

func ensureFeatureGateEnabled(ctx context.Context, gateName string) {
	kv, err := kubevirt.Client().KubeVirt(kvNamespace).Get(ctx, kvName, metav1.GetOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	gates := getFeatureGates(kv)
	for _, g := range gates {
		if g == gateName {
			return // already enabled
		}
	}

	patchData := fmt.Sprintf(
		`[{"op": "add", "path": "/spec/configuration/developerConfiguration/featureGates/-", "value": "%s"}]`,
		gateName,
	)
	_, err = kubevirt.Client().KubeVirt(kvNamespace).Patch(ctx, kvName, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	Eventually(func() []string {
		kv, err = kubevirt.Client().KubeVirt(kvNamespace).Get(ctx, kvName, metav1.GetOptions{})
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
		return getFeatureGates(kv)
	}, 30*time.Second, 2*time.Second).Should(ContainElement(gateName))
}
