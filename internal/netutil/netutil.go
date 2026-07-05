// Package netutil provides small, dependency-free host-networking helpers
// used by source adapters to resolve dynamic values at parse time.
package netutil

import (
	"fmt"
	"net"
)

// probeAddr is never actually dialed: UDP "Dial" only performs a local
// routing-table lookup to pick an outbound interface/address — no packets
// are sent. 203.0.113.0/24 is reserved for documentation (RFC 5737), so the
// address is guaranteed to be non-routable.
const probeAddr = "203.0.113.1:80"

// LocalIP returns the local IP address this host would use for outbound
// traffic, determined via a UDP route lookup (no packets are sent). This is
// the address of whatever network namespace the calling process runs in —
// for a containerized deployment of Beacons, that means the container's own
// namespace unless it runs with host networking.
func LocalIP() (string, error) {
	conn, err := net.Dial("udp", probeAddr)
	if err != nil {
		return "", fmt.Errorf("netutil: resolving local ip: %w", err)
	}
	defer conn.Close()

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", fmt.Errorf("netutil: unexpected local address type %T", conn.LocalAddr())
	}
	return addr.IP.String(), nil
}
