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
	"fmt"
	"io"
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"kubevirt.io/kubevirt/tests/decorators"
)

// startEchoServer starts a TCP echo server that echoes back any data received.
// Returns the listener and its address. Caller must close the listener.
func startEchoServer() (net.Listener, string) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	return listener, listener.Addr().String()
}

var _ = Describe("[CNV-76508] Cross-Cluster Migration Proxy - Unit", decorators.SigNetwork, func() {
	/*
		Unit tests for the ProxyMappingManager component.
		These tests validate proxy port lifecycle, SSRF protection,
		connection concurrency limits, and bidirectional forwarding.

		No cluster access required — pure Go unit tests.
	*/

	Context("Proxy port opening and mapping", func() {
		var manager *ProxyMappingManager
		var migrationID string

		BeforeEach(func() {
			manager = NewProxyMappingManager()
			migrationID = "test-migration-001"
		})

		AfterEach(func() {
			if manager != nil {
				manager.Close(migrationID)
			}
		})

		It("[test_id:TS-CNV-76508-001] should open proxy ports and return valid remapped port map", func() {
			By("Calling OpenProxyPorts with a 2-port map")
			portMap := map[int]int{49152: 49152, 49153: 49153}
			remappedPorts, err := manager.OpenProxyPorts(migrationID, "127.0.0.1", "10.0.0.1:8080", portMap)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying remapped port map has correct number of entries")
			Expect(remappedPorts).To(HaveLen(2))

			By("Verifying each remapped port is a valid listening TCP port")
			for _, port := range remappedPorts {
				conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
				ExpectWithOffset(1, dialErr).ToNot(HaveOccurred(), "remapped port %d should be listening", port)
				conn.Close()
			}

			By("Verifying HasMappings returns true for the migration ID")
			Expect(manager.HasMappings(migrationID)).To(BeTrue())
		})
	})

	Context("Proxy port cleanup on close", func() {
		var manager *ProxyMappingManager
		var migrationID string
		var remappedPorts map[int]int

		BeforeEach(func() {
			manager = NewProxyMappingManager()
			migrationID = "test-migration-cleanup"
			portMap := map[int]int{49152: 49152, 49153: 49153}
			var err error
			remappedPorts, err = manager.OpenProxyPorts(migrationID, "127.0.0.1", "10.0.0.1:8080", portMap)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		})

		It("[test_id:TS-CNV-76508-002] should close all listeners and remove mappings on Close()", func() {
			By("Calling Close() with the migration ID")
			manager.Close(migrationID)

			By("Verifying HasMappings returns false after Close()")
			Expect(manager.HasMappings(migrationID)).To(BeFalse())

			By("Verifying connections are refused on all previously opened ports")
			for _, port := range remappedPorts {
				_, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
				Expect(err).To(HaveOccurred(), "port %d should refuse connections after Close()", port)
			}
		})
	})

	Context("SSRF protection", func() {
		var manager *ProxyMappingManager
		var migrationID string

		BeforeEach(func() {
			manager = NewProxyMappingManager()
			migrationID = "test-migration-ssrf"
		})

		AfterEach(func() {
			if manager != nil {
				manager.Close(migrationID)
			}
		})

		DescribeTable("[test_id:TS-CNV-76508-003] should block proxy connections to link-local addresses",
			func(targetAddr string) {
				portMap := map[int]int{49152: 49152}

				By(fmt.Sprintf("Attempting to open proxy ports with target address %s", targetAddr))
				_, err := manager.OpenProxyPorts(migrationID, "127.0.0.1", targetAddr, portMap)

				By("Verifying error is returned with 'not allowed' message")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not allowed"))

				By("Verifying no listeners were opened for blocked addresses")
				Expect(manager.HasMappings(migrationID)).To(BeFalse())
			},
			Entry("cloud metadata IPv4", "169.254.169.254:8080"),
			Entry("link-local IPv4", "169.254.1.1:443"),
			Entry("link-local IPv6", "[fe80::1]:8080"),
		)
	})

	Context("Connection concurrency limit", func() {
		var manager *ProxyMappingManager
		var migrationID string
		var connections []net.Conn
		var echoListener net.Listener

		BeforeEach(func() {
			manager = NewProxyMappingManager()
			migrationID = "test-migration-concurrency"
			connections = nil

			By("Starting a target echo TCP server")
			var echoAddr string
			echoListener, echoAddr = startEchoServer()

			By("Opening proxy port targeting echo server")
			portMap := map[int]int{49152: 49152}
			_, err := manager.OpenProxyPorts(migrationID, "127.0.0.1", echoAddr, portMap)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			for _, conn := range connections {
				conn.Close()
			}
			if manager != nil {
				manager.Close(migrationID)
			}
			if echoListener != nil {
				echoListener.Close()
			}
		})

		It("[test_id:TS-CNV-76508-004] should enforce 64-connection limit per proxy mapping", func() {
			By("Opening 64 concurrent connections - all should succeed")
			remappedPorts := manager.GetRemappedPorts(migrationID)
			Expect(remappedPorts).ToNot(BeEmpty())

			var proxyPort int
			for _, p := range remappedPorts {
				proxyPort = p
				break
			}
			proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)

			for i := 0; i < 64; i++ {
				conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
				ExpectWithOffset(1, err).ToNot(HaveOccurred(), "connection %d should succeed", i+1)
				connections = append(connections, conn)
			}

			By("Attempting to open a 65th connection - should be rejected")
			conn, err := net.DialTimeout("tcp", proxyAddr, time.Second)
			if err == nil {
				conn.Close()
			}
			Expect(err).To(HaveOccurred(), "65th connection should be rejected")
		})
	})

	Context("Bidirectional traffic forwarding", func() {
		var manager *ProxyMappingManager
		var migrationID string
		var echoListener net.Listener

		BeforeEach(func() {
			manager = NewProxyMappingManager()
			migrationID = "test-migration-bidir"

			By("Starting a target TCP echo server")
			var echoAddr string
			echoListener, echoAddr = startEchoServer()

			By("Opening proxy port targeting echo server")
			portMap := map[int]int{49152: 49152}
			_, err := manager.OpenProxyPorts(migrationID, "127.0.0.1", echoAddr, portMap)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			if manager != nil {
				manager.Close(migrationID)
			}
			if echoListener != nil {
				echoListener.Close()
			}
		})

		It("[test_id:TS-CNV-76508-005] should forward data correctly in both directions through proxy", func() {
			remappedPorts := manager.GetRemappedPorts(migrationID)
			Expect(remappedPorts).ToNot(BeEmpty())

			var proxyPort int
			for _, p := range remappedPorts {
				proxyPort = p
				break
			}
			proxyAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)

			By("Connecting to the proxy port")
			conn, err := net.DialTimeout("tcp", proxyAddr, 2*time.Second)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			defer conn.Close()

			testData := []byte("Hello, cross-cluster migration proxy!")

			By("Sending data client -> proxy -> target")
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, err = conn.Write(testData)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Reading echo response target -> proxy -> client")
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			response := make([]byte, len(testData))
			_, err = io.ReadFull(conn, response)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())

			By("Verifying data integrity")
			Expect(response).To(Equal(testData))
		})
	})
})
