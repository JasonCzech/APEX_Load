// Package grpcclient opens gRPC client channels the way the deprecated
// grpc.DialContext did, on top of grpc.NewClient.
package grpcclient

import (
	"net/url"

	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"
)

// New opens a channel with grpc.DialContext's non-blocking behavior. Three
// differences between grpc.NewClient and grpc.DialContext would otherwise
// change what a target means and when it connects:
//
//   - A target without a registered scheme ("host:port", "bufnet") goes to the
//     dialer as written (passthrough) instead of through DNS.
//   - Hostnames are resolved locally even when a proxy is configured.
//   - The channel starts connecting now instead of at its first RPC.
func New(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	opts = append([]grpc.DialOption{grpc.WithLocalDNSResolution()}, opts...)
	conn, err := grpc.NewClient(passthroughTarget(target), opts...)
	if err != nil {
		return nil, err
	}
	conn.Connect()
	return conn, nil
}

// passthroughTarget prefixes passthrough:/// where gRPC falls back to its
// default scheme: when the target does not parse as a URL or names a scheme
// with no registered resolver.
func passthroughTarget(target string) string {
	if parsed, err := url.Parse(target); err == nil && resolver.Get(parsed.Scheme) != nil {
		return target
	}
	return "passthrough:///" + target
}
