/*
 * This file is part of the KubeVirt project
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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "kubevirt.io/api/core/v1"

	"kubevirt.io/kubevirt/pkg/libvmi"
	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/testsuite"
)

var _ = Describe(SIG("Multus NAD name validation", decorators.Multus, func() {
	var namespace string

	BeforeEach(func() {
		namespace = testsuite.GetTestNamespace(nil)
	})

	// PSE: Preconditions: KubeVirt admission webhook is running.
	// Steps: Create VMI with Multus network using invalid NAD name format.
	// Expected: VMI creation is rejected by admission webhook with appropriate error message.
	DescribeTable("should reject VMI creation with invalid Multus NAD network name",
		func(networkName, expectedMsgSubstring string) {
			vmi := libvmi.New(
				libvmi.WithInterface(v1.Interface{Name: "test-net"}),
				libvmi.WithNetwork(&v1.Network{
					Name: "test-net",
					NetworkSource: v1.NetworkSource{
						Multus: &v1.MultusNetwork{NetworkName: networkName},
					},
				}),
			)

			By(fmt.Sprintf("Creating VMI with invalid Multus NAD name %q", networkName))
			_, err := kubevirt.Client().VirtualMachineInstance(namespace).Create(
				context.Background(), vmi, metav1.CreateOptions{},
			)

			By("Verifying admission webhook rejected the VMI")
			Expect(err).To(HaveOccurred(), "VMI creation should be rejected for invalid NAD name %q", networkName)
			Expect(err.Error()).To(ContainSubstring(expectedMsgSubstring),
				"Error message should indicate the validation failure for NAD name %q", networkName)
		},
		Entry("uppercase standalone name",
			"UPPER", "is not valid"),
		Entry("trailing dot in standalone name",
			"not.valid.name.", "is not valid"),
		Entry("uppercase namespace",
			"UPPER/my-nad", "is not valid"),
		Entry("dotted namespace (not a valid DNS label)",
			"my.ns/my-nad", "is not valid"),
		Entry("uppercase in name part",
			"foo/UPPER", "is not valid"),
		Entry("leading hyphen in name part",
			"foo/-invalid", "is not valid"),
		Entry("trailing hyphen in name part",
			"foo/invalid-", "is not valid"),
		Entry("empty namespace",
			"/name", "namespace must not be empty"),
		Entry("empty name",
			"ns/", "name must not be empty"),
		Entry("multiple slashes",
			"a/b/c", "expected format"),
	)

	// PSE: Preconditions: KubeVirt admission webhook is running.
	// Steps: Create VMI with Multus network using valid NAD name format.
	// Expected: VMI creation is accepted by admission webhook (may fail later for other reasons,
	// but not due to NAD name format validation).
	DescribeTable("should accept VMI creation with valid Multus NAD network name format",
		func(networkName string) {
			vmi := libvmi.New(
				libvmi.WithInterface(v1.Interface{Name: "test-net"}),
				libvmi.WithNetwork(&v1.Network{
					Name: "test-net",
					NetworkSource: v1.NetworkSource{
						Multus: &v1.MultusNetwork{NetworkName: networkName},
					},
				}),
			)

			By(fmt.Sprintf("Creating VMI with valid Multus NAD name %q", networkName))
			_, err := kubevirt.Client().VirtualMachineInstance(namespace).Create(
				context.Background(), vmi, metav1.CreateOptions{},
			)

			By("Verifying admission webhook did not reject for NAD name format")
			// The VMI may fail for other reasons (e.g., NAD doesn't exist, missing disk),
			// but should NOT fail due to NAD name format validation.
			if err != nil {
				Expect(err.Error()).NotTo(ContainSubstring("NAD name"),
					"VMI should not be rejected for NAD name format %q", networkName)
				Expect(err.Error()).NotTo(ContainSubstring("is not valid"),
					"VMI should not be rejected for NAD name format %q", networkName)
			}
		},
		Entry("simple name", "my-nad"),
		Entry("namespaced name", "default/my-nad"),
		Entry("namespaced name with numbers", "my-namespace/my-nad-123"),
		Entry("name with dots (valid subdomain)", "my.nad"),
		Entry("namespaced name with dots", "my-namespace/my.nad"),
	)

	// PSE: Preconditions: KubeVirt admission webhook is running.
	// Steps: Create VMI with Multus network where both namespace and name parts are invalid.
	// Expected: Admission webhook reports errors for both namespace and name.
	It("should report both namespace and name errors when both are invalid", func() {
		vmi := libvmi.New(
			libvmi.WithInterface(v1.Interface{Name: "test-net"}),
			libvmi.WithNetwork(&v1.Network{
				Name: "test-net",
				NetworkSource: v1.NetworkSource{
					Multus: &v1.MultusNetwork{NetworkName: "UPPER/UPPER"},
				},
			}),
		)

		By("Creating VMI with both namespace and name invalid")
		_, err := kubevirt.Client().VirtualMachineInstance(namespace).Create(
			context.Background(), vmi, metav1.CreateOptions{},
		)

		By("Verifying both errors are reported")
		Expect(err).To(HaveOccurred(), "VMI creation should be rejected")
		Expect(err.Error()).To(ContainSubstring("namespace"),
			"Error should mention invalid namespace")
		Expect(err.Error()).To(ContainSubstring("name"),
			"Error should mention invalid name")
	})

	// PSE: Preconditions: KubeVirt admission webhook is running.
	// Steps: Create VMI with multiple Multus networks, each having a different type of invalid NAD name.
	// Expected: Admission webhook reports errors for all invalid networks.
	It("should report errors from multiple Multus networks with different invalid NAD names", func() {
		vmi := libvmi.New(
			libvmi.WithInterface(v1.Interface{Name: "net1"}),
			libvmi.WithInterface(v1.Interface{Name: "net2"}),
			libvmi.WithNetwork(&v1.Network{
				Name: "net1",
				NetworkSource: v1.NetworkSource{
					Multus: &v1.MultusNetwork{NetworkName: "a/b/c"},
				},
			}),
			libvmi.WithNetwork(&v1.Network{
				Name: "net2",
				NetworkSource: v1.NetworkSource{
					Multus: &v1.MultusNetwork{NetworkName: "UPPER"},
				},
			}),
		)

		By("Creating VMI with multiple Multus networks having different errors")
		_, err := kubevirt.Client().VirtualMachineInstance(namespace).Create(
			context.Background(), vmi, metav1.CreateOptions{},
		)

		By("Verifying errors from both networks are reported")
		Expect(err).To(HaveOccurred(), "VMI creation should be rejected")
		Expect(err.Error()).To(ContainSubstring("expected format"),
			"Error should report format issue for a/b/c")
		Expect(err.Error()).To(ContainSubstring("is not valid"),
			"Error should report validation issue for UPPER")
	})
}))
