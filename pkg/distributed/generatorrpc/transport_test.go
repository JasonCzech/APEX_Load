package generatorrpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testCertFiles generates a CA plus a leaf certificate signed by it (valid for
// 127.0.0.1 and the given DNS name) and writes PEM files into dir.
func testCertFiles(t *testing.T, dir string, dnsName string) (certFile, keyFile, caFile string) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "apexload-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{dnsName},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}

	certFile = filepath.Join(dir, "leaf.crt")
	keyFile = filepath.Join(dir, "leaf.key")
	caFile = filepath.Join(dir, "ca.crt")
	writePEM(t, certFile, "CERTIFICATE", leafDER)
	writePEM(t, keyFile, "EC PRIVATE KEY", leafKeyDER)
	writePEM(t, caFile, "CERTIFICATE", caDER)
	return certFile, keyFile, caFile
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func clearTransportEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{grpcTLSCertEnvVar, grpcTLSKeyEnvVar, grpcTLSCAEnvVar, grpcTLSServerNameEnvVar, grpcAuthTokenEnvVar} {
		t.Setenv(key, "")
	}
}

// startTestServer builds server options from the current env and serves the
// generator service on a loopback listener.
func startTestServer(t *testing.T) string {
	t.Helper()
	serverOptions, err := ServerOptionsFromEnv()
	if err != nil {
		t.Fatalf("ServerOptionsFromEnv: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer(serverOptions...)
	pb.RegisterGeneratorControlServer(server, NewService("transport-test", 10))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

func dialAndDistribute(t *testing.T, target string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Dial(ctx, target)
	if err != nil {
		return err
	}
	defer client.Close()
	_, err = client.DistributeScript(ctx, Script{ID: "s1", Content: []byte(`{}`), ContentHash: "h"})
	return err
}

func TestTransport_PlaintextDefault(t *testing.T) {
	clearTransportEnv(t)
	target := startTestServer(t)
	if err := dialAndDistribute(t, target); err != nil {
		t.Fatalf("plaintext round-trip failed: %v", err)
	}
	security := TransportSecurityFromEnv()
	if !security.Insecure() || security.String() != "plaintext+no-token" {
		t.Fatalf("expected insecure posture, got %s", security.String())
	}
}

func TestTransportAcceptsScriptAtConfiguredContentLimit(t *testing.T) {
	clearTransportEnv(t)
	const scriptLimit = 1024
	t.Setenv(envGeneratorScriptMaxBytes, "1024")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	target := startTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Dial(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	result, err := client.DistributeScript(ctx, Script{
		ID:          "exact-limit",
		Content:     make([]byte, scriptLimit),
		ContentHash: "hash",
	})
	if err != nil {
		t.Fatalf("exact-limit script rejected by transport: %v", err)
	}
	if result.ContentHash != "hash" {
		t.Fatalf("exact-limit script hash = %q, want service acknowledgement", result.ContentHash)
	}
	if got := generatorRPCMaxReceiveBytes(); got <= scriptLimit {
		t.Fatalf("transport receive limit = %d, want protobuf envelope headroom above %d", got, scriptLimit)
	}

	assignment, err := client.AssignWork(ctx, Assignment{
		TestID:        "exact-limit-assignment",
		GroupName:     "group",
		ScriptID:      "inline",
		ScriptContent: make([]byte, scriptLimit),
		VUserCount:    1,
		DataFiles: []DataFile{{
			Name:    "input.bin",
			Content: make([]byte, 1<<20),
		}},
	})
	if err != nil {
		t.Fatalf("application-valid assignment rejected by transport: %v", err)
	}
	if !assignment.Accepted {
		t.Fatalf("application-valid assignment rejected by service: %s", assignment.ErrorMessage)
	}
}

func TestAssignWorkRejectsAncillaryBytesBeyondTransportBudget(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	service := NewService("ancillary-limit", 10, WithGeneratorScriptCacheByteLimit(1024))
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:     "oversized-assignment",
		GroupName:  "group",
		ScriptId:   "script",
		VuserCount: 1,
		DataFiles: []*pb.DataFile{{
			Name:    "input.bin",
			Content: make([]byte, (1<<20)+2048),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ack.GetAccepted() || !strings.Contains(ack.GetErrorMessage(), "data and metadata") {
		t.Fatalf("oversized assignment acknowledgement = %#v", ack)
	}
}

func TestTransport_TokenAuth(t *testing.T) {
	clearTransportEnv(t)
	t.Setenv(grpcAuthTokenEnvVar, "correct-token")
	target := startTestServer(t)

	// Same env on the client side: accepted (unary).
	if err := dialAndDistribute(t, target); err != nil {
		t.Fatalf("token round-trip failed: %v", err)
	}

	// Wrong token: rejected with Unauthenticated.
	t.Setenv(grpcAuthTokenEnvVar, "wrong-token")
	err := dialAndDistribute(t, target)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated with wrong token, got %v", err)
	}

	// Missing token: rejected too.
	t.Setenv(grpcAuthTokenEnvVar, "")
	err = dialAndDistribute(t, target)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated with missing token, got %v", err)
	}
}

func TestTransport_TokenAuthGuardsStreams(t *testing.T) {
	clearTransportEnv(t)
	t.Setenv(grpcAuthTokenEnvVar, "stream-token")
	target := startTestServer(t)

	t.Setenv(grpcAuthTokenEnvVar, "wrong")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Dial(ctx, target)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	stream, err := client.OpenMetricsStream(ctx)
	if err == nil {
		_, err = stream.Receive()
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated on stream, got %v", err)
	}
}

func TestTransport_TLSServerAuth(t *testing.T) {
	clearTransportEnv(t)
	dir := t.TempDir()
	certFile, keyFile, caFile := testCertFiles(t, dir, "apexload-generator.apexload.svc")

	// Server: cert only (no client-cert requirement).
	t.Setenv(grpcTLSCertEnvVar, certFile)
	t.Setenv(grpcTLSKeyEnvVar, keyFile)
	target := startTestServer(t)

	// Client: trusts the CA, verifies via the DNS SAN even though the target
	// is an IP (the dns:// discovery scenario).
	t.Setenv(grpcTLSCertEnvVar, "")
	t.Setenv(grpcTLSKeyEnvVar, "")
	t.Setenv(grpcTLSCAEnvVar, caFile)
	t.Setenv(grpcTLSServerNameEnvVar, "apexload-generator.apexload.svc")
	if err := dialAndDistribute(t, target); err != nil {
		t.Fatalf("TLS round-trip failed: %v", err)
	}

	security := TransportSecurityFromEnv()
	if !security.TLS || security.MutualTLS {
		t.Fatalf("expected TLS-only posture, got %s", security.String())
	}
}

func TestTransport_MutualTLSRequiresClientCert(t *testing.T) {
	clearTransportEnv(t)
	dir := t.TempDir()
	certFile, keyFile, caFile := testCertFiles(t, dir, "apexload-generator.apexload.svc")

	// Server requires verified client certs.
	t.Setenv(grpcTLSCertEnvVar, certFile)
	t.Setenv(grpcTLSKeyEnvVar, keyFile)
	t.Setenv(grpcTLSCAEnvVar, caFile)
	target := startTestServer(t)

	// Client without a certificate is rejected during/after the handshake.
	t.Setenv(grpcTLSCertEnvVar, "")
	t.Setenv(grpcTLSKeyEnvVar, "")
	t.Setenv(grpcTLSServerNameEnvVar, "apexload-generator.apexload.svc")
	if err := dialAndDistribute(t, target); err == nil {
		t.Fatal("expected mTLS handshake failure without client cert")
	}

	// Client presenting a CA-signed cert succeeds.
	t.Setenv(grpcTLSCertEnvVar, certFile)
	t.Setenv(grpcTLSKeyEnvVar, keyFile)
	if err := dialAndDistribute(t, target); err != nil {
		t.Fatalf("mTLS round-trip failed: %v", err)
	}
	if security := TransportSecurityFromEnv(); !security.MutualTLS {
		t.Fatalf("expected mTLS posture, got %s", security.String())
	}
}

func TestTransport_MustRefuseInsecure(t *testing.T) {
	insecure := TransportSecurity{}             // plaintext + no token
	tokenOnly := TransportSecurity{Token: true} // plaintext + token
	tlsOnly := TransportSecurity{TLS: true}     // TLS + no token
	mtls := TransportSecurity{TLS: true, MutualTLS: true, Token: true}

	tests := []struct {
		name     string
		security TransportSecurity
		env      string
		want     bool
	}{
		// Fail-closed: insecure + production must refuse.
		{"insecure+production refuses", insecure, "production", true},
		{"insecure+production with surrounding whitespace refuses", insecure, "  ProDuction  ", true},
		{"insecure+prod alias refuses", insecure, "prod", true},
		// Permissive everywhere outside production.
		{"insecure+empty env allowed", insecure, "", false},
		{"insecure+local env allowed", insecure, "local", false},
		{"insecure+dev env allowed", insecure, "dev", false},
		{"insecure+kind env allowed", insecure, "kind", false},
		// Production requires encryption plus client authentication.
		{"token-only+production refuses", tokenOnly, "production", true},
		{"tls-only+production refuses", tlsOnly, "production", true},
		{"mtls+production allowed", mtls, "production", false},
		{"tls-token+production allowed", TransportSecurity{TLS: true, Token: true}, "production", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.security.MustRefuseInsecure(tc.env); got != tc.want {
				t.Fatalf("MustRefuseInsecure(%q) for %s = %v, want %v", tc.env, tc.security.String(), got, tc.want)
			}
		})
	}
}

func TestTransportProductionConfigurationFailsClosed(t *testing.T) {
	clearTransportEnv(t)
	t.Setenv("APEXLOAD_ENV", " PROD ")
	t.Setenv(grpcAuthTokenEnvVar, "plaintext-token")
	if _, err := ServerOptionsFromEnv(); err == nil {
		t.Fatal("production server accepted a plaintext bearer token")
	}
	if _, err := DialOptionsFromEnv(); err == nil {
		t.Fatal("production client accepted a plaintext bearer token")
	}
}

func TestTransport_ConfigValidation(t *testing.T) {
	clearTransportEnv(t)
	t.Setenv(grpcTLSCertEnvVar, "/nonexistent/cert.pem")
	if _, err := ServerOptionsFromEnv(); err == nil {
		t.Fatal("expected error for cert without key")
	}
	if _, err := DialOptionsFromEnv(); err == nil {
		t.Fatal("expected dial error for cert without key")
	}

	clearTransportEnv(t)
	t.Setenv(grpcTLSCAEnvVar, "/nonexistent/ca.pem")
	if _, err := ServerOptionsFromEnv(); err == nil {
		t.Fatal("expected error for server CA without server cert")
	}
}
