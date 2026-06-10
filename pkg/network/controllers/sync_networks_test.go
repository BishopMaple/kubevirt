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

package controllers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "kubevirt.io/api/core/v1"
)

// CNV-72329: Unit tests for syncNetworks function
// STP Reference: CNV-72329_test_plan.md
//
// syncNetworks synchronizes VM network specs to VMI network specs,
// handling NAD reference swaps by deep-copying the VM network object
// when the Multus NAD name differs between VM and VMI.

var _ = Describe("syncNetworks", func() {

	// Helper to create a Multus network
	newMultusNetwork := func(name, nadName string) v1.Network {
		return v1.Network{
			Name: name,
			NetworkSource: v1.NetworkSource{
				Multus: &v1.MultusNetwork{
					NetworkName: nadName,
				},
			},
		}
	}

	// Helper to create a pod network
	newPodNetwork := func(name string) v1.Network {
		return v1.Network{
			Name: name,
			NetworkSource: v1.NetworkSource{
				Pod: &v1.PodNetwork{},
			},
		}
	}

	// TS-CNV-72329-001: VMI network not present in VM spec is dropped
	//
	// Preconditions:
	//   - VM spec with one Multus network (net1 -> nad-a)
	//   - VMI spec with two Multus networks (net1 -> nad-a, net2 -> nad-b)
	//
	// Steps:
	//   1. Call syncNetworks(vmNets, vmiNets)
	//
	// Expected:
	//   - Result contains only net1 with nad-a
	//   - net2 is not present in the result
	It("should drop VMI networks not present in VM spec", func() {
		vmNets := []v1.Network{
			newMultusNetwork("net1", "nad-a"),
		}
		vmiNets := []v1.Network{
			newMultusNetwork("net1", "nad-a"),
			newMultusNetwork("net2", "nad-b"),
		}

		result := syncNetworks(vmNets, vmiNets)

		Expect(result).To(HaveLen(1))
		Expect(result[0].Name).To(Equal("net1"))
		Expect(result[0].Multus.NetworkName).To(Equal("nad-a"))
	})

	// TS-CNV-72329-002: Non-Multus (pod) network is preserved
	//
	// Preconditions:
	//   - VM spec with default pod network and one Multus network
	//   - VMI spec with matching default pod network and Multus network
	//
	// Steps:
	//   1. Call syncNetworks(vmNets, vmiNets)
	//
	// Expected:
	//   - Default pod network is preserved unchanged in the result
	//   - Multus network is also preserved (same NAD name)
	It("should preserve non-Multus (pod) networks", func() {
		vmNets := []v1.Network{
			newPodNetwork("default"),
			newMultusNetwork("net1", "nad-a"),
		}
		vmiNets := []v1.Network{
			newPodNetwork("default"),
			newMultusNetwork("net1", "nad-a"),
		}

		result := syncNetworks(vmNets, vmiNets)

		Expect(result).To(HaveLen(2))
		Expect(result[0].Name).To(Equal("default"))
		Expect(result[0].Pod).NotTo(BeNil())
		Expect(result[0].Multus).To(BeNil())
		Expect(result[1].Name).To(Equal("net1"))
		Expect(result[1].Multus.NetworkName).To(Equal("nad-a"))
	})

	// TS-CNV-72329-003: VMI network replaced when NAD name differs
	//
	// Preconditions:
	//   - VM spec with Multus network (net1 -> new-nad)
	//   - VMI spec with Multus network (net1 -> old-nad)
	//
	// Steps:
	//   1. Call syncNetworks(vmNets, vmiNets)
	//
	// Expected:
	//   - Result contains net1 with NAD name 'new-nad' (from VM spec)
	It("should replace VMI network with VM network when NAD name differs", func() {
		vmNets := []v1.Network{
			newMultusNetwork("net1", "new-nad"),
		}
		vmiNets := []v1.Network{
			newMultusNetwork("net1", "old-nad"),
		}

		result := syncNetworks(vmNets, vmiNets)

		Expect(result).To(HaveLen(1))
		Expect(result[0].Name).To(Equal("net1"))
		Expect(result[0].Multus.NetworkName).To(Equal("new-nad"))
	})

	// TS-CNV-72329-004: VMI network preserved when NAD name matches
	//
	// Preconditions:
	//   - VM spec with Multus network (net1 -> same-nad)
	//   - VMI spec with Multus network (net1 -> same-nad)
	//
	// Steps:
	//   1. Call syncNetworks(vmNets, vmiNets)
	//
	// Expected:
	//   - Result contains net1 with NAD name 'same-nad' (preserved from VMI)
	It("should preserve VMI network when NAD name matches", func() {
		vmNets := []v1.Network{
			newMultusNetwork("net1", "same-nad"),
		}
		vmiNets := []v1.Network{
			newMultusNetwork("net1", "same-nad"),
		}

		result := syncNetworks(vmNets, vmiNets)

		Expect(result).To(HaveLen(1))
		Expect(result[0].Name).To(Equal("net1"))
		Expect(result[0].Multus.NetworkName).To(Equal("same-nad"))
	})

	// TS-CNV-72329-005: Multiple networks with mixed changes
	//
	// Preconditions:
	//   - VM spec with: pod network, net1 -> new-nad1, net2 -> same-nad2, net3 -> new-nad3
	//   - VMI spec with: pod network, net1 -> old-nad1, net2 -> same-nad2, net3 -> old-nad3
	//
	// Steps:
	//   1. Call syncNetworks(vmNets, vmiNets)
	//
	// Expected:
	//   - Pod network preserved unchanged
	//   - net1 updated to new-nad1 (from VM)
	//   - net2 preserved as same-nad2 (unchanged)
	//   - net3 updated to new-nad3 (from VM)
	It("should handle multiple networks with mixed changes", func() {
		vmNets := []v1.Network{
			newPodNetwork("default"),
			newMultusNetwork("net1", "new-nad1"),
			newMultusNetwork("net2", "same-nad2"),
			newMultusNetwork("net3", "new-nad3"),
		}
		vmiNets := []v1.Network{
			newPodNetwork("default"),
			newMultusNetwork("net1", "old-nad1"),
			newMultusNetwork("net2", "same-nad2"),
			newMultusNetwork("net3", "old-nad3"),
		}

		result := syncNetworks(vmNets, vmiNets)

		Expect(result).To(HaveLen(4))
		Expect(result[0].Name).To(Equal("default"))
		Expect(result[0].Pod).NotTo(BeNil())
		Expect(result[1].Name).To(Equal("net1"))
		Expect(result[1].Multus.NetworkName).To(Equal("new-nad1"))
		Expect(result[2].Name).To(Equal("net2"))
		Expect(result[2].Multus.NetworkName).To(Equal("same-nad2"))
		Expect(result[3].Name).To(Equal("net3"))
		Expect(result[3].Multus.NetworkName).To(Equal("new-nad3"))
	})

	// TS-CNV-72329-006: Empty VMI networks returns nil
	//
	// Preconditions:
	//   - VM spec with one Multus network
	//   - VMI spec with empty networks list
	//
	// Steps:
	//   1. Call syncNetworks(vmNets, emptyVmiNets)
	//
	// Expected:
	//   - Result is nil/empty
	It("should return nil when VMI has no networks", func() {
		vmNets := []v1.Network{
			newMultusNetwork("net1", "nad-a"),
		}
		var vmiNets []v1.Network

		result := syncNetworks(vmNets, vmiNets)

		Expect(result).To(BeNil())
	})

	// TS-CNV-72329-007: Empty VM networks drops all VMI networks
	//
	// Preconditions:
	//   - VM spec with empty networks list
	//   - VMI spec with two Multus networks
	//
	// Steps:
	//   1. Call syncNetworks(emptyVmNets, vmiNets)
	//
	// Expected:
	//   - Result is nil/empty (all networks dropped)
	It("should drop all VMI networks when VM spec has no networks", func() {
		var vmNets []v1.Network
		vmiNets := []v1.Network{
			newMultusNetwork("net1", "nad-a"),
			newMultusNetwork("net2", "nad-b"),
		}

		result := syncNetworks(vmNets, vmiNets)

		Expect(result).To(BeNil())
	})

	// Additional edge case: deep copy isolation
	// Verify that when NAD name differs, the result is a deep copy
	// (modifying the result doesn't affect the original VM network).
	It("should deep copy VM network when NAD name differs", func() {
		vmNets := []v1.Network{
			newMultusNetwork("net1", "new-nad"),
		}
		vmiNets := []v1.Network{
			newMultusNetwork("net1", "old-nad"),
		}

		result := syncNetworks(vmNets, vmiNets)

		Expect(result).To(HaveLen(1))
		Expect(result[0].Multus.NetworkName).To(Equal("new-nad"))

		// Modify the result and verify the original is unchanged
		result[0].Multus.NetworkName = "modified"
		Expect(vmNets[0].Multus.NetworkName).To(Equal("new-nad"))
	})
})
