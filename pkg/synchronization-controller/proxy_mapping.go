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
	"io"
	"net"
	"strconv"
	"sync"

	"kubevirt.io/client-go/log"
)

// ProxyMapping stores the mapping between a local proxy port and a remote
// target address. It accepts connections on the local listener and forwards
// them to the remote target, enabling CCLM migration data to flow through
// the synchronization controller proxy layer.
type ProxyMapping struct {
	// SourcePort is the local port that was opened for proxying.
	SourcePort int
	// TargetAddr is the remote host:port to forward connections to.
	TargetAddr string
	// VirtHandlerPort is the original virt-handler port (e.g. 49152 or 49153).
	VirtHandlerPort int

	listener net.Listener
	stopChan chan struct{}
}

// ProxyMappingManager manages proxy port mappings for CCLM migrations,
// keyed by migrationID. Each migration can have multiple proxy mappings
// (typically one for block migration and one for state migration).
type ProxyMappingManager struct {
	mu       sync.Mutex
	mappings map[string][]*ProxyMapping // keyed by migrationID
}

// NewProxyMappingManager creates a new ProxyMappingManager.
func NewProxyMappingManager() *ProxyMappingManager {
	return &ProxyMappingManager{
		mappings: make(map[string][]*ProxyMapping),
	}
}

// OpenProxyPorts opens local TCP proxy ports for CCLM migration data
// multiplexing. For each entry in the port map (destPort -> srcPort),
// it opens a new TCP listener on bindAddress and sets up forwarding to
// targetAddress:destPort.
//
// Returns a new port map where keys are the newly opened local ports
// (as strings) and values are the original virt-handler ports.
func (m *ProxyMappingManager) OpenProxyPorts(migrationID string, bindAddress string, targetAddress string, ports map[string]int) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Close existing mappings for this migrationID if any
	m.closeUnlocked(migrationID)

	remappedPorts := make(map[string]int)
	var mappings []*ProxyMapping

	for destPort, srcPort := range ports {
		targetAddr := net.JoinHostPort(targetAddress, destPort)
		// Listen on port 0 to get a random available port
		listener, err := net.Listen("tcp", net.JoinHostPort(bindAddress, "0"))
		if err != nil {
			// Clean up any listeners we already opened
			for _, pm := range mappings {
				pm.Close()
			}
			return nil, fmt.Errorf("failed to open proxy port for virt-handler port %d: %v", srcPort, err)
		}

		localPort := listener.Addr().(*net.TCPAddr).Port
		pm := &ProxyMapping{
			SourcePort:      localPort,
			TargetAddr:      targetAddr,
			VirtHandlerPort: srcPort,
			listener:        listener,
			stopChan:        make(chan struct{}),
		}
		go pm.serve()

		remappedPorts[strconv.Itoa(localPort)] = srcPort
		mappings = append(mappings, pm)
		log.Log.Infof("CCLM proxy: opened port %d -> %s (virt-handler port %d) for migration %s",
			localPort, targetAddr, srcPort, migrationID)
	}

	m.mappings[migrationID] = mappings
	return remappedPorts, nil
}

// Close closes all proxy mappings for the given migrationID.
func (m *ProxyMappingManager) Close(migrationID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeUnlocked(migrationID)
}

// CloseAll closes all proxy mappings across all migrations.
func (m *ProxyMappingManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.mappings {
		m.closeUnlocked(id)
	}
}

// HasMappings returns true if proxy mappings exist for the given migrationID.
func (m *ProxyMappingManager) HasMappings(migrationID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.mappings[migrationID]
	return ok
}

func (m *ProxyMappingManager) closeUnlocked(migrationID string) {
	if mappings, ok := m.mappings[migrationID]; ok {
		for _, pm := range mappings {
			pm.Close()
		}
		delete(m.mappings, migrationID)
		log.Log.Infof("CCLM proxy: closed all proxy mappings for migration %s", migrationID)
	}
}

// Close stops the proxy listener and terminates all active connections.
func (pm *ProxyMapping) Close() {
	select {
	case <-pm.stopChan:
		// already closed
		return
	default:
		close(pm.stopChan)
	}
	if pm.listener != nil {
		pm.listener.Close()
	}
}

func (pm *ProxyMapping) serve() {
	for {
		conn, err := pm.listener.Accept()
		if err != nil {
			select {
			case <-pm.stopChan:
				return
			default:
				log.Log.Reason(err).Errorf("CCLM proxy: accept error on port %d", pm.SourcePort)
				return
			}
		}
		go pm.handleConnection(conn)
	}
}

func (pm *ProxyMapping) handleConnection(src net.Conn) {
	defer src.Close()

	dst, err := net.Dial("tcp", pm.TargetAddr)
	if err != nil {
		log.Log.Reason(err).Errorf("CCLM proxy: failed to connect to %s", pm.TargetAddr)
		return
	}
	defer dst.Close()

	outboundErr := make(chan error, 1)
	inboundErr := make(chan error, 1)

	go func() {
		_, err := io.Copy(dst, src)
		outboundErr <- err
	}()
	go func() {
		_, err := io.Copy(src, dst)
		inboundErr <- err
	}()

	select {
	case <-outboundErr:
	case <-inboundErr:
	case <-pm.stopChan:
	}
}
