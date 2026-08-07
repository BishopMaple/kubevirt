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

package cache

import (
	"fmt"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	k8scache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	cmdclient "kubevirt.io/kubevirt/pkg/virt-handler/cmd-client"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
)

var _ = Describe("Domain Watcher", func() {
	Context("listSockets ", func() {
		It("should return socket list from ghost record cache", func() {
			const podUID = "5678"
			const socketPath = "/path/to/domainsock"

			ghostCacheDir := GinkgoT().TempDir()

			ghostRecordStore := InitializeGhostRecordCache(NewIterableCheckpointManager(ghostCacheDir))

			err := ghostRecordStore.Add("test-ns", "test-domain", socketPath, podUID)
			Expect(err).ToNot(HaveOccurred())

			socketFiles, err := listSockets(ghostRecordStore.list())
			Expect(err).ToNot(HaveOccurred())
			Expect(socketFiles).To(HaveLen(1))
			Expect(socketFiles[0]).To(Equal(socketPath))

		})
	})

	Context("consecutive failure panic", func() {
		It("should panic after reaching max consecutive failures", func() {
			origMax := notifyServerMaxConsecutiveFails
			origHealthy := notifyServerHealthyRunTime
			defer func() {
				notifyServerMaxConsecutiveFails = origMax
				notifyServerHealthyRunTime = origHealthy
			}()
			notifyServerMaxConsecutiveFails = 1
			notifyServerHealthyRunTime = 1 * time.Hour

			d := &domainWatcher{
				virtShareDir:        GinkgoT().TempDir(),
				watchdogTimeout:     10,
				unresponsiveSockets: make(map[string]int64),
				resyncPeriod:        1 * time.Hour,
				runServer: func(string, chan struct{}, chan watch.Event, record.EventRecorder, k8scache.Store, ...time.Duration) error {
					return fmt.Errorf("permanent failure")
				},
				eventChan: make(chan watch.Event, 100),
				stopChan:  make(chan struct{}),
			}
			d.wg.Add(1)

			Expect(d.worker).To(PanicWith(
				ContainSubstring("domain notify server reached max consecutive failures")))
		})
	})

	Context("getDomainWithRetry", func() {
		It("should return domain on first successful attempt", func() {
			expectedDomain := api.NewMinimalDomainWithNS("test-ns", "test-domain")
			expectedDomain.ObjectMeta.UID = types.UID("test-uid")

			factory := func(_ string) (cmdclient.LauncherClient, error) {
				return &fakeLauncherClient{domain: expectedDomain, exists: true}, nil
			}

			domain := getDomainWithRetry("/fake/socket", 3, 1*time.Millisecond, factory)
			Expect(domain).ToNot(BeNil())
			Expect(domain.ObjectMeta.Name).To(Equal("test-domain"))
			Expect(domain.ObjectMeta.Namespace).To(Equal("test-ns"))
		})

		It("should retry and succeed after transient connection failures", func() {
			expectedDomain := api.NewMinimalDomainWithNS("test-ns", "test-domain")
			expectedDomain.ObjectMeta.UID = types.UID("test-uid")

			var attempts int32
			factory := func(_ string) (cmdclient.LauncherClient, error) {
				attempt := atomic.AddInt32(&attempts, 1)
				if attempt <= 2 {
					return nil, fmt.Errorf("connection refused")
				}
				return &fakeLauncherClient{domain: expectedDomain, exists: true}, nil
			}

			domain := getDomainWithRetry("/fake/socket", 3, 1*time.Millisecond, factory)
			Expect(domain).ToNot(BeNil())
			Expect(domain.ObjectMeta.Name).To(Equal("test-domain"))
			Expect(atomic.LoadInt32(&attempts)).To(BeNumerically("==", 3))
		})

		It("should retry and succeed after transient GetDomain failures", func() {
			expectedDomain := api.NewMinimalDomainWithNS("test-ns", "test-domain")

			var attempts int32
			factory := func(_ string) (cmdclient.LauncherClient, error) {
				attempt := atomic.AddInt32(&attempts, 1)
				if attempt == 1 {
					return &fakeLauncherClient{err: fmt.Errorf("libvirt unavailable")}, nil
				}
				return &fakeLauncherClient{domain: expectedDomain, exists: true}, nil
			}

			domain := getDomainWithRetry("/fake/socket", 3, 1*time.Millisecond, factory)
			Expect(domain).ToNot(BeNil())
			Expect(domain.ObjectMeta.Name).To(Equal("test-domain"))
			Expect(atomic.LoadInt32(&attempts)).To(BeNumerically("==", 2))
		})

		It("should return nil after exhausting all retries", func() {
			factory := func(_ string) (cmdclient.LauncherClient, error) {
				return nil, fmt.Errorf("connection refused")
			}

			domain := getDomainWithRetry("/fake/socket", 2, 1*time.Millisecond, factory)
			Expect(domain).To(BeNil())
		})

		It("should return nil when domain does not exist", func() {
			factory := func(_ string) (cmdclient.LauncherClient, error) {
				return &fakeLauncherClient{exists: false}, nil
			}

			domain := getDomainWithRetry("/fake/socket", 3, 1*time.Millisecond, factory)
			Expect(domain).To(BeNil())
		})
	})

	Context("Stop() idempotency", func() {
		It("should not panic when Stop is called twice", func() {
			d := &domainWatcher{
				virtShareDir:        GinkgoT().TempDir(),
				watchdogTimeout:     1,
				unresponsiveSockets: make(map[string]int64),
				resyncPeriod:        1 * time.Hour,
				runServer: func(string, chan struct{}, chan watch.Event, record.EventRecorder, k8scache.Store, ...time.Duration) error {
					return fmt.Errorf("injected error")
				},
			}

			Expect(d.startBackground()).To(Succeed())
			Eventually(func() bool {
				d.lock.Lock()
				defer d.lock.Unlock()
				return !d.backgroundWatcherStarted
			}, 5*time.Second).Should(BeTrue())

			Expect(func() { d.Stop() }).ShouldNot(Panic())
			Expect(func() { d.Stop() }).ShouldNot(Panic())
		})
	})
})

// fakeLauncherClient is a minimal fake implementing cmdclient.LauncherClient
// for testing getDomainWithRetry without a real gRPC connection.
type fakeLauncherClient struct {
	cmdclient.LauncherClient
	domain *api.Domain
	exists bool
	err    error
}

func (f *fakeLauncherClient) GetDomain() (*api.Domain, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	return f.domain, f.exists, nil
}

func (f *fakeLauncherClient) Close() {}
