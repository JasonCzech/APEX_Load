package api

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// recordingKubeRestarter fails the test if the resolver ever reaches a cluster
// it should not have.
type recordingKubeRestarter struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingKubeRestarter) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
}

func (r *recordingKubeRestarter) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingKubeRestarter) DeletePod(context.Context, string, string) error {
	r.record("DeletePod")
	return nil
}

func (r *recordingKubeRestarter) RolloutRestart(context.Context, string, string, string) error {
	r.record("RolloutRestart")
	return nil
}

func (r *recordingKubeRestarter) PodByIP(context.Context, string, string, string) (KubePodRef, error) {
	r.record("PodByIP")
	return KubePodRef{}, nil
}

func (r *recordingKubeRestarter) ListPods(context.Context, string, string) ([]KubePodRef, error) {
	r.record("ListPods")
	return nil, nil
}

func (r *recordingKubeRestarter) ScaleStatefulSet(context.Context, string, string, int) error {
	r.record("ScaleStatefulSet")
	return nil
}

// TestUnboundPoolKeepsTheEnvConfiguredScaler is the backward-compatibility
// guarantee for every deployment that predates the cluster registry. Every
// pre-existing pool row gets cluster_id=” from the column migration, so it
// must resolve to exactly the scaler the controller always used. Do not delete
// this test.
func TestUnboundPoolKeepsTheEnvConfiguredScaler(t *testing.T) {
	// APEXLOAD_GENERATOR_PROVISIONER unset is today's default: not_configured.
	t.Setenv(provisionerEnvVar, "")
	fallback := newFleetPoolScaler(nil, K8sRestartSettings{})

	resolver := &registryFleetScalerResolver{fallback: func() fleetPoolScaler { return fallback }}
	pool := &storage.FleetPool{ID: "pool-legacy", Name: "legacy"}

	scaler, err := resolver.ScalerForPool(context.Background(), pool)
	if err != nil {
		t.Fatalf("ScalerForPool: %v", err)
	}
	if scaler != fallback {
		t.Fatalf("scaler = %T, want the env-configured fallback unchanged", scaler)
	}
	if scaler.CanProvision() {
		t.Fatal("the unbound pool gained provisioning it did not have before")
	}
	if _, _, err := scaler.ScalePool(context.Background(), pool, 3); !errors.Is(err, errProvisionerNotConfigured) {
		t.Fatalf("ScalePool error = %v, want the existing not_configured error", err)
	}
}

func TestUnboundPoolFallbackIsResolvedAtCallTime(t *testing.T) {
	var current fleetPoolScaler = unconfiguredFleetScaler{}
	resolver := &registryFleetScalerResolver{fallback: func() fleetPoolScaler { return current }}
	pool := &storage.FleetPool{ID: "pool-legacy"}

	first, err := resolver.ScalerForPool(context.Background(), pool)
	if err != nil {
		t.Fatalf("first ScalerForPool: %v", err)
	}
	if first.CanProvision() {
		t.Fatal("first fallback unexpectedly provisions")
	}

	current = ownerConflictFleetScaler{owner: "keda"}
	second, err := resolver.ScalerForPool(context.Background(), pool)
	if err != nil {
		t.Fatalf("second ScalerForPool: %v", err)
	}
	if second.Describe().Name != "keda" {
		t.Fatalf("fallback name = %q, want reassigned live fallback keda", second.Describe().Name)
	}
}

// TestUnknownClusterFailsClosedWithoutTouchingKubernetes pins the safety
// property: a pool naming a cluster we cannot load must never silently fall
// back to the controller's own cluster.
func TestUnknownClusterFailsClosedWithoutTouchingKubernetes(t *testing.T) {
	recorder := &recordingKubeRestarter{}
	fallback := kubernetesFleetScaler{kube: recorder, namespace: "apex", defaultWorkload: "apexload-generator"}
	resolver := &registryFleetScalerResolver{fallback: func() fleetPoolScaler { return fallback }}

	pool := &storage.FleetPool{ID: "pool-1", Name: "bound", ClusterID: "cluster-gone"}
	scaler, err := resolver.ScalerForPool(context.Background(), pool)
	if err == nil {
		t.Fatalf("ScalerForPool succeeded (%T); a missing cluster must fail closed", scaler)
	}
	if !errors.Is(err, errClusterScalerUnavailable) {
		t.Fatalf("error = %v, want errClusterScalerUnavailable", err)
	}
	if recorder.callCount() != 0 {
		t.Fatalf("made %d Kubernetes calls; a failed cluster resolution must touch nothing", recorder.callCount())
	}
}

// stubQuotaChecker returns a fixed verdict.
type stubQuotaChecker struct {
	verdict preflightResult
	err     error
	calls   int
}

func (s *stubQuotaChecker) CheckScale(context.Context, *storage.ClusterConnection, *storage.FleetPool, int) (preflightResult, error) {
	s.calls++
	return s.verdict, s.err
}

func TestQuotaCheckedScalerRefusesOverQuotaScale(t *testing.T) {
	recorder := &recordingKubeRestarter{}
	inner := kubernetesFleetScaler{kube: recorder, namespace: "apex", defaultWorkload: "apexload-generator"}
	checker := &stubQuotaChecker{verdict: preflightResult{Allowed: false, Reasons: []string{"resource quota \"apex\" limits requests.cpu"}}}
	scaler := quotaCheckedFleetScaler{inner: inner, checker: checker, connection: &storage.ClusterConnection{}}

	_, _, err := scaler.ScalePool(context.Background(), &storage.FleetPool{ID: "pool-1"}, 10)
	if err == nil || !strings.Contains(err.Error(), "requests.cpu") {
		t.Fatalf("error = %v, want a refusal naming the quota", err)
	}
	if recorder.callCount() != 0 {
		t.Fatal("the workload was scaled despite a refused quota preflight")
	}
}

func TestQuotaCheckedScalerAllowsAdmittedScale(t *testing.T) {
	recorder := &recordingKubeRestarter{}
	inner := kubernetesFleetScaler{kube: recorder, namespace: "apex", defaultWorkload: "apexload-generator"}
	checker := &stubQuotaChecker{verdict: preflightResult{Allowed: true}}
	scaler := quotaCheckedFleetScaler{inner: inner, checker: checker, connection: &storage.ClusterConnection{}}

	workload, scaledTo, err := scaler.ScalePool(context.Background(), &storage.FleetPool{ID: "pool-1", MaxSize: 5}, 3)
	if err != nil {
		t.Fatalf("ScalePool: %v", err)
	}
	if workload != "apexload-generator" || scaledTo != 3 {
		t.Fatalf("ScalePool = (%q, %d), want (apexload-generator, 3)", workload, scaledTo)
	}
	if recorder.callCount() != 1 {
		t.Fatalf("made %d Kubernetes calls, want exactly 1", recorder.callCount())
	}
}

// TestQuotaCheckedScalerChecksTheClampedTarget ensures the verdict matches what
// would actually be applied rather than the raw request.
func TestQuotaCheckedScalerChecksTheClampedTarget(t *testing.T) {
	var checkedTarget int
	checker := &clampRecordingChecker{onCheck: func(target int) { checkedTarget = target }}
	scaler := quotaCheckedFleetScaler{
		inner:      kubernetesFleetScaler{kube: &recordingKubeRestarter{}, namespace: "apex", defaultWorkload: "apexload-generator"},
		checker:    checker,
		connection: &storage.ClusterConnection{},
	}
	pool := &storage.FleetPool{ID: "pool-1", MaxSize: 4}
	if _, _, err := scaler.ScalePool(context.Background(), pool, 99); err != nil {
		t.Fatalf("ScalePool: %v", err)
	}
	if checkedTarget != 4 {
		t.Fatalf("preflight checked %d, want the clamped maximum of 4", checkedTarget)
	}
}

type clampRecordingChecker struct {
	onCheck func(int)
}

func (c *clampRecordingChecker) CheckScale(_ context.Context, _ *storage.ClusterConnection, _ *storage.FleetPool, target int) (preflightResult, error) {
	c.onCheck(target)
	return preflightResult{Allowed: true}, nil
}

func TestKubeClusterDialGuard(t *testing.T) {
	guard := kubeClusterDialGuard(false)
	cases := []struct {
		addr    string
		blocked bool
		why     string
	}{
		// Private ranges are the normal case for a Kubernetes API server and
		// must be reachable, unlike the general outbound guard's policy.
		{addr: "10.0.0.1", blocked: false, why: "private AKS API server"},
		{addr: "172.16.5.4", blocked: false, why: "private cluster"},
		{addr: "192.168.1.10", blocked: false, why: "on-prem cluster"},
		{addr: "20.51.3.4", blocked: false, why: "public AKS endpoint"},
		// Instance metadata is never a Kubernetes API server.
		{addr: "169.254.169.254", blocked: true, why: "cloud instance metadata"},
		{addr: "127.0.0.1", blocked: true, why: "loopback in production"},
		{addr: "::1", blocked: true, why: "IPv6 loopback"},
		{addr: "fe80::1", blocked: true, why: "IPv6 link-local"},
		{addr: "0.0.0.0", blocked: true, why: "unspecified"},
		{addr: "224.0.0.1", blocked: true, why: "multicast"},
		// An IPv4-mapped IPv6 form must not evade the IPv4 predicates.
		{addr: "::ffff:169.254.169.254", blocked: true, why: "IPv4-mapped metadata address"},
	}
	for _, tc := range cases {
		addr, err := netip.ParseAddr(tc.addr)
		if err != nil {
			t.Fatalf("ParseAddr(%q): %v", tc.addr, err)
		}
		err = guard(addr)
		if tc.blocked && err == nil {
			t.Errorf("guard(%s) allowed the connection; %s must be blocked", tc.addr, tc.why)
		}
		if !tc.blocked && err != nil {
			t.Errorf("guard(%s) = %v; %s must be allowed", tc.addr, err, tc.why)
		}
	}

	// Loopback is permitted outside production so tests and local development
	// can point at a stub API server.
	devGuard := kubeClusterDialGuard(true)
	loopback := netip.MustParseAddr("127.0.0.1")
	if err := devGuard(loopback); err != nil {
		t.Errorf("non-production guard blocked loopback: %v", err)
	}
	// Metadata stays blocked even outside production.
	if err := devGuard(netip.MustParseAddr("169.254.169.254")); err == nil {
		t.Error("non-production guard allowed cloud instance metadata")
	}
}
