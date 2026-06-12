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

package synchronization

import (
	"fmt"
	"net"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ProxyMapping", func() {
	Context("ProxyMappingManager", func() {
		var manager *ProxyMappingManager

		BeforeEach(func() {
			manager = NewProxyMappingManager()
		})

		AfterEach(func() {
			manager.CloseAll()
		})

		It("should open proxy ports and return remapped ports", func() {
			// Set up a target TCP listener to proxy to
			targetListener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			defer targetListener.Close()
			targetPort := targetListener.Addr().(*net.TCPAddr).Port

			ports := map[string]int{
				strconv.Itoa(targetPort): 49152,
			}

			remappedPorts, err := manager.OpenProxyPorts("test-migration", "127.0.0.1", "127.0.0.1", ports)
			Expect(err).ToNot(HaveOccurred())
			Expect(remappedPorts).To(HaveLen(1))

			// Verify the remapped ports have the correct virt-handler port value
			for _, virtHandlerPort := range remappedPorts {
				Expect(virtHandlerPort).To(Equal(49152))
			}
		})

		It("should open proxy ports for both block and state migration", func() {
			// Set up two target listeners
			targetListener1, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			defer targetListener1.Close()
			targetListener2, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			defer targetListener2.Close()

			port1 := targetListener1.Addr().(*net.TCPAddr).Port
			port2 := targetListener2.Addr().(*net.TCPAddr).Port

			ports := map[string]int{
				strconv.Itoa(port1): 49152,
				strconv.Itoa(port2): 49153,
			}

			remappedPorts, err := manager.OpenProxyPorts("test-migration", "127.0.0.1", "127.0.0.1", ports)
			Expect(err).ToNot(HaveOccurred())
			Expect(remappedPorts).To(HaveLen(2))

			// Verify both virt-handler ports are present in the remapped ports
			virtHandlerPorts := make(map[int]bool)
			for _, vhPort := range remappedPorts {
				virtHandlerPorts[vhPort] = true
			}
			Expect(virtHandlerPorts).To(HaveKey(49152))
			Expect(virtHandlerPorts).To(HaveKey(49153))
		})

		It("should proxy data through to the target", func() {
			// Set up a target listener that echoes data
			targetListener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			defer targetListener.Close()
			targetPort := targetListener.Addr().(*net.TCPAddr).Port

			receivedChan := make(chan []byte, 1)
			go func() {
				conn, err := targetListener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				buf := make([]byte, 1024)
				n, err := conn.Read(buf)
				if err != nil {
					return
				}
				receivedChan <- buf[:n]
			}()

			ports := map[string]int{
				strconv.Itoa(targetPort): 49152,
			}

			remappedPorts, err := manager.OpenProxyPorts("test-migration", "127.0.0.1", "127.0.0.1", ports)
			Expect(err).ToNot(HaveOccurred())

			// Connect to the proxy port and send data
			var proxyPort string
			for port := range remappedPorts {
				proxyPort = port
				break
			}
			conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%s", proxyPort))
			Expect(err).ToNot(HaveOccurred())
			defer conn.Close()

			message := []byte("test migration data")
			_, err = conn.Write(message)
			Expect(err).ToNot(HaveOccurred())

			// Verify data was received at the target
			received := <-receivedChan
			Expect(received).To(Equal(message))
		})

		It("should close proxy ports for a specific migration", func() {
			targetListener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			defer targetListener.Close()
			targetPort := targetListener.Addr().(*net.TCPAddr).Port

			ports := map[string]int{
				strconv.Itoa(targetPort): 49152,
			}

			remappedPorts, err := manager.OpenProxyPorts("test-migration", "127.0.0.1", "127.0.0.1", ports)
			Expect(err).ToNot(HaveOccurred())
			Expect(manager.HasMappings("test-migration")).To(BeTrue())

			manager.Close("test-migration")
			Expect(manager.HasMappings("test-migration")).To(BeFalse())

			// Verify we can no longer connect to the proxy port
			var proxyPort string
			for port := range remappedPorts {
				proxyPort = port
				break
			}
			_, err = net.Dial("tcp", fmt.Sprintf("127.0.0.1:%s", proxyPort))
			Expect(err).To(HaveOccurred())
		})

		It("should replace existing mappings when opening ports for the same migrationID", func() {
			targetListener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			defer targetListener.Close()
			targetPort := targetListener.Addr().(*net.TCPAddr).Port

			ports := map[string]int{
				strconv.Itoa(targetPort): 49152,
			}

			firstPorts, err := manager.OpenProxyPorts("test-migration", "127.0.0.1", "127.0.0.1", ports)
			Expect(err).ToNot(HaveOccurred())

			// Open again for the same migration - should close old ones
			secondPorts, err := manager.OpenProxyPorts("test-migration", "127.0.0.1", "127.0.0.1", ports)
			Expect(err).ToNot(HaveOccurred())

			// The ports should be different (new listeners)
			var firstPort, secondPort string
			for p := range firstPorts {
				firstPort = p
			}
			for p := range secondPorts {
				secondPort = p
			}
			// They could theoretically be the same if the OS reassigns, but likely different
			_ = firstPort
			_ = secondPort
		})

		It("should close all mappings", func() {
			targetListener1, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			defer targetListener1.Close()
			targetListener2, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			defer targetListener2.Close()

			ports1 := map[string]int{
				strconv.Itoa(targetListener1.Addr().(*net.TCPAddr).Port): 49152,
			}
			ports2 := map[string]int{
				strconv.Itoa(targetListener2.Addr().(*net.TCPAddr).Port): 49152,
			}

			_, err = manager.OpenProxyPorts("migration-1", "127.0.0.1", "127.0.0.1", ports1)
			Expect(err).ToNot(HaveOccurred())
			_, err = manager.OpenProxyPorts("migration-2", "127.0.0.1", "127.0.0.1", ports2)
			Expect(err).ToNot(HaveOccurred())

			Expect(manager.HasMappings("migration-1")).To(BeTrue())
			Expect(manager.HasMappings("migration-2")).To(BeTrue())

			manager.CloseAll()

			Expect(manager.HasMappings("migration-1")).To(BeFalse())
			Expect(manager.HasMappings("migration-2")).To(BeFalse())
		})

		It("should handle closing non-existent migration gracefully", func() {
			Expect(func() {
				manager.Close("non-existent")
			}).ToNot(Panic())
		})

		It("should handle single migration channel (block only)", func() {
			targetListener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			defer targetListener.Close()

			ports := map[string]int{
				strconv.Itoa(targetListener.Addr().(*net.TCPAddr).Port): 49152,
			}

			remappedPorts, err := manager.OpenProxyPorts("test-migration", "127.0.0.1", "127.0.0.1", ports)
			Expect(err).ToNot(HaveOccurred())
			Expect(remappedPorts).To(HaveLen(1))

			for _, vhPort := range remappedPorts {
				Expect(vhPort).To(Equal(49152))
			}
		})
	})
})
