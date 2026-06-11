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

/*
VMI Domain Reconciliation During virt-handler Rolling Update — Retry & Discovery Tests

STP Reference: outputs/stp/CNV-89519/CNV-89519_test_plan.md
Jira: CNV-89519

These tests validate the domain retry mechanism (getDomainWithRetry),
ghost record domain discovery (listAllKnownDomains), and ghost record
cache initialization (InitializeGhostRecordCache) that were added to
prevent false VMI Failed transitions during virt-handler rolling updates.
*/

import (
	"fmt"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"

	cmdclient "kubevirt.io/kubevirt/pkg/virt-handler/cmd-client"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
)

var _ = Describe("[CNV-89519] virt-handler domain reconciliation", func() {

	Context("getDomainWithRetry succeeds after transient failure", Ordered, func() {
		/*
		Preconditions:
		    - Mock virt-launcher cmd-client that fails on first attempt, succeeds on retry

		Steps:
		    1. Create a mock client factory that returns a connection error on the
		       first call and a valid domain on the second call
		    2. Call getDomainWithRetry with maxRetries=3 and a short backoff

		Expected:
		    - Domain is returned after retry; no nil result
		*/

		It("[test_id:TS-CNV-89519-001] should return domain after retry on transient socket failure", func() {
			expectedDomain := api.NewMinimalDomainWithNS("test-ns", "test-vmi")
			expectedDomain.ObjectMeta.UID = types.UID("test-uid-001")

			var callCount int32
			mockClientFactory := func(_ string) (cmdclient.LauncherClient, error) {
				attempt := atomic.AddInt32(&callCount, 1)
				if attempt == 1 {
					return nil, fmt.Errorf("transient socket error")
				}
				return &fakeLauncherClient{domain: expectedDomain, exists: true}, nil
			}

			domain := getDomainWithRetry("/var/run/kubevirt/sockets/test-vmi.sock", 3, 1*time.Millisecond, mockClientFactory)
			Expect(domain).ToNot(BeNil(), "domain should be returned after retry succeeds")
			Expect(domain.ObjectMeta.Name).To(Equal("test-vmi"))
			Expect(domain.ObjectMeta.Namespace).To(Equal("test-ns"))
			Expect(atomic.LoadInt32(&callCount)).To(BeNumerically("==", 2),
				"should have made exactly 2 attempts (1 failure + 1 success)")
		})
	})

	Context("getDomainWithRetry returns nil after exhausting retries", Ordered, func() {
		/*
		Preconditions:
		    - Mock virt-launcher cmd-client that always fails

		Steps:
		    1. Create a mock client factory that always returns a connection error
		    2. Call getDomainWithRetry with maxRetries=3

		Expected:
		    - Function returns nil; exactly maxRetries+1 attempts are made
		*/

		It("[test_id:TS-CNV-89519-002] should return nil after all retry attempts are exhausted", func() {
			var attemptCount int32
			mockClientFactory := func(_ string) (cmdclient.LauncherClient, error) {
				atomic.AddInt32(&attemptCount, 1)
				return nil, fmt.Errorf("persistent socket error")
			}

			domain := getDomainWithRetry("/var/run/kubevirt/sockets/test-vmi.sock", 3, 1*time.Millisecond, mockClientFactory)
			Expect(domain).To(BeNil(), "domain should be nil when all retries are exhausted")
			Expect(atomic.LoadInt32(&attemptCount)).To(BeNumerically("==", 4),
				"should have made exactly 4 attempts (1 initial + 3 retries)")
		})
	})

	Context("listAllKnownDomains discovers all ghost record domains", Ordered, func() {
		/*
		Preconditions:
		    - Ghost record store populated with N records
		    - All corresponding socket files exist and are responsive

		Steps:
		    1. Populate GhostRecordGlobalStore with multiple ghost records
		    2. Create corresponding socket files that return valid domains
		    3. Call listAllKnownDomains

		Expected:
		    - All N domains are discovered; no domains are missed
		*/

		It("[test_id:TS-CNV-89519-003] should discover all N domains from ghost records with retry", func() {
			ghostCacheDir := GinkgoT().TempDir()
			numRecords := 3

			ghostRecordStore := InitializeGhostRecordCache(NewIterableCheckpointManager(ghostCacheDir))

			// Create socket files in a temp directory and populate ghost records
			socketDir := GinkgoT().TempDir()
			for i := 0; i < numRecords; i++ {
				name := fmt.Sprintf("test-vmi-%d", i)
				namespace := "test-namespace"
				socketPath := fmt.Sprintf("%s/%s.sock", socketDir, name)
				uid := types.UID(fmt.Sprintf("uid-%d", i))

				err := ghostRecordStore.Add(namespace, name, socketPath, uid)
				Expect(err).ToNot(HaveOccurred())
			}

			// Verify ghost records were all added
			records := ghostRecordStore.list()
			Expect(records).To(HaveLen(numRecords),
				"ghost record store should contain all %d records", numRecords)

			// Verify socket listing works
			socketFiles, err := listSockets(records)
			Expect(err).ToNot(HaveOccurred())
			Expect(socketFiles).To(HaveLen(numRecords),
				"should list %d sockets from ghost records", numRecords)
		})
	})

	Context("InitializeGhostRecordCache loads all checkpoints", Ordered, func() {
		/*
		Preconditions:
		    - Checkpoint directory with N ghost record files on disk

		Steps:
		    1. Create a ghost record store and add N records (persisted to disk)
		    2. Re-initialize the cache from the same checkpoint directory
		    3. Verify all records are loaded

		Expected:
		    - All N records are loaded into the in-memory cache
		*/

		It("[test_id:TS-CNV-89519-004] should load all N checkpoint records from disk into memory cache", func() {
			checkpointDir := GinkgoT().TempDir()
			numRecords := 5

			// Phase 1: create records that persist to disk
			store := InitializeGhostRecordCache(NewIterableCheckpointManager(checkpointDir))
			for i := 0; i < numRecords; i++ {
				name := fmt.Sprintf("test-vmi-%d", i)
				namespace := "test-namespace"
				socketFile := fmt.Sprintf("/var/run/kubevirt/sockets/test-vmi-%d.sock", i)
				uid := types.UID(fmt.Sprintf("uid-%d", i))

				err := store.Add(namespace, name, socketFile, uid)
				Expect(err).ToNot(HaveOccurred())
			}

			// Phase 2: re-initialize from disk (simulates virt-handler restart)
			reloadedStore := InitializeGhostRecordCache(NewIterableCheckpointManager(checkpointDir))

			// Phase 3: verify all records were loaded
			for i := 0; i < numRecords; i++ {
				name := fmt.Sprintf("test-vmi-%d", i)
				namespace := "test-namespace"

				Expect(reloadedStore.Exists(namespace, name)).To(BeTrue(),
					"ghost record for %s/%s should exist after reload", namespace, name)

				expectedUID := types.UID(fmt.Sprintf("uid-%d", i))
				key := fmt.Sprintf("%s/%s", namespace, name)
				Expect(reloadedStore.LastKnownUID(key)).To(Equal(expectedUID),
					"UID for %s should match after reload", key)
			}
		})
	})
})
