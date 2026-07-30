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
	"time"

	"kubevirt.io/client-go/log"
)

const (
	// maxConnectionsPerProxy limits the number of concurrent connections
	// per proxy mapping to prevent resource exhaustion.
	maxConnectionsPerProxy = 64
	// proxyIdleTimeout is the maximum duration a proxied connection can
	// remain idle before being terminated.
	proxyIdleTimeout = 10 * time.Minute
	// maxAcceptRetries is the number of transient Accept() errors to
	// tolerate before stopping the proxy listener.
	maxAcceptRetries = 3
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
	// connSem limits concurrent connections per proxy mapping.
	connSem chan struct{}
	// activeConns tracks open connections for explicit cleanup during Close.
	activeConns   []net.Conn
	activeConnsMu sync.Mutex
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

// validateTargetAddress checks that the target address is not a prohibited
// destination (cloud metadata endpoints, link-local addresses).
func validateTargetAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// address might not have a port; try parsing as plain host
		host = address
	}
	if host == "" {
		return fmt.Errorf("empty target address")
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// Could be a hostname — resolve it to check the IP
		addrs, err := net.LookupHost(host)
		if err != nil {
			return fmt.Errorf("cannot resolve target host %q: %v", host, err)
		}
		for _, addr := range addrs {
			if err := validateIP(net.ParseIP(addr)); err != nil {
				return fmt.Errorf("resolved address %s for host %q is prohibited: %v", addr, host, err)
			}
		}
		return nil
	}
	return validateIP(ip)
}

// validateIP checks an IP against prohibited ranges.
func validateIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("nil IP address")
	}
	// Block link-local IPv4 (169.254.0.0/16) — includes cloud metadata 169.254.169.254
	linkLocal4 := net.IPNet{IP: net.ParseIP("169.254.0.0"), Mask: net.CIDRMask(16, 32)}
	if linkLocal4.Contains(ip) {
		return fmt.Errorf("link-local IPv4 address %s is not allowed", ip)
	}
	// Block link-local IPv6 (fe80::/10)
	linkLocal6 := net.IPNet{IP: net.ParseIP("fe80::"), Mask: net.CIDRMask(10, 128)}
	if linkLocal6.Contains(ip) {
		return fmt.Errorf("link-local IPv6 address %s is not allowed", ip)
	}
	return nil
}

// OpenProxyPorts opens local TCP proxy ports for CCLM migration data
// multiplexing. For each entry in the port map (destPort -> srcPort),
// it opens a new TCP listener on bindAddress and sets up forwarding to
// targetAddress:destPort.
//
// The targetAddress is validated against prohibited ranges (link-local,
// cloud metadata endpoints) to prevent SSRF attacks.
//
// Returns a new port map where keys are the newly opened local ports
// (as strings) and values are the original virt-handler ports.
func (m *ProxyMappingManager) OpenProxyPorts(migrationID string, bindAddress string, targetAddress string, ports map[string]int) (map[string]int, error) {
	// Validate target address before acquiring lock
	if err := validateTargetAddress(targetAddress); err != nil {
		return nil, fmt.Errorf("CCLM proxy: invalid target address %q: %v", targetAddress, err)
	}

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
			connSem:         make(chan struct{}, maxConnectionsPerProxy),
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
	// Explicitly close all tracked active connections so io.Copy goroutines
	// unblock immediately rather than waiting for the remote side to close.
	pm.activeConnsMu.Lock()
	for _, conn := range pm.activeConns {
		conn.Close()
	}
	pm.activeConns = nil
	pm.activeConnsMu.Unlock()
}

func (pm *ProxyMapping) trackConn(conn net.Conn) {
	pm.activeConnsMu.Lock()
	pm.activeConns = append(pm.activeConns, conn)
	pm.activeConnsMu.Unlock()
}

func (pm *ProxyMapping) untrackConn(conn net.Conn) {
	pm.activeConnsMu.Lock()
	for i, c := range pm.activeConns {
		if c == conn {
			pm.activeConns = append(pm.activeConns[:i], pm.activeConns[i+1:]...)
			break
		}
	}
	pm.activeConnsMu.Unlock()
}

func (pm *ProxyMapping) serve() {
	consecutiveErrors := 0
	for {
		conn, err := pm.listener.Accept()
		if err != nil {
			select {
			case <-pm.stopChan:
				return
			default:
				consecutiveErrors++
				if consecutiveErrors >= maxAcceptRetries {
					log.Log.Reason(err).Errorf("CCLM proxy: %d consecutive accept errors on port %d, stopping listener",
						consecutiveErrors, pm.SourcePort)
					return
				}
				log.Log.Reason(err).Warningf("CCLM proxy: transient accept error on port %d (%d/%d)",
					pm.SourcePort, consecutiveErrors, maxAcceptRetries)
				continue
			}
		}
		consecutiveErrors = 0

		// Enforce max concurrent connections
		select {
		case pm.connSem <- struct{}{}:
			go func() {
				defer func() { <-pm.connSem }()
				pm.handleConnection(conn)
			}()
		default:
			log.Log.Warningf("CCLM proxy: max connections (%d) reached on port %d, rejecting",
				maxConnectionsPerProxy, pm.SourcePort)
			conn.Close()
		}
	}
}

func (pm *ProxyMapping) handleConnection(src net.Conn) {
	pm.trackConn(src)
	defer func() {
		pm.untrackConn(src)
		src.Close()
	}()

	dst, err := net.Dial("tcp", pm.TargetAddr)
	if err != nil {
		log.Log.Reason(err).Errorf("CCLM proxy: failed to connect to %s", pm.TargetAddr)
		return
	}
	pm.trackConn(dst)
	defer func() {
		pm.untrackConn(dst)
		dst.Close()
	}()

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
