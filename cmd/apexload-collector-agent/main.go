package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
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
	insecure := fs.Bool("insecure", strings.EqualFold(strings.TrimSpace(os.Getenv("APEXLOAD_COLLECTOR_ALLOW_INSECURE")), "true"),
		"Permit a non-loopback plain-http controller URL (sends the token in cleartext; use only on a trusted network)")
	_ = fs.Parse(os.Args[1:])

	agent, err := collectoragent.New(collectoragent.Config{
		ControllerURL: *controller,
		CollectorID:   *collectorID,
		Token:         *token,
		Interval:      *interval,
		Packs:         splitPacks(*packs),
		PrometheusURL: *prometheusURL,
		AllowInsecure: *insecure,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
