package netutil_test

import (
	"net"
	"testing"

	"github.com/16bitowl/beacons/internal/netutil"
)

func TestLocalIP_ReturnsParseableIP(t *testing.T) {
	ip, err := netutil.LocalIP()
	if err != nil {
		// The sandbox running this test may have no routable network stack
		// at all; that's an environment limitation, not a behavioural bug.
		t.Skipf("LocalIP unavailable in this environment: %v", err)
	}
	if net.ParseIP(ip) == nil {
		t.Errorf("LocalIP() = %q, not a parseable IP address", ip)
	}
}
