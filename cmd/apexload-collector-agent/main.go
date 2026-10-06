package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/collectoragent"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		info := buildinfo.Get()
		fmt.Printf("apexload-collector-agent %s (%s, %s)\n", info.DisplayVersion, info.Commit, info.BuildTime)
		return
	}

	fs := flag.NewFlagSet("apexload-collector-agent", flag.ExitOnError)
	controller := fs.String("controller", envOr("APEXLOAD_CONTROLLER_URL", "http://127.0.0.1:8080"), "Controller base URL")
	collectorID := fs.String("collector-id", os.Getenv("APEXLOAD_COLLECTOR_ID"), "Collector ID issued at registration")
	token := fs.String("token", os.Getenv("APEXLOAD_COLLECTOR_TOKEN"), "Collector token (or APEXLOAD_COLLECTOR_TOKEN)")
	interval := fs.Duration("interval", 15*time.Second, "Heartbeat interval")
	defaultPacks := "linux-hosts"
	if runtime.GOOS != "linux" {
		defaultPacks = "go-runtime"
	}
	packs := fs.String("packs", envOr("APEXLOAD_COLLECTOR_PACKS", defaultPacks), "Comma-separated metric pack names (must be in the collector's allow-list)")
	prometheusURL := fs.String("prometheus-url", os.Getenv("APEXLOAD_COLLECTOR_PROMETHEUS_URL"), "Prometheus text-exposition endpoint for the prometheus pack")
	monitorKey := fs.String("monitor-public-key", os.Getenv("APEXLOAD_MONITOR_PUBLIC_KEY"), "Base64 Ed25519 controller key; enables signed run jobs and traffic-free availability heartbeats")
	adapterFile := fs.String("adapter-targets", os.Getenv("APEXLOAD_COLLECTOR_ADAPTER_TARGETS"), "JSON file containing locally approved SSH/JMX/JDBC/OTLP/cloud/Dynatrace/Elastic/Prometheus-query and Kafka/Service Bus/Solace management destinations")
	credentialKeyFile := fs.String("credential-key-file", os.Getenv("APEXLOAD_COLLECTOR_CREDENTIAL_KEY_FILE"), "Protected file containing base64 X25519 private key")
	otlpListen := fs.String("otlp-listen", "", "Loopback OTLP HTTP JSON listen address; place behind authenticated TLS proxy for remote emitters")
	otlpTokenFile := fs.String("otlp-token-file", "", "Protected file containing OTLP bearer token (at least 32 bytes)")
	spool := fs.String("spool-directory", envOr("APEXLOAD_COLLECTOR_SPOOL_DIRECTORY", "collector-state"), "Protected persistent directory for unacknowledged monitor evidence")
	insecure := fs.Bool("insecure", strings.EqualFold(strings.TrimSpace(os.Getenv("APEXLOAD_COLLECTOR_ALLOW_INSECURE")), "true"),
		"Permit a non-loopback plain-http controller URL (sends the token in cleartext; use only on a trusted network)")
	_ = fs.Parse(os.Args[1:])
	publicKey, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(*monitorKey))
	if decodeErr != nil {
		fmt.Fprintln(os.Stderr, "error: invalid monitor public key")
		os.Exit(1)
	}

	targets, err := collectoragent.LoadAdapterTargets(*adapterFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: invalid adapter target configuration")
		os.Exit(1)
	}
	var privateKey []byte
	if *credentialKeyFile != "" {
		encoded, readErr := readProtectedFile(*credentialKeyFile)
		if readErr != nil {
			fmt.Fprintln(os.Stderr, "error: cannot read protected collector key")
			os.Exit(1)
		}
		privateKey, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
		if err != nil || len(privateKey) != 32 {
			fmt.Fprintln(os.Stderr, "error: invalid collector key")
			os.Exit(1)
		}
	}
	var receiver *collectoragent.OTLPReceiver
	var listener net.Listener
	if *otlpListen != "" {
		host, _, parseErr := net.SplitHostPort(*otlpListen)
		ip := net.ParseIP(host)
		if parseErr != nil || ip == nil || !ip.IsLoopback() {
			fmt.Fprintln(os.Stderr, "error: OTLP listener must bind an explicit loopback IP")
			os.Exit(1)
		}
		token, readErr := readProtectedFile(*otlpTokenFile)
		if readErr != nil {
			fmt.Fprintln(os.Stderr, "error: cannot read protected OTLP token")
			os.Exit(1)
		}
		streams := map[string]string{}
		for _, target := range targets {
			if target.Pack == "otel-metrics" {
				streams[target.Destination] = strings.TrimSpace(string(token))
			}
		}
		receiver, err = collectoragent.NewOTLPReceiver(streams)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		listener, err = net.Listen("tcp", *otlpListen)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: OTLP listen failed")
			os.Exit(1)
		}
	}
	agent, err := collectoragent.New(collectoragent.Config{
		ControllerURL:  *controller,
		AdapterTargets: targets, CredentialPrivateKey: privateKey, OTLP: receiver,
		CollectorID:         *collectorID,
		Token:               *token,
		Interval:            *interval,
		Packs:               splitPacks(*packs),
		PrometheusURL:       *prometheusURL,
		ControllerPublicKey: publicKey,
		SpoolDirectory:      *spool,
		AllowInsecure:       *insecure,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if listener != nil {
		server := &http.Server{Handler: receiver, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
		defer server.Close()
		go func() {
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				slog.Error("OTLP receiver stopped")
				stop()
			}
		}()
	}

	slog.Info("apexload-collector-agent starting",
		"controller", *controller, "collector_id", *collectorID, "interval", *interval,
		"packs", splitPacks(*packs), "version", buildinfo.Get().DisplayVersion)

	if err := agent.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("collector agent stopped", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func splitPacks(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func readProtectedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 64<<10 || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("protected file must be at most 64 KiB and readable only by its owner")
	}
	return io.ReadAll(io.LimitReader(file, (64<<10)+1))
}
