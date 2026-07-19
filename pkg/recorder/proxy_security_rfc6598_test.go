package recorder

import (
	"net"
	"testing"
)

func TestProxyRejectsRFC6598SharedAddressSpace(t *testing.T) {
	t.Setenv(envProxyAllowPrivateTargets, "")

	tests := []struct {
		name    string
		ip      string
		blocked bool
	}{
		{name: "below shared range", ip: "100.63.255.255"},
		{name: "near shared range start", ip: "100.64.0.1", blocked: true},
		{name: "inside shared range", ip: "100.100.100.200", blocked: true},
		{name: "near shared range end", ip: "100.127.255.254", blocked: true},
		{name: "above shared range", ip: "100.128.0.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			literalErr := validateProxyTargetURL("http://" + test.ip + "/record")
			if got := literalErr != nil; got != test.blocked {
				t.Fatalf("validateProxyTargetURL(%q) error = %v, blocked=%t", test.ip, literalErr, test.blocked)
			}

			dialErr := proxyDialControl("tcp", net.JoinHostPort(test.ip, "443"), nil)
			if got := dialErr != nil; got != test.blocked {
				t.Fatalf("proxyDialControl(%q) error = %v, blocked=%t", test.ip, dialErr, test.blocked)
			}
		})
	}
}
