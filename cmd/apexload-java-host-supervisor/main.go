// The experimental supervisor is PID1 of one restricted, disposable Linux pod.
// It is not a local runner or a substitute for broker termination attestation.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

type imageProfile struct {
	Contract             string                          `json:"contract"`
	BrokerClientURI      string                          `json:"broker_client_uri"`
	LeaseTTLMS           int64                           `json:"lease_ttl_ms"`
	PreparationTimeoutMS int64                           `json:"preparation_timeout_ms"`
	InvocationTimeoutMS  int64                           `json:"invocation_timeout_ms"`
	Runtime              javasidecar.DedicatedPodProfile `json:"runtime"`
}

var errConfiguration = errors.New("java_host_configuration_invalid")

func readBounded(path string, limit int64) ([]byte, error) {
	st, e := os.Stat(path)
	if e != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errConfiguration
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, errConfiguration
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(st, opened) {
		return nil, errConfiguration
	}
	data, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(data)) > limit {
		return nil, errConfiguration
	}
	return data, nil
}
func loadProfile(path, digest string) (imageProfile, error) {
	var p imageProfile
	data, e := readBounded(path, 512<<10)
	if e != nil {
		return p, e
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return p, errConfiguration
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&p) != nil || dec.Decode(new(any)) != io.EOF || p.Contract != broker.SupervisorContract || p.LeaseTTLMS < 1000 || p.LeaseTTLMS > 60000 || p.PreparationTimeoutMS < 1000 || p.PreparationTimeoutMS > 3600000 || p.InvocationTimeoutMS < 1000 || p.InvocationTimeoutMS > 3600000 {
		return imageProfile{}, errConfiguration
	}
	return p, nil
}
func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "java_host_supervisor_stopped")
		os.Exit(1)
	}
}
func run(args []string) error {
	// Refuse before reading credentials or starting any JVM.
	if e := javarunner.RequirePID1Isolation(); e != nil {
		return e
	}
	flags := flag.NewFlagSet("java-host-supervisor", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	host := flags.String("host", "", "")
	epoch := flags.String("broker-epoch", "", "")
	digest := flags.String("configuration-sha256", "", "")
	tlsDir := flags.String("tls-dir", "/transport", "")
	workDir := flags.String("work-dir", "/scratch", "")
	profilePath := flags.String("profile", "/assets/java-host.json", "")
	hostContext := flags.String("context", "", "")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errConfiguration
	}
	profile, e := loadProfile(*profilePath, *digest)
	if e != nil {
		return e
	}
	if *hostContext != "" {
		decoded, err := javarunner.DecodeDedicatedHostContext(*hostContext)
		if err != nil {
			return errConfiguration
		}
		profile.Runtime, err = javasidecar.BindDedicatedPodContext(profile.Runtime, decoded)
		if err != nil {
			return errConfiguration
		}
	}
	if !filepath.IsAbs(*tlsDir) || !filepath.IsAbs(*workDir) {
		return errConfiguration
	}
	certificate, e := readBounded(filepath.Join(*tlsDir, "tls.crt"), 128<<10)
	if e != nil {
		return e
	}
	key, e := readBounded(filepath.Join(*tlsDir, "tls.key"), 128<<10)
	if e != nil {
		return e
	}
	pair, e := tls.X509KeyPair(certificate, key)
	if e != nil {
		return errConfiguration
	}
	ca, e := readBounded(filepath.Join(*tlsDir, "ca.crt"), 128<<10)
	if e != nil {
		return e
	}
	tlsConfig, e := broker.SupervisorTLS(pair, ca, profile.BrokerClientURI)
	if e != nil {
		return e
	}
	if e = os.Setenv("TMPDIR", *workDir); e != nil {
		return e
	}
	runtime, e := javasidecar.NewDedicatedPodRuntime(profile.Runtime, *workDir)
	if e != nil {
		return e
	}
	defer runtime.Close()
	parent, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	cfg := broker.SupervisorConfig{HostID: *host, BrokerEpoch: *epoch, ConfigurationSHA256: *digest, BrokerClientURI: profile.BrokerClientURI, LeaseTTL: time.Duration(profile.LeaseTTLMS) * time.Millisecond, PreparationTimeout: time.Duration(profile.PreparationTimeoutMS) * time.Millisecond, InvocationTimeout: time.Duration(profile.InvocationTimeoutMS) * time.Millisecond}
	control, e := broker.NewSupervisorControl(parent, cfg, runtime)
	if e != nil {
		return e
	}
	defer control.Close()
	listener, e := net.Listen("tcp", ":9443")
	if e != nil {
		return e
	}
	defer listener.Close()
	server := newHTTPServer(control, cfg.InvocationTimeout)
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(tls.NewListener(listener, tlsConfig)) }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	rootReady := runtime.RootReady()
	rootPID := 0
	for {
		select {
		case <-parent.Done():
			return parent.Err()
		case <-control.Done():
			return errors.New("java_host_retired")
		case <-runtime.Done():
			return errors.New("java_runtime_retired")
		case e := <-failed:
			return e
		case <-rootReady:
			rootPID = runtime.RootPID()
			rootReady = nil
		case <-ticker.C:
			if rootPID > 1 {
				if e := javarunner.ReapPodOrphans(rootPID); e != nil {
					return e
				}
			}
		}
	}
	// Never wait for HTTP/JVM shutdown here. Returning exits PID1, even if native
	// code ignores interrupts. A broker must still independently prove termination.
}
func newHTTPServer(handler http.Handler, invocationTimeout time.Duration) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: invocationTimeout + 5*time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10, ErrorLog: log.New(io.Discard, "", 0)}
}
