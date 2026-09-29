// The dedicated Java broker is explicitly configured and experimental. Starting
// it is not qualification of Kubernetes containment or an external attestor.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	if run(ctx, os.Args[1:]) != nil {
		fmt.Fprintln(os.Stderr, "java_host_broker_stopped")
		os.Exit(1)
	}
}
func run(parent context.Context, args []string) error {
	flags := flag.NewFlagSet("java-host-broker", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("config", "", "")
	initialize := flags.Bool("initialize-store", false, "")
	initializeOnly := flags.Bool("initialize-store-only", false, "")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errConfiguration
	}
	cfg, e := loadConfig(*path)
	if e != nil {
		return e
	}
	if *initializeOnly {
		if *initialize {
			return errConfiguration
		}
		store, e := broker.Open(parent, cfg.StatePath, cfg.storeConfig(), true)
		if e != nil {
			return e
		}
		return store.Close()
	}
	return runConfigured(parent, cfg, *initialize, nil)
}

func runConfigured(parent context.Context, cfg brokerConfig, initialize bool, onListening func(net.Addr)) error {
	if _, _, e := net.SplitHostPort(cfg.Listen); e != nil {
		return errConfiguration
	}
	serverTLS, e := clientTLS(cfg.ServerTLS)
	if e != nil {
		return e
	}
	serverTLS.ClientCAs = serverTLS.RootCAs
	serverTLS.RootCAs = nil
	serverTLS.ClientAuth = tls.RequireAndVerifyClientCert
	controlTLS, e := clientTLS(cfg.ControlTLS)
	if e != nil {
		return e
	}
	issuerCertificate, e := readBounded(cfg.HostIssuer.Certificate, 128<<10)
	if e != nil {
		return e
	}
	issuerKey, e := readBounded(cfg.HostIssuer.Key, 32<<10)
	if e != nil {
		return e
	}
	brokerTrust, e := readBounded(cfg.HostIssuer.CA, 128<<10)
	if e != nil {
		return e
	}
	issuer, e := broker.NewHostCredentialIssuer(issuerCertificate, issuerKey, brokerTrust, cfg.HostTrustDomain, cfg.HostDNSDomain)
	if e != nil {
		return e
	}
	attestorTLS := controlTLS.Clone()
	attestorTLS.RootCAs, e = rootsFrom(cfg.AttestorCA)
	if e != nil {
		return e
	}
	attestor, e := broker.NewExternalAttestor(broker.AttestorConfig{Endpoint: cfg.AttestorEndpoint, ServerURI: cfg.AttestorURI, BrokerClientURI: cfg.ControlClientURI, QualificationSHA256: cfg.AttestorQualificationSHA256, Timeout: 3 * time.Second}, attestorTLS)
	if e != nil {
		return e
	}
	defer attestor.Close()
	kubeRoots, e := rootsFrom(cfg.KubernetesCA)
	if e != nil {
		return e
	}
	token := func(context.Context) (string, error) {
		data, e := readBounded(cfg.KubernetesToken, 16384)
		if e != nil {
			return "", e
		}
		return strings.TrimSpace(string(data)), nil
	}
	if value, e := token(parent); e != nil || value == "" {
		return errConfiguration
	}
	backend, e := broker.NewKubernetesBackend(cfg.KubernetesEndpoint, cfg.Namespace, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: kubeRoots}, token, attestor)
	if e != nil {
		return e
	}
	defer backend.Close()
	store, e := broker.Open(parent, cfg.StatePath, cfg.storeConfig(), initialize)
	if e != nil {
		return e
	}
	defer store.Close()
	epoch, e := store.StartEpoch(parent)
	if e != nil {
		return e
	}
	profiles := map[string]broker.BrokerProfile{}
	maxInvocation := time.Second
	for id, profile := range cfg.Profiles {
		managed := profile.managed(cfg.ControlClientURI)
		factory := func(contexts []javarunner.DedicatedHostContext) (broker.DedicatedHostPreparer, error) {
			return broker.NewManagedKubernetesPreparer(store, backend, epoch, issuer, controlTLS, managed, contexts)
		}
		// Validate operator TLS/control profiles before opening the listener.
		if _, e := factory([]javarunner.DedicatedHostContext{{Contract: javarunner.DedicatedHostContextContract, SessionID: "validation", GroupName: "validation", ScenarioID: "validation"}}); e != nil {
			return e
		}
		profiles[id] = broker.BrokerProfile{PolicySHA256: profile.policySHA256(), TransportLifetime: managed.TransportLifetime, InvocationTimeout: managed.InvocationTimeout, ConfigurationSHA256: profile.ConfigurationSHA256, MemoryBytes: profile.MemoryBytes, HostLeaseTTL: managed.LeaseTTL, PreparationTimeout: managed.PreparationTimeout, NewPreparer: factory}
		maxInvocation = max(maxInvocation, managed.InvocationTimeout)
	}
	principals := make([]broker.BrokerPrincipal, len(cfg.Principals))
	for i, p := range cfg.Principals {
		principals[i] = broker.BrokerPrincipal{URI: p.URI, OwnerID: p.OwnerID, Profiles: p.Profiles}
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	service, e := broker.NewBrokerService(ctx, store, epoch, time.Duration(cfg.OwnerLeaseMS)*time.Millisecond, principals, profiles)
	if e != nil {
		return e
	}
	defer service.Close()
	reconciler, e := broker.NewReconciler(store, epoch, backend, 5*time.Second)
	if e != nil {
		return e
	}
	listener, e := net.Listen("tcp", cfg.Listen)
	if e != nil {
		return e
	}
	defer listener.Close()
	server := &http.Server{Handler: service, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: maxInvocation + 10*time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	defer server.Close()
	var workers sync.WaitGroup
	workers.Go(func() { reconcileLoop(ctx, reconciler) })
	workers.Go(func() { transportLoop(ctx, store, backend, epoch) })
	defer func() { cancel(); workers.Wait() }()
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(tls.NewListener(listener, serverTLS)) }()
	if onListening != nil {
		onListening(listener.Addr())
	}
	select {
	case <-parent.Done():
		return nil
	case e := <-failed:
		return e
	}
}
func reconcileLoop(ctx context.Context, reconciler *broker.Reconciler) {
	for {
		step, stop := context.WithTimeout(ctx, 10*time.Second)
		_ = reconciler.Step(step)
		stop()
		if !waitTick(ctx, time.Second) {
			return
		}
	}
}
func waitTick(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func transportLoop(ctx context.Context, store *broker.Store, backend *broker.KubernetesBackend, epoch string) {
	// Round-robin bounded batches retain late-create cleanup without rereading
	// all historical Secrets on every tick. Tombstones remain in durable state.
	cursor := 0
	for {
		records, _, e := store.Snapshot(ctx, epoch)
		if e == nil && len(records) > 0 {
			sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
			for range min(16, len(records)) {
				cursor %= len(records)
				record := records[cursor]
				cursor++
				if record.State != javarunner.HostReaped {
					continue
				}
				step, stop := context.WithTimeout(ctx, 3*time.Second)
				_ = backend.ReconcileHostTransport(step, store, epoch, record.ID)
				stop()
				if ctx.Err() != nil {
					return
				}
			}
		}
		if !waitTick(ctx, 5*time.Second) {
			return
		}
	}
}
