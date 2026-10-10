package h3fixture

// Socket-ownership spike for decision D3 (docs/HTTP3_IMPLEMENTATION.md).
// It is a measurement, not a regression test, so it only runs when
// APEXLOAD_H3_SPIKE=1:
//
//	APEXLOAD_H3_SPIKE=1 go test -run TestSpikeSocketOwnership -v ./pkg/internal/h3fixture/
//
// The fixture server runs in a child process (this test binary re-executed
// with APEXLOAD_H3_SPIKE_SERVE=1) so the parent measures client cost only.

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type ownershipSample struct {
	goroutines int
	heapInuse  uint64
	fds        int
}

func openFDs() int {
	n := 0
	for fd := 0; fd < 65536; fd++ {
		if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0); errno == 0 {
			n++
		}
	}
	return n
}

func sampleProcess() ownershipSample {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return ownershipSample{goroutines: runtime.NumGoroutine(), heapInuse: m.HeapInuse, fds: openFDs()}
}

// TestSpikeServe is the child-process side: it serves the fixture, prints
// its URL and CA, and exits when stdin closes or after two minutes.
func TestSpikeServe(t *testing.T) {
	if os.Getenv("APEXLOAD_H3_SPIKE_SERVE") != "1" {
		t.Skip("child-process helper for TestSpikeSocketOwnership")
	}
	s := Start(t, Options{})
	fmt.Printf("SPIKE-URL %s\nSPIKE-CA %s\n", s.URL("/"), base64.StdEncoding.EncodeToString(s.CAPEMOrPanic()))
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
	}
}

func startSpikeServer(t *testing.T) (string, *x509.CertPool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSpikeServe$", "-test.v")
	cmd.Env = append(os.Environ(), "APEXLOAD_H3_SPIKE_SERVE=1", "APEXLOAD_H3_SPIKE=0")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
		cancel()
	})
	var url string
	pool := x509.NewCertPool()
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if v, ok := strings.CutPrefix(line, "SPIKE-URL "); ok {
			url = v
		}
		if v, ok := strings.CutPrefix(line, "SPIKE-CA "); ok {
			pemBytes, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				t.Fatal(err)
			}
			pool.AppendCertsFromPEM(pemBytes)
			break
		}
	}
	go func() { _, _ = io.Copy(io.Discard, stdout) }()
	if url == "" {
		t.Fatal("spike server did not report its URL")
	}
	return url, pool
}

func TestSpikeSocketOwnership(t *testing.T) {
	if os.Getenv("APEXLOAD_H3_SPIKE") != "1" {
		t.Skip("set APEXLOAD_H3_SPIKE=1 to run the socket-ownership measurement")
	}
	base, pool := startSpikeServer(t)
	tlsConf := &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}
	for _, vusers := range []int{100, 1000} {
		for _, model := range []string{"per-vuser-socket", "shared-socket"} {
			t.Run(model+"/"+strconv.Itoa(vusers), func(t *testing.T) {
				before := sampleProcess()
				started := time.Now()
				transports := make([]*http3.Transport, vusers)
				var shared *quic.Transport
				if model == "shared-socket" {
					udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
					if err != nil {
						t.Fatal(err)
					}
					shared = &quic.Transport{Conn: udp}
				}
				for i := range transports {
					tr := &http3.Transport{TLSClientConfig: tlsConf.Clone()}
					if shared != nil {
						tr.Dial = func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
							udpAddr, err := net.ResolveUDPAddr("udp", addr)
							if err != nil {
								return nil, err
							}
							return shared.DialEarly(ctx, udpAddr, tlsCfg, cfg)
						}
					}
					transports[i] = tr
					resp, err := tr.RoundTrip(mustRequest(t, http.MethodGet, base+"?vu="+strconv.Itoa(i), nil))
					if err != nil {
						t.Fatal(err)
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
				elapsed := time.Since(started)
				after := sampleProcess()
				t.Logf("model=%s vusers=%d goroutines=+%d (%.2f/VUser) heap_inuse=+%.0f KiB (%.1f KiB/VUser) fds=+%d connect_all=%s (%.0f µs/VUser)",
					model, vusers,
					after.goroutines-before.goroutines, float64(after.goroutines-before.goroutines)/float64(vusers),
					float64(after.heapInuse-before.heapInuse)/1024, float64(after.heapInuse-before.heapInuse)/1024/float64(vusers),
					after.fds-before.fds, elapsed.Round(time.Millisecond), float64(elapsed.Microseconds())/float64(vusers))
				for _, tr := range transports {
					_ = tr.Close()
				}
				if shared != nil {
					_ = shared.Close()
					_ = shared.Conn.Close()
				}
				time.Sleep(200 * time.Millisecond)
			})
		}
	}
}

// CAPEMOrPanic exposes the CA PEM for the spike's child process.
func (s *Server) CAPEMOrPanic() []byte {
	if len(s.Certs.CAPEM) == 0 {
		panic("fixture CA missing")
	}
	return s.Certs.CAPEM
}
