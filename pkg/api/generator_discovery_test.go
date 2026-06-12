package api

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

type fakeHostResolver struct {
	hosts map[string][]string
	errs  map[string]error
	calls int
}

func (f *fakeHostResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	f.calls++
	if err, ok := f.errs[host]; ok {
		return nil, err
	}
	addrs, ok := f.hosts[host]
	if !ok {
		return nil, fmt.Errorf("no such host %q", host)
	}
	return addrs, nil
}

func TestParseGeneratorDiscoverySpec(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantOK   bool
		wantSpec generatorDiscoverySpec
	}{
		{
			name:     "plain dns entry",
			raw:      "dns://apexload-generator.apexload.svc.cluster.local:9090",
			wantOK:   true,
			wantSpec: generatorDiscoverySpec{Host: "apexload-generator.apexload.svc.cluster.local", Port: "9090"},
		},
		{
			name:     "dns entry with metadata suffix",
			raw:      "dns://gen.ns.svc:9090|region=eastus2|protocols=http,grpc",
			wantOK:   true,
			wantSpec: generatorDiscoverySpec{Host: "gen.ns.svc", Port: "9090", MetaSuffix: "|region=eastus2|protocols=http,grpc"},
		},
		{
			name:     "uppercase scheme",
			raw:      "DNS://gen.ns.svc:9090",
			wantOK:   true,
			wantSpec: generatorDiscoverySpec{Host: "gen.ns.svc", Port: "9090"},
		},
		{name: "static entry", raw: "gen-0.gen:9090", wantOK: false},
		{name: "missing port", raw: "dns://gen.ns.svc", wantOK: false},
		{name: "empty", raw: "", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, ok := parseGeneratorDiscoverySpec(tc.raw)
			if ok != tc.wantOK {
				t.Fatalf("parseGeneratorDiscoverySpec(%q) ok = %v, want %v", tc.raw, ok, tc.wantOK)
			}
			if ok && spec != tc.wantSpec {
				t.Fatalf("parseGeneratorDiscoverySpec(%q) = %+v, want %+v", tc.raw, spec, tc.wantSpec)
			}
		})
	}
}

func TestSplitGeneratorTargets(t *testing.T) {
	static, discovery := splitGeneratorTargets([]string{
		"gen-0.gen:9090|region=onprem",
		"dns://gen.ns.svc:9090|region=eastus2",
		" ",
		"dns://malformed-no-port",
		"gen-1.gen:9090",
	})
	wantStatic := []string{"gen-0.gen:9090|region=onprem", "gen-1.gen:9090"}
	if !reflect.DeepEqual(static, wantStatic) {
		t.Fatalf("static = %v, want %v", static, wantStatic)
	}
	if len(discovery) != 1 || discovery[0].Host != "gen.ns.svc" || discovery[0].MetaSuffix != "|region=eastus2" {
		t.Fatalf("discovery = %+v, want single gen.ns.svc spec", discovery)
	}
}

func TestGeneratorDiscoveryResolveTargets(t *testing.T) {
	resolver := &fakeHostResolver{hosts: map[string][]string{
		"gen.ns.svc": {"10.0.0.2", "10.0.0.1"},
	}}
	d := &generatorDiscovery{
		static:   []string{"static-gen:9090"},
		specs:    []generatorDiscoverySpec{{Host: "gen.ns.svc", Port: "9090", MetaSuffix: "|region=eastus2"}},
		resolver: resolver,
	}
	targets, complete := d.resolveTargets(context.Background())
	if !complete {
		t.Fatal("expected complete resolution")
	}
	want := []string{"static-gen:9090", "10.0.0.1:9090|region=eastus2", "10.0.0.2:9090|region=eastus2"}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
}

func TestGeneratorDiscoveryResolveTargetsDeduplicates(t *testing.T) {
	resolver := &fakeHostResolver{hosts: map[string][]string{
		"gen.ns.svc": {"10.0.0.1", "10.0.0.1"},
	}}
	d := &generatorDiscovery{
		static:   []string{"10.0.0.1:9090"},
		specs:    []generatorDiscoverySpec{{Host: "gen.ns.svc", Port: "9090"}},
		resolver: resolver,
	}
	targets, _ := d.resolveTargets(context.Background())
	want := []string{"10.0.0.1:9090"}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
}

func TestGeneratorDiscoveryRunOnceUpdatesBackend(t *testing.T) {
	backend := newDistributedExecutionBackend(nil, time.Second, nil)
	resolver := &fakeHostResolver{hosts: map[string][]string{
		"gen.ns.svc": {"10.0.0.1"},
	}}
	d := &generatorDiscovery{
		specs:    []generatorDiscoverySpec{{Host: "gen.ns.svc", Port: "9090", MetaSuffix: "|region=eastus2"}},
		resolver: resolver,
		backend:  backend,
	}

	d.runOnce(context.Background())
	if got := backend.configuredTargets(); !reflect.DeepEqual(got, []string{"10.0.0.1:9090"}) {
		t.Fatalf("configuredTargets = %v, want [10.0.0.1:9090]", got)
	}
	specs := backend.configuredTargetSpecs()
	if len(specs) != 1 || specs[0].Region != "eastus2" {
		t.Fatalf("expected region metadata to propagate, got %+v", specs)
	}

	// Scale up: a new pod appears.
	resolver.hosts["gen.ns.svc"] = []string{"10.0.0.1", "10.0.0.2"}
	d.runOnce(context.Background())
	if got := backend.configuredTargets(); !reflect.DeepEqual(got, []string{"10.0.0.1:9090", "10.0.0.2:9090"}) {
		t.Fatalf("configuredTargets after scale-up = %v", got)
	}

	// Scale down: a pod is removed.
	resolver.hosts["gen.ns.svc"] = []string{"10.0.0.2"}
	d.runOnce(context.Background())
	if got := backend.configuredTargets(); !reflect.DeepEqual(got, []string{"10.0.0.2:9090"}) {
		t.Fatalf("configuredTargets after scale-down = %v", got)
	}
}

func TestGeneratorDiscoveryRunOnceKeepsFleetOnLookupFailure(t *testing.T) {
	backend := newDistributedExecutionBackend(nil, time.Second, nil)
	resolver := &fakeHostResolver{hosts: map[string][]string{
		"gen.ns.svc": {"10.0.0.1", "10.0.0.2"},
	}}
	d := &generatorDiscovery{
		specs:    []generatorDiscoverySpec{{Host: "gen.ns.svc", Port: "9090"}},
		resolver: resolver,
		backend:  backend,
	}
	d.runOnce(context.Background())
	if got := len(backend.configuredTargets()); got != 2 {
		t.Fatalf("expected 2 targets after initial resolve, got %d", got)
	}

	// A transient DNS failure must not shrink an already-applied fleet.
	resolver.errs = map[string]error{"gen.ns.svc": fmt.Errorf("dns timeout")}
	d.runOnce(context.Background())
	if got := len(backend.configuredTargets()); got != 2 {
		t.Fatalf("expected fleet preserved on lookup failure, got %d targets", got)
	}
}

func TestSetTargetsPreservesDrainStates(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"10.0.0.1:9090", "10.0.0.2:9090"}, time.Second, nil)
	if err := backend.SetGeneratorDrainState("10.0.0.1:9090", "draining"); err != nil {
		t.Fatalf("SetGeneratorDrainState: %v", err)
	}
	backend.SetTargets([]string{"10.0.0.1:9090", "10.0.0.2:9090", "10.0.0.3:9090"})
	if got := backend.generatorDrainState("10.0.0.1:9090"); got != "draining" {
		t.Fatalf("drain state lost across SetTargets, got %q", got)
	}
	want := []string{"10.0.0.2:9090", "10.0.0.3:9090"}
	if got := backend.configuredTargets(); !reflect.DeepEqual(got, want) {
		t.Fatalf("configuredTargets = %v, want %v", got, want)
	}
}
