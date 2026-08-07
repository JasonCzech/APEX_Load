package api

import (
	"fmt"
	"net/netip"
)

// Egress policy for administrator-registered Kubernetes API servers.
//
// The general outbound guard (outbound_guard.go) is deliberately NOT reused
// here. Its policy is "block anything that is not a public IP", which is
// correct for federated library remotes and webhooks but wrong for a control
// plane: private AKS API servers, on-prem clusters, and the in-cluster
// kubernetes.default service IP are all RFC1918, i.e. the normal case. Opting
// into APEXLOAD_LIBRARY_REMOTE_ALLOW_PRIVATE_HOSTS to reach them would
// simultaneously loosen library federation, so this is a separate, narrower
// policy: private ranges are allowed, but the addresses that are never a
// legitimate API server — loopback, link-local (cloud instance metadata),
// unspecified, and multicast — are always denied.
//
// There is deliberately no insecure-skip-TLS escape hatch anywhere in this
// path. Every managed distribution hands out a CA certificate, and a stored
// control-plane credential paired with unverified TLS is a permanent
// man-in-the-middle foothold on a system-administrator surface.

// kubeClusterDialGuard returns the per-IP policy for cluster API traffic.
// allowLoopback is true outside production so httptest servers work.
func kubeClusterDialGuard(allowLoopback bool) func(netip.Addr) error {
	return func(addr netip.Addr) error {
		return checkKubeClusterAddr(addr, allowLoopback)
	}
}

func checkKubeClusterAddr(addr netip.Addr, allowLoopback bool) error {
	// Unmap first so an IPv4-mapped IPv6 form (::ffff:169.254.169.254) cannot
	// slip past the IPv4 predicates below.
	addr = addr.Unmap()
	if !addr.IsValid() {
		return fmt.Errorf("blocked cluster connection: invalid address")
	}
	switch {
	case addr.IsUnspecified():
		return fmt.Errorf("blocked cluster connection to %s: unspecified address", addr)
	case addr.IsMulticast() || addr.IsInterfaceLocalMulticast() || addr.IsLinkLocalMulticast():
		return fmt.Errorf("blocked cluster connection to %s: multicast address", addr)
	case addr.IsLinkLocalUnicast():
		// 169.254.0.0/16 and fe80::/10 — this is what keeps a registered
		// cluster endpoint from being pointed at cloud instance metadata.
		return fmt.Errorf("blocked cluster connection to %s: link-local address", addr)
	case addr.IsLoopback():
		if allowLoopback {
			return nil
		}
		return fmt.Errorf("blocked cluster connection to %s: loopback address", addr)
	}
	return nil
}

// clusterDialGuard is the guard for this server's configuration. Loopback is
// permitted outside production so tests and local development can point at a
// stub API server.
func (s *Server) clusterDialGuard() func(netip.Addr) error {
	return kubeClusterDialGuard(!productionMode())
}
