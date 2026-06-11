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

package virthandler

/*
VMI Phase Calculation During virt-handler Rolling Update — Nil Domain Tests

STP Reference: outputs/stp/CNV-89519/CNV-89519_test_plan.md
Jira: CNV-89519

These tests validate calculateVmPhaseForStatusReason behavior when the domain
is nil (not found during scan), which is the code path that caused false
VMI Failed transitions during virt-handler rolling updates.

The key decision point is at vm.go:2196-2220: when domain is nil and VMI is
scheduled, the function checks whether the launcher client is responsive.
If the launcher is responsive, the VMI should NOT transition to Failed.
If the launcher is unresponsive, the VMI should transition to Failed.
*/

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"

	backupv1 "kubevirt.io/api/backup/v1alpha1"
	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
	kubevirtfake "kubevirt.io/client-go/kubevirt/fake"

	"kubevirt.io/kubevirt/pkg/certificates"
	diskutils "kubevirt.io/kubevirt/pkg/ephemeral-disk-utils"
	"kubevirt.io/kubevirt/pkg/hypervisor"
	"kubevirt.io/kubevirt/pkg/libvmi"
	"kubevirt.io/kubevirt/pkg/safepath"
	"kubevirt.io/kubevirt/pkg/testutils"
	virtcache "kubevirt.io/kubevirt/pkg/virt-handler/cache"
	"kubevirt.io/kubevirt/pkg/virt-handler/cgroup"
	cmdclient "kubevirt.io/kubevirt/pkg/virt-handler/cmd-client"
	containerdisk "kubevirt.io/kubevirt/pkg/virt-handler/container-disk"
	hotplugvolume "kubevirt.io/kubevirt/pkg/virt-handler/hotplug-disk"
	"kubevirt.io/kubevirt/pkg/virt-handler/isolation"
	launcherclients "kubevirt.io/kubevirt/pkg/virt-handler/launcher-clients"
	migrationproxy "kubevirt.io/kubevirt/pkg/virt-handler/migration-proxy"
	notifyserver "kubevirt.io/kubevirt/pkg/virt-handler/notify-server"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
)

var _ = Describe("[CNV-89519] calculateVmPhaseForStatusReason with nil domain", func() {
	var controller *VirtualMachineController
	var launcherClientManager *launcherclients.MockLauncherClientManager
	var recorder *record.FakeRecorder
	var mockCgroupManager *cgroup.MockManager
	var stop chan struct{}
	var wg *sync.WaitGroup

	const host = "master"

	getCgroupManager = func(_ *v1.VirtualMachineInstance, _ string, _ hypervisor.HypervisorNodeInformation, _ bool) (cgroup.Manager, error) {
		return mockCgroupManager, nil
	}

	BeforeEach(func() {
		diskutils.MockDefaultOwnershipManager()

		wg = &sync.WaitGroup{}
		stop = make(chan struct{})
		eventChan := make(chan watch.Event, 100)
		shareDir := GinkgoT().TempDir()
		privateDir := GinkgoT().TempDir()
		podsDir, err := os.MkdirTemp("", "")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(os.RemoveAll, podsDir)
		certDir := GinkgoT().TempDir()

		vmiShareDir := GinkgoT().TempDir()
		ghostCacheDir := GinkgoT().TempDir()

		_ = virtcache.InitializeGhostRecordCache(virtcache.NewIterableCheckpointManager(ghostCacheDir))

		Expect(os.MkdirAll(filepath.Join(vmiShareDir, "var", "run", "kubevirt"), 0755)).To(Succeed())

		cmdclient.SetPodsBaseDir(podsDir)

		store, err := certificates.GenerateSelfSignedCert(certDir, "test", "test")
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true,
			GetCertificate: func(info *tls.ClientHelloInfo) (certificate *tls.Certificate, e error) {
				return store.Current()
			},
		}
		Expect(err).ToNot(HaveOccurred())

		vmiInformer, _ := testutils.NewFakeInformerFor(&v1.VirtualMachineInstance{})
		domainInformer, _ := testutils.NewFakeInformerFor(&api.Domain{})
		recorder = record.NewFakeRecorder(100)
		recorder.IncludeObject = true

		k8sfakeClient := fake.NewSimpleClientset()
		virtfakeClient := kubevirtfake.NewSimpleClientset()
		ctrl := gomock.NewController(GinkgoT())
		virtClient := kubecli.NewMockKubevirtClient(ctrl)
		virtClient.EXPECT().CoreV1().Return(k8sfakeClient.CoreV1()).AnyTimes()
		virtClient.EXPECT().VirtualMachineInstance(metav1.NamespaceDefault).Return(virtfakeClient.KubevirtV1().VirtualMachineInstances(metav1.NamespaceDefault)).AnyTimes()
		kv := &v1.KubeVirtConfiguration{}
		config, _, _ := testutils.NewFakeClusterConfigUsingKVConfig(kv)

		Expect(os.MkdirAll(filepath.Join(vmiShareDir, "dev"), 0755)).To(Succeed())
		f, err := os.OpenFile(filepath.Join(vmiShareDir, "dev", "kvm"), os.O_CREATE, 0755)
		Expect(err).ToNot(HaveOccurred())
		Expect(f.Close()).To(Succeed())

		mockIsolationResult := isolation.NewMockIsolationResult(ctrl)
		mockIsolationResult.EXPECT().Pid().Return(1).AnyTimes()
		rootDir, err := safepath.JoinAndResolveWithRelativeRoot(vmiShareDir)
		Expect(err).ToNot(HaveOccurred())
		mockIsolationResult.EXPECT().MountRoot().Return(rootDir, nil).AnyTimes()

		mockIsolationDetector := isolation.NewMockPodIsolationDetector(ctrl)
		mockIsolationDetector.EXPECT().Detect(gomock.Any()).Return(mockIsolationResult, nil).AnyTimes()

		mockContainerDiskMounter := containerdisk.NewMockMounter(ctrl)
		mockHotplugVolumeMounter := hotplugvolume.NewMockVolumeMounter(ctrl)
		mockCgroupManager = cgroup.NewMockManager(ctrl)

		migrationProxy := migrationproxy.NewMigrationProxyManager(tlsConfig, tlsConfig, tlsConfig, config)
		fakeDownwardMetricsManager := newFakeManager()

		launcherClientManager = &launcherclients.MockLauncherClientManager{
			Initialized: true,
		}
		fakeNodeInformer, _ := testutils.NewFakeInformerFor(&k8sv1.Node{})
		fakeNodeStore := fakeNodeInformer.GetStore()
		fakeBackupTrackerInformer, _ := testutils.NewFakeInformerFor(&backupv1.VirtualMachineBackupTracker{})
		cbtHandler := NewCBTHandler(virtClient, fakeBackupTrackerInformer)

		controller, _ = NewVirtualMachineController(
			recorder,
			virtClient,
			fakeNodeStore,
			host,
			privateDir,
			podsDir,
			launcherClientManager,
			vmiInformer,
			vmiInformer.GetStore(),
			domainInformer,
			10,
			config,
			mockIsolationDetector,
			migrationProxy,
			fakeDownwardMetricsManager,
			mockContainerDiskMounter,
			mockHotplugVolumeMounter,
			"",
			nil, // capabilities
			"",  // host cpu model
			&netConfStub{},
			&netStatStub{},
			cbtHandler,
		)

		controller.hotplugVolumeMounter = mockHotplugVolumeMounter

		vmiTestUUID := uuid.NewUUID()
		podTestUUID := uuid.NewUUID()
		sockFile := cmdclient.SocketFilePathOnHost(string(podTestUUID))
		Expect(os.MkdirAll(filepath.Dir(sockFile), 0755)).To(Succeed())
		f, err = os.Create(sockFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(f.Close()).To(Succeed())

		mockQueue := testutils.NewMockWorkQueue(controller.queue)
		controller.queue = mockQueue

		client := cmdclient.NewMockLauncherClient(ctrl)
		clientInfo := &virtcache.LauncherClientInfo{
			Client:             client,
			SocketFile:         sockFile,
			DomainPipeStopChan: make(chan struct{}),
			Ready:              true,
		}
		launcherClientManager.Client = client
		launcherClientManager.ClientInfo = clientInfo

		// Store UIDs for use in tests (via closure)
		_ = vmiTestUUID

		wg.Add(1)
		go func() {
			err := notifyserver.RunServer(shareDir, stop, eventChan, nil, nil)
			wg.Done()
			Expect(err).ToNot(HaveOccurred())
		}()
		time.Sleep(1 * time.Second)
	})

	AfterEach(func() {
		close(stop)
		wg.Wait()
		// Drain events
		for len(recorder.Events) > 0 {
			<-recorder.Events
		}
	})

	/*
	Preconditions:
	    - VMI in Running phase (IsScheduled() returns false, IsRunning() returns true)
	    - Domain is nil (not found during scan)
	    - Launcher client manager configured as responsive

	Steps:
	    1. Create a VMI in Running phase with a node name assigned
	    2. Configure the launcher client manager as responsive (not unresponsive)
	    3. Call calculateVmPhaseForStatusReason with nil domain and the VMI

	Expected:
	    - Phase is NOT Failed; VMI should not be incorrectly transitioned
	*/
	It("[test_id:TS-CNV-89519-005] should not transition to Failed when launcher is responsive and domain is nil", func() {
		vmi := libvmi.New(
			libvmi.WithNamespace(metav1.NamespaceDefault),
		)
		vmi.UID = types.UID(uuid.NewUUID())
		vmi.Status.Phase = v1.Running
		vmi.Status.NodeName = host
		// Set ActivePods so VMI is considered scheduled
		vmi.Status.ActivePods = map[types.UID]string{
			types.UID(uuid.NewUUID()): host,
		}

		// Configure launcher as responsive (initialized, NOT unresponsive)
		launcherClientManager.Initialized = true
		launcherClientManager.UnResponsive = false

		phase, err := controller.calculateVmPhaseForStatusReason(nil, vmi)
		Expect(err).ToNot(HaveOccurred())
		Expect(phase).ToNot(Equal(v1.Failed),
			"VMI should not transition to Failed when launcher is responsive and domain is nil during virt-handler restart")
	})

	/*
	Preconditions:
	    - VMI in Running phase
	    - Domain is nil (not found during scan)
	    - Launcher client manager configured as unresponsive

	Steps:
	    1. Create a VMI in Running phase with a node name assigned
	    2. Configure the launcher client manager as unresponsive
	    3. Call calculateVmPhaseForStatusReason with nil domain and the VMI

	Expected:
	    - Phase IS Failed; unresponsive launcher with nil domain should transition to Failed
	*/
	It("[test_id:TS-CNV-89519-005b] should transition to Failed when launcher is unresponsive and domain is nil", func() {
		vmi := libvmi.New(
			libvmi.WithNamespace(metav1.NamespaceDefault),
		)
		vmi.UID = types.UID(uuid.NewUUID())
		vmi.Status.Phase = v1.Running
		vmi.Status.NodeName = host
		vmi.Status.ActivePods = map[types.UID]string{
			types.UID(uuid.NewUUID()): host,
		}

		// Configure launcher as unresponsive
		launcherClientManager.Initialized = true
		launcherClientManager.UnResponsive = true

		phase, err := controller.calculateVmPhaseForStatusReason(nil, vmi)
		Expect(err).ToNot(HaveOccurred())
		Expect(phase).To(Equal(v1.Failed),
			"VMI should transition to Failed when launcher is unresponsive and domain is nil")
	})
})
