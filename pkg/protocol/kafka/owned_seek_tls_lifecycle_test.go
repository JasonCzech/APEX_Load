//go:build kafka_real && kafka_seek_integration

package kafka

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// These actual TLS sockets qualify proxy lifecycle/error handling only. No
// Kafka response or assignment is fabricated as owned-seek acceptance evidence.
func TestOwnedSeekProxyTLSHandshakeLifecycle(t *testing.T) {
	for _, mode := range []string{"owned-close-during-handshake", "owned-close-with-open-pipeline", "active-untrusted-peer"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			certPath, keyPath, certificate, trust := ownedSeekLifecycleCertificate(t)
			if mode == "active-untrusted-peer" {
				_, _, certificate, _ = ownedSeekLifecycleCertificate(t)
			}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal("TLS lifecycle listener unavailable")
			}
			var serverMu sync.Mutex
			var serverConn net.Conn
			serverClosing := false
			started := make(chan bool, 1)
			serverExited := make(chan struct{})
			serverCtx, stopServer := context.WithCancel(ctx)
			t.Cleanup(func() {
				stopServer()
				listener.Close()
				serverMu.Lock()
				serverClosing = true
				conn := serverConn
				serverMu.Unlock()
				if conn != nil {
					conn.Close()
				}
				joinOwnedSeekOperation(t, serverExited, "TLS lifecycle server")
			})
			go func() {
				defer close(serverExited)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				serverMu.Lock()
				if serverClosing {
					serverMu.Unlock()
					conn.Close()
					return
				}
				serverConn = conn
				serverMu.Unlock()
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(4 * time.Second))
				if mode != "owned-close-during-handshake" {
					err := tls.Server(conn, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}).HandshakeContext(serverCtx)
					if mode == "active-untrusted-peer" {
						return
					}
					started <- err == nil
					<-serverCtx.Done()
					return
				}
				var header [5]byte
				_, err = io.ReadFull(conn, header[:])
				started <- err == nil && header[0] == 22 && header[1] == 3
				<-serverCtx.Done()
			}()
			proxy := newOwnedSeekProxy(t, ctx, ownedSeekFixture{DialAddress: "127.0.0.1:0", BrokerAddress: listener.Addr().String(), ProxyCertFile: certPath, ProxyKeyFile: keyPath}, trust)
			conn, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: time.Second}, Config: trust.Clone()}).DialContext(ctx, "tcp4", proxy.listener.Addr().String())
			if err != nil {
				t.Fatal("real TLS lifecycle client handshake failed")
			}
			t.Cleanup(func() { conn.Close() })
			if mode != "active-untrusted-peer" {
				select {
				case valid := <-started:
					if !valid {
						t.Fatal("upstream did not reach its genuine TLS lifecycle boundary")
					}
				case <-ctx.Done():
					t.Fatal("TLS lifecycle upstream handshake was not observed")
				}
				proxy.Close(t)
				proxy.mu.Lock()
				failures := append([]string{}, proxy.failures...)
				proxy.mu.Unlock()
				if len(failures) != 0 {
					t.Fatal("owned proxy close became an upstream trust failure")
				}
				return
			}
			waitOwnedSeek(t, ctx, 3*time.Second, "active upstream certificate refusal", func() bool {
				proxy.mu.Lock()
				defer proxy.mu.Unlock()
				return len(proxy.failures) != 0
			})
			proxy.mu.Lock()
			failures := append([]string{}, proxy.failures...)
			if reflect.DeepEqual(failures, []string{"execution proxy upstream TLS failed"}) {
				// Only this offline negative control consumes its expected receipt;
				// live qualification never removes a recorded proxy failure.
				proxy.failures = nil
			}
			proxy.mu.Unlock()
			if !reflect.DeepEqual(failures, []string{"execution proxy upstream TLS failed"}) {
				t.Fatal("active TLS trust failure lost its exact receipt")
			}
			t.Log("active real TLS trust failure retained: execution proxy upstream TLS failed")
			proxy.Close(t)
		})
	}
}

func ownedSeekLifecycleCertificate(t *testing.T) (certPath, keyPath string, certificate tls.Certificate, trust *tls.Config) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("TLS lifecycle key generation failed")
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal("TLS lifecycle certificate generation failed")
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal("TLS lifecycle private key encoding failed")
	}
	certBody := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBody := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	defer clear(keyBody)
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "server.pem"), filepath.Join(dir, "server-key.pem")
	if os.WriteFile(certPath, certBody, 0600) != nil || os.WriteFile(keyPath, keyBody, 0600) != nil {
		t.Fatal("TLS lifecycle protected files unavailable")
	}
	certificate, err = tls.X509KeyPair(certBody, keyBody)
	if err != nil {
		t.Fatal("TLS lifecycle key pair invalid")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certBody) {
		t.Fatal("TLS lifecycle trust invalid")
	}
	return certPath, keyPath, certificate, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "localhost", RootCAs: roots}
}
