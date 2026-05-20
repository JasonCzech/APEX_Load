package network

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestNetworkProtocolDNSQuery(t *testing.T) {
	netProto := &Network{}
	if err := netProto.Init(context.Background(), map[string]any{"network_timeout_ms": float64(1000)}); err != nil {
		t.Fatalf("init network: %v", err)
	}

	result, err := netProto.Execute(context.Background(), &protocol.Action{
		Name:   "lookup",
		Type:   "dns_query",
		Target: "localhost",
		Params: map[string]any{"record_type": "A"},
	})
	if err != nil {
		t.Fatalf("dns query: %v", err)
	}
	if !result.Success {
		t.Fatalf("dns query failed: %s", result.ErrorMessage)
	}
	if result.DependencyType != "network_dns" || result.DependencyTarget != "localhost" {
		t.Fatalf("dependency = %s/%s, want network_dns/localhost", result.DependencyType, result.DependencyTarget)
	}
	if !strings.Contains(result.ResponseBody, `"record_type":"A"`) {
		t.Fatalf("response body = %s, want A record payload", result.ResponseBody)
	}
}

func TestNetworkProtocolTCPRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 16)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("pong"))
	}()

	netProto := &Network{}
	if err := netProto.Init(context.Background(), map[string]any{"network_timeout_ms": float64(1000)}); err != nil {
		t.Fatalf("init network: %v", err)
	}
	result, err := netProto.Execute(context.Background(), &protocol.Action{
		Name:   "tcp",
		Type:   "tcp_request",
		Target: listener.Addr().String(),
		Body:   []byte("ping"),
		Params: map[string]any{"read": true},
	})
	if err != nil {
		t.Fatalf("tcp request: %v", err)
	}
	if !result.Success {
		t.Fatalf("tcp request failed: %s", result.ErrorMessage)
	}
	if result.DependencyType != "network_tcp" || result.BytesSent != 4 || result.BytesReceived != 4 {
		t.Fatalf("result = %#v, want tcp bytes sent/received", result)
	}
	if !strings.Contains(result.ResponseBody, `"response":"pong"`) {
		t.Fatalf("response body = %s, want pong", result.ResponseBody)
	}
}

func TestNetworkProtocolUDPRequest(t *testing.T) {
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve udp: %v", err)
	}
	server, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer server.Close()
	go func() {
		buf := make([]byte, 16)
		n, client, err := server.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if string(buf[:n]) == "ping" {
			_, _ = server.WriteToUDP([]byte("pong"), client)
		}
	}()

	netProto := &Network{}
	if err := netProto.Init(context.Background(), map[string]any{"network_timeout_ms": float64(1000)}); err != nil {
		t.Fatalf("init network: %v", err)
	}
	result, err := netProto.Execute(context.Background(), &protocol.Action{
		Name:   "udp",
		Type:   "udp_request",
		Target: server.LocalAddr().String(),
		Body:   []byte("ping"),
		Params: map[string]any{"read": true},
	})
	if err != nil {
		t.Fatalf("udp request: %v", err)
	}
	if !result.Success {
		t.Fatalf("udp request failed: %s", result.ErrorMessage)
	}
	if result.DependencyType != "network_udp" || !strings.Contains(result.ResponseBody, `"response":"pong"`) {
		t.Fatalf("result = %#v, want udp pong", result)
	}
}

func TestNetworkProtocolFTPCommand(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ftp: %v", err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("220 fake ftp ready\r\n"))
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "USER "):
				_, _ = conn.Write([]byte("331 password required\r\n"))
			case strings.HasPrefix(line, "PASS "):
				_, _ = conn.Write([]byte("230 logged in\r\n"))
			case line == "NOOP":
				_, _ = conn.Write([]byte("200 ok\r\n"))
			case line == "QUIT":
				_, _ = conn.Write([]byte("221 bye\r\n"))
				return
			default:
				_, _ = conn.Write([]byte("500 unexpected\r\n"))
			}
		}
	}()

	netProto := &Network{}
	if err := netProto.Init(context.Background(), map[string]any{"network_timeout_ms": float64(1000)}); err != nil {
		t.Fatalf("init network: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := netProto.Execute(ctx, &protocol.Action{
		Name:   "ftp",
		Type:   "ftp_noop",
		Target: listener.Addr().String(),
	})
	if err != nil {
		t.Fatalf("ftp command: %v", err)
	}
	if !result.Success {
		t.Fatalf("ftp command failed: %s", result.ErrorMessage)
	}
	if result.DependencyType != "network_ftp" || !strings.Contains(result.ResponseBody, "fake ftp ready") {
		t.Fatalf("result = %#v, want ftp diagnostics", result)
	}
}
