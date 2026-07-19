package netpolicy

import (
	"net"
	"net/netip"
	"testing"
)

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		name   string
		ip     string
		public bool
	}{
		{name: "invalid"},
		{name: "public IPv4", ip: "8.8.8.8", public: true},
		{name: "below this-network range", ip: "0.0.0.1"},
		{name: "end of this-network range", ip: "0.255.255.255"},
		{name: "above this-network range", ip: "1.0.0.0", public: true},
		{name: "below shared range", ip: "100.63.255.255", public: true},
		{name: "start of shared range", ip: "100.64.0.0"},
		{name: "end of shared range", ip: "100.127.255.255"},
		{name: "above shared range", ip: "100.128.0.0", public: true},
		{name: "IETF protocol assignment", ip: "192.0.0.9"},
		{name: "globally reachable AS112", ip: "192.31.196.1", public: true},
		{name: "below benchmark range", ip: "198.17.255.255", public: true},
		{name: "start of benchmark range", ip: "198.18.0.0"},
		{name: "end of benchmark range", ip: "198.19.255.255"},
		{name: "above benchmark range", ip: "198.20.0.0", public: true},
		{name: "documentation IPv4", ip: "203.0.113.10"},
		{name: "multicast IPv4", ip: "239.255.255.255"},
		{name: "reserved IPv4", ip: "240.0.0.1"},
		{name: "limited broadcast", ip: "255.255.255.255"},
		{name: "mapped public IPv4", ip: "::ffff:8.8.8.8", public: true},
		{name: "mapped private IPv4", ip: "::ffff:10.0.0.1"},
		{name: "mapped shared IPv4", ip: "::ffff:100.100.100.200"},
		{name: "IPv6 loopback", ip: "::1"},
		{name: "IPv6 unique local", ip: "fd00::1"},
		{name: "IPv6 link local", ip: "fe80::1"},
		{name: "IPv6 NAT64", ip: "64:ff9b::0808:0808"},
		{name: "IPv6 below global aggregate", ip: "1fff:ffff::1"},
		{name: "public IPv6", ip: "2606:4700:4700::1111", public: true},
		{name: "IPv6 IETF assignments", ip: "2001:1::1"},
		{name: "IPv6 above IETF assignments", ip: "2001:200::1", public: true},
		{name: "IPv6 documentation", ip: "2001:db8::1"},
		{name: "IPv6 6to4", ip: "2002:0808:0808::1"},
		{name: "returned 6bone", ip: "3ffe::1"},
		{name: "new IPv6 documentation", ip: "3fff::1"},
		{name: "IPv6 above global aggregate", ip: "4000::1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ip := net.ParseIP(test.ip)
			if got := IsPublicIP(ip); got != test.public {
				t.Fatalf("IsPublicIP(%q) = %t, want %t", test.ip, got, test.public)
			}
		})
	}
}

func TestIsPublicAddrRejectsInvalidAddress(t *testing.T) {
	if IsPublicAddr(netip.Addr{}) {
		t.Fatal("invalid netip address was classified as public")
	}
}
