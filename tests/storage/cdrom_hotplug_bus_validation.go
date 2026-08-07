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

package storage

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	"kubevirt.io/kubevirt/pkg/apimachinery/patch"
	"kubevirt.io/kubevirt/pkg/libdv"
	"kubevirt.io/kubevirt/pkg/libvmi"
	cd "kubevirt.io/kubevirt/tests/containerdisk"
	"kubevirt.io/kubevirt/tests/framework/kubevirt"
	"kubevirt.io/kubevirt/tests/framework/matcher"
	"kubevirt.io/kubevirt/tests/libstorage"
	"kubevirt.io/kubevirt/tests/libvmifact"
	"kubevirt.io/kubevirt/tests/testsuite"
)

// Tests for CNV-68916: CD-ROM hotplug bus validation in admission webhook
//
// These e2e tests validate that the storage admission webhook correctly
// accepts or rejects CD-ROM hotplug operations based on the bus type.
//
// Preconditions: OpenShift/Kubernetes cluster with KubeVirt and CDI installed.
// Steps: Create VMs with CD-ROM disks using various bus types and verify
//        admission responses through the Kubernetes API.
// Expected: scsi and sata buses are accepted; virtio bus is rejected.

var _ = Describe(SIG("CD-ROM Hotplug Bus Validation", func() {
	var virtClient kubecli.KubevirtClient

	const (
		cdromVolumeName = "cdrom-bus-test"
		volumeSize      = "1Gi"
	)

	BeforeEach(func() {
		virtClient = kubevirt.Client()
	})

	createVM := func(options ...libvmi.Option) *v1.VirtualMachine {
		vm := libvmi.NewVirtualMachine(
			libvmifact.NewCirros(options...),
			libvmi.WithRunStrategy(v1.RunStrategyAlways))
		vm, err := virtClient.VirtualMachine(testsuite.GetTestNamespace(nil)).Create(context.Background(), vm, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "failed to create VirtualMachine")
		Eventually(matcher.ThisVM(vm)).WithTimeout(300 * time.Second).WithPolling(time.Second).Should(matcher.BeReady(), "VM %s did not become ready in time", vm.Name)
		return vm
	}

	createCDRomDataVolume := func(namespace string) *cdiv1.DataVolume {
		url := "docker://" + cd.ContainerDiskFor(cd.ContainerDiskVirtio)
		dv := libdv.NewDataVolume(
			libdv.WithRegistryURLSourceAndPullMethod(url, cdiv1.RegistryPullNode),
			libdv.WithStorage(
				libdv.StorageWithVolumeSize(volumeSize),
			),
		)
		var err error
		dv, err = virtClient.CdiClient().CdiV1beta1().DataVolumes(namespace).Create(context.Background(), dv, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "failed to create CD-ROM DataVolume in namespace %s", namespace)
		return dv
	}

	patchVMWithCDRomVolume := func(vm *v1.VirtualMachine, volumeName, pvcName string, bus v1.DiskBus) (*v1.VirtualMachine, error) {
		vm, err := virtClient.VirtualMachine(vm.Namespace).Get(context.Background(), vm.Name, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred(), "failed to get VirtualMachine %s/%s", vm.Namespace, vm.Name)

		newVolumes := vm.DeepCopy().Spec.Template.Spec.Volumes
		newVolumes = append(newVolumes, v1.Volume{
			Name: volumeName,
			VolumeSource: v1.VolumeSource{
				DataVolume: &v1.DataVolumeSource{
					Name:         pvcName,
					Hotpluggable: true,
				},
			},
		})

		newDisks := vm.DeepCopy().Spec.Template.Spec.Domain.Devices.Disks
		newDisks = append(newDisks, v1.Disk{
			Name: volumeName,
			DiskDevice: v1.DiskDevice{
				CDRom: &v1.CDRomTarget{
					Bus: bus,
				},
			},
		})

		patchObj := patch.New()
		patchObj.AddOption(patch.WithReplace("/spec/template/spec/volumes", newVolumes))
		patchObj.AddOption(patch.WithReplace("/spec/template/spec/domain/devices/disks", newDisks))
		patchBytes, err := patchObj.GeneratePayload()
		Expect(err).ToNot(HaveOccurred(), "failed to generate patch payload")

		return virtClient.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.JSONPatchType, patchBytes, metav1.PatchOptions{})
	}

	Context("Bus type validation for CD-ROM hotplug", func() {
		It("should accept hotplug of CD-ROM with scsi bus", func() {
			By("Creating a VM with an empty CD-ROM on sata bus")
			vm := createVM(libvmi.WithEmptyCDRom(v1.DiskBusSATA, "initial-cdrom"))

			By("Creating a DataVolume for CD-ROM content")
			dv := createCDRomDataVolume(vm.Namespace)
			libstorage.EventuallyDV(dv, 240, matcher.HaveSucceeded())

			By("Hotplugging a CD-ROM volume with scsi bus")
			vm, err := patchVMWithCDRomVolume(vm, cdromVolumeName, dv.Name, v1.DiskBusSCSI)
			Expect(err).ToNot(HaveOccurred(), "CD-ROM hotplug with scsi bus should be accepted")

			By("Verifying the hotplug completed successfully")
			libstorage.WaitForHotplugToComplete(virtClient, vm, cdromVolumeName, dv.Name, true)
		})

		It("should accept hotplug of CD-ROM with sata bus", func() {
			By("Creating a VM with an empty CD-ROM on sata bus")
			vm := createVM(libvmi.WithEmptyCDRom(v1.DiskBusSATA, "initial-cdrom"))

			By("Creating a DataVolume for CD-ROM content")
			dv := createCDRomDataVolume(vm.Namespace)
			libstorage.EventuallyDV(dv, 240, matcher.HaveSucceeded())

			By("Hotplugging a CD-ROM volume with sata bus")
			vm, err := patchVMWithCDRomVolume(vm, cdromVolumeName, dv.Name, v1.DiskBusSATA)
			Expect(err).ToNot(HaveOccurred(), "CD-ROM hotplug with sata bus should be accepted")

			By("Verifying the hotplug completed successfully")
			libstorage.WaitForHotplugToComplete(virtClient, vm, cdromVolumeName, dv.Name, true)
		})

		It("should reject hotplug of CD-ROM with virtio bus", func() {
			By("Creating a VM with an empty CD-ROM on sata bus")
			vm := createVM(libvmi.WithEmptyCDRom(v1.DiskBusSATA, "initial-cdrom"))

			By("Creating a DataVolume for CD-ROM content")
			dv := createCDRomDataVolume(vm.Namespace)
			libstorage.EventuallyDV(dv, 240, matcher.HaveSucceeded())

			By("Attempting to hotplug a CD-ROM volume with virtio bus")
			_, err := patchVMWithCDRomVolume(vm, cdromVolumeName, dv.Name, v1.DiskBusVirtio)
			Expect(err).To(HaveOccurred(), "CD-ROM hotplug with virtio bus should be rejected")
			Expect(err.Error()).To(ContainSubstring("requires bus to be 'scsi' or 'sata'"),
				"error should indicate that only scsi and sata buses are allowed for CD-ROM")
			Expect(err.Error()).To(ContainSubstring("virtio"),
				"error should mention the rejected bus type")
		})

		It("should reject hotplug of disk with no device type and mention CD-ROM in error", func() {
			By("Creating a VM")
			vm := createVM()

			By("Creating a DataVolume")
			dv := createCDRomDataVolume(vm.Namespace)
			libstorage.EventuallyDV(dv, 240, matcher.HaveSucceeded())

			By("Attempting to hotplug a volume with no disk device type set")
			vm, getErr := virtClient.VirtualMachine(vm.Namespace).Get(context.Background(), vm.Name, metav1.GetOptions{})
			Expect(getErr).ToNot(HaveOccurred())

			newVolumes := vm.DeepCopy().Spec.Template.Spec.Volumes
			newVolumes = append(newVolumes, v1.Volume{
				Name: "no-device-type",
				VolumeSource: v1.VolumeSource{
					DataVolume: &v1.DataVolumeSource{
						Name:         dv.Name,
						Hotpluggable: true,
					},
				},
			})

			newDisks := vm.DeepCopy().Spec.Template.Spec.Domain.Devices.Disks
			newDisks = append(newDisks, v1.Disk{
				Name: "no-device-type",
				// No DiskDevice set — neither Disk, LUN, nor CDRom
			})

			patchObj := patch.New()
			patchObj.AddOption(patch.WithReplace("/spec/template/spec/volumes", newVolumes))
			patchObj.AddOption(patch.WithReplace("/spec/template/spec/domain/devices/disks", newDisks))
			patchBytes, patchErr := patchObj.GeneratePayload()
			Expect(patchErr).ToNot(HaveOccurred())

			_, err := virtClient.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.JSONPatchType, patchBytes, metav1.PatchOptions{})
			Expect(err).To(HaveOccurred(), "hotplug with no device type should be rejected")
			Expect(err.Error()).To(ContainSubstring("CD-ROM"),
				"error should mention CD-ROM as a valid device type")
		})
	})
}))
