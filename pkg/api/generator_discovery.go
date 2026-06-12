package api

import (
	"context"
	"log/slog"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Generator targets in APEXLOAD_GENERATORS may use a dns:// scheme to enable
// dynamic fleet discovery against a Kubernetes headless service:
//
//	APEXLOAD_GENERATORS=dns://apexload-generator.apexload.svc.cluster.local:9090
//
// The controller resolves the host periodically (APEXLOAD_GENERATOR_DISCOVERY_INTERVAL,
// default 15s) and replaces the generator fleet with one target per resolved
// address, so generators added or removed by autoscaling (KEDA/HPA scaling a
// StatefulSet) are picked up without a controller restart. Static entries and
// dns:// entries can be mixed; metadata suffixes (|region=...|protocols=...)
// on a dns:// entry are inherited by every target it discovers.

const (
	generatorDiscoverySchemePrefix    = "dns://"
	generatorDiscoveryIntervalEnvVar  = "APEXLOAD_GENERATOR_DISCOVERY_INTERVAL"
	defaultGeneratorDiscoveryInterval = 15 * time.Second
	generatorDiscoveryResolveTimeout  = 5 * time.Second
)

// generatorHostResolver is the subset of *net.Resolver used by discovery,
// extracted so tests can substitute a fake.
type generatorHostResolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// generatorDiscoverySpec is one dns:// entry from APEXLOAD_GENERATORS.
type generatorDiscoverySpec struct {
	Host string
	Port string
	// MetaSuffix is the raw "|key=value|..." tail appended to each discovered
	// target so parseGeneratorTargetSpec applies the same metadata to all of
	// them (e.g. region).
	MetaSuffix string
}

// parseGeneratorDiscoverySpec returns the discovery spec for a dns:// entry,
// or ok=false when the entry is a static target.
func parseGeneratorDiscoverySpec(raw string) (generatorDiscoverySpec, bool) {
	entry := strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(entry), generatorDiscoverySchemePrefix) {
		return generatorDiscoverySpec{}, false
	}
	entry = entry[len(generatorDiscoverySchemePrefix):]
	metaSuffix := ""
	if idx := strings.Index(entry, "|"); idx >= 0 {
		metaSuffix = entry[idx:]
		entry = entry[:idx]
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(entry))
	if err != nil || host == "" || port == "" {
		slog.Warn("ignoring malformed dns:// generator target", "target", raw)
		return generatorDiscoverySpec{}, false
	}
	return generatorDiscoverySpec{Host: host, Port: port, MetaSuffix: metaSuffix}, true
}

// splitGeneratorTargets separates static target entries from dns:// discovery
// entries. Malformed dns:// entries are dropped (with a warning) rather than
// passed through as bogus static addresses.
func splitGeneratorTargets(raw []string) (static []string, discovery []generatorDiscoverySpec) {
	for _, entry := range raw {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), generatorDiscoverySchemePrefix) {
			if spec, ok := parseGeneratorDiscoverySpec(trimmed); ok {
				discovery = append(discovery, spec)
			}
			continue
		}
		static = append(static, trimmed)
	}
	return static, discovery
}

func generatorDiscoveryInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(generatorDiscoveryIntervalEnvVar))
	if raw == "" {
		return defaultGeneratorDiscoveryInterval
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		slog.Warn("invalid "+generatorDiscoveryIntervalEnvVar+", falling back to default", "value", raw, "default", defaultGeneratorDiscoveryInterval)
		return defaultGeneratorDiscoveryInterval
	}
	return interval
}

// generatorDiscovery periodically re-resolves dns:// generator entries and
// pushes the merged fleet into the distributed execution backend.
type generatorDiscovery struct {
	static   []string
	specs    []generatorDiscoverySpec
	resolver generatorHostResolver
	backend  *distributedExecutionBackend
	interval time.Duration

	mu          sync.Mutex
	lastApplied string
}

func newGeneratorDiscovery(static []string, specs []generatorDiscoverySpec, backend *distributedExecutionBackend) *generatorDiscovery {
	return &generatorDiscovery{
		static:   append([]string(nil), static...),
		specs:    append([]generatorDiscoverySpec(nil), specs...),
		resolver: net.DefaultResolver,
		backend:  backend,
		interval: generatorDiscoveryInterval(),
	}
}

// resolveTargets resolves every dns:// spec and merges the results with the
// static entries, deduplicated by address with static entries first. The
// second return value reports whether every lookup succeeded.
func (d *generatorDiscovery) resolveTargets(ctx context.Context) ([]string, bool) {
	targets := make([]string, 0, len(d.static))
	seen := make(map[string]struct{})
	appendTarget := func(entry string) {
		address := strings.TrimSpace(strings.SplitN(entry, "|", 2)[0])
		if address == "" {
			return
		}
		if _, dup := seen[address]; dup {
			return
		}
		seen[address] = struct{}{}
		targets = append(targets, entry)
	}
	for _, entry := range d.static {
		appendTarget(entry)
	}

	complete := true
	for _, spec := range d.specs {
		lookupCtx, cancel := context.WithTimeout(ctx, generatorDiscoveryResolveTimeout)
		addrs, err := d.resolver.LookupHost(lookupCtx, spec.Host)
		cancel()
		if err != nil {
			slog.Warn("generator discovery lookup failed", "host", spec.Host, "error", err)
			complete = false
			continue
		}
		sort.Strings(addrs)
		for _, addr := range addrs {
			appendTarget(net.JoinHostPort(addr, spec.Port) + spec.MetaSuffix)
		}
	}
	return targets, complete
}

// runOnce performs a single discovery pass. Incomplete rounds (any lookup
// failure) never shrink an already-applied fleet — transient DNS errors must
// not drop generators mid-run.
func (d *generatorDiscovery) runOnce(ctx context.Context) {
	targets, complete := d.resolveTargets(ctx)
	applied := strings.Join(targets, "\n")

	d.mu.Lock()
	defer d.mu.Unlock()
	if !complete && d.lastApplied != "" {
		return
	}
	if applied == d.lastApplied {
		return
	}
	d.backend.SetTargets(targets)
	d.lastApplied = applied
	slog.Info("generator fleet updated via DNS discovery", "targets", len(targets))
}

func (d *generatorDiscovery) loop(ctx context.Context) {
	d.runOnce(ctx)
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.runOnce(ctx)
		}
	}
}

func (s *Server) startGeneratorDiscovery() {
	if s == nil || s.generatorDiscovery == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.discoveryCancel = cancel
	slog.Info("generator DNS discovery enabled",
		"services", len(s.generatorDiscovery.specs),
		"interval", s.generatorDiscovery.interval)
	go s.generatorDiscovery.loop(ctx)
}
