package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// The very first probe of a cycle is synchronous (gating semantics need a real
// observation), and its result is served from cache within the TTL so repeated
// launch preflights do not fork a JVM under the runner lock each time.
func TestJavaVuserRuntimeHealthCacheCachesWithinTTL(t *testing.T) {
	cache := &javaVuserRuntimeHealthCache{}
	var probes atomic.Int32
	probe := func() bool {
		probes.Add(1)
		return true
	}

	if !cache.healthyNow(probe) {
		t.Fatal("first probe result = false, want true")
	}
	for range 2 {
		if !cache.healthyNow(probe) {
			t.Fatal("cached result = false, want true")
		}
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("probe executions = %d, want 1 (results cached within TTL)", got)
	}
}

// Once a stale observation exists, callers must never block on a refresh, but
// an expired healthy value must not authorize a launch while that refresh runs.
func TestJavaVuserRuntimeHealthCacheFailsClosedWhileRevalidating(t *testing.T) {
	cache := &javaVuserRuntimeHealthCache{}
	block := make(chan struct{})
	var result atomic.Bool
	result.Store(true)
	var probes atomic.Int32
	probe := func() bool {
		if probes.Add(1) > 1 {
			<-block
		}
		return result.Load()
	}

	if !cache.healthyNow(probe) {
		t.Fatal("initial probe = false, want true")
	}
	// Expire the observation and flip the underlying health.
	cache.mu.Lock()
	cache.expiresAt = time.Now().Add(-time.Second)
	cache.mu.Unlock()
	result.Store(false)

	served := make(chan bool, 1)
	go func() { served <- cache.healthyNow(probe) }()
	select {
	case healthy := <-served:
		if healthy {
			t.Fatal("expired healthy observation authorized a launch while its refresh was still pending")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("caller with a stale observation blocked on the background refresh")
	}

	close(block)
	deadline := time.Now().Add(2 * time.Second)
	for {
		cache.mu.Lock()
		refreshComplete := cache.inflight == nil && cache.checked && !cache.healthy
		cache.mu.Unlock()
		if refreshComplete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh never published the new unhealthy observation")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if cache.healthyNow(probe) {
		t.Fatal("refreshed unhealthy observation authorized a launch")
	}
}

// Concurrent cold-cache callers must join a single in-flight probe instead of
// forking one JVM health subprocess each.
func TestJavaVuserRuntimeHealthCacheColdCallersJoinSingleProbe(t *testing.T) {
	cache := &javaVuserRuntimeHealthCache{}
	entered := make(chan struct{})
	block := make(chan struct{})
	var probes atomic.Int32
	probe := func() bool {
		probes.Add(1)
		close(entered)
		<-block
		return true
	}

	first := make(chan bool, 1)
	go func() { first <- cache.healthyNow(probe) }()
	<-entered

	var wg sync.WaitGroup
	results := make(chan bool, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- cache.healthyNow(probe)
		}()
	}
	close(block)
	wg.Wait()
	close(results)
	if !<-first {
		t.Fatal("probing caller result = false, want true")
	}
	for healthy := range results {
		if !healthy {
			t.Fatal("joined caller result = false, want true")
		}
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("probe executions = %d, want 1 (cold callers must join the in-flight probe)", got)
	}
}

func TestJavaVuserRuntimeHealthCacheContainsProbePanicAndUnblocksWaiters(t *testing.T) {
	cache := &javaVuserRuntimeHealthCache{}
	var probes atomic.Int32
	probe := func() bool {
		probes.Add(1)
		panic("resolved-runner-secret")
	}

	if cache.healthyNow(probe) {
		t.Fatal("panicking probe result = true, want fail-closed false")
	}
	if cache.healthyNow(func() bool {
		probes.Add(1)
		return true
	}) {
		t.Fatal("cached panicking probe result = true, want cached unhealthy observation")
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("probe executions = %d, want one", got)
	}
	cache.mu.Lock()
	inflight := cache.inflight
	cache.mu.Unlock()
	if inflight != nil {
		t.Fatal("panicking probe left the cache permanently in flight")
	}
}

func TestDistributedJavaVuserFreshInventoryOverridesOptimisticTargetLabels(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	backend := newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"},
		time.Second,
		nil,
	)
	runner.setExecutionBackend(backend)
	if !runner.distributedJavaVuserRunnerAvailableLocked(backend, false) {
		t.Fatal("target label should be a provisional capability before the first heartbeat")
	}
	runner.liveInventoryMu.Lock()
	runner.liveInventory = []GeneratorInventoryEntry{{
		Target:             "gen-jvm:9090",
		GeneratorID:        "gen-jvm",
		Status:             "connected",
		DrainState:         "active",
		SupportedProtocols: []string{},
		CompatibleFeatures: []string{},
		Labels:             map[string]string{"runtime": "java_vuser", "image": "generator-java"},
	}}
	runner.liveInventoryAt = time.Now()
	runner.liveInventoryMu.Unlock()
	if runner.distributedJavaVuserRunnerAvailableLocked(backend, false) {
		t.Fatal("fresh heartbeat without java_vuser was overridden by optimistic labels")
	}

	runner.liveInventoryMu.Lock()
	runner.liveInventoryAt = time.Now().Add(-defaultGeneratorSnapshotTTL - time.Second)
	runner.liveInventoryMu.Unlock()
	if !runner.distributedJavaVuserRunnerAvailableLocked(backend, false) {
		t.Fatal("stale inventory should fall back to provisional target labels")
	}

	runner.liveInventoryMu.Lock()
	runner.liveInventoryAt = time.Now().Add(time.Minute)
	runner.liveInventoryMu.Unlock()
	if !runner.distributedJavaVuserRunnerAvailableLocked(backend, false) {
		t.Fatal("future-dated inventory should fail stale and fall back to provisional target labels")
	}
}

func TestDistributedJavaVuserPersistentCapabilityRequiresFreshFeatureEvidence(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	backend := newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"},
		time.Second,
		nil,
	)
	runner.setExecutionBackend(backend)
	if runner.distributedJavaVuserRunnerAvailableLocked(backend, true) {
		t.Fatal("persistent lifecycle was inferred from provisional target labels before a heartbeat")
	}

	setInventory := func(protocols, features []string) {
		runner.liveInventoryMu.Lock()
		runner.liveInventory = []GeneratorInventoryEntry{{
			Target:             "gen-jvm:9090",
			GeneratorID:        "gen-jvm",
			Status:             "connected",
			DrainState:         "active",
			SupportedProtocols: protocols,
			CompatibleFeatures: features,
			Labels:             map[string]string{"runtime": "java_vuser", "image": "generator-java"},
			RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{
				Protocol:        "java_vuser",
				Available:       true,
				MaxVUsers:       25,
				AvailableVUsers: 25,
				Features:        append([]string(nil), features...),
				OccupancyMode:   generatorrpc.RuntimeOccupancyModeIdle,
			}},
		}}
		runner.liveInventoryAt = time.Now()
		runner.liveInventoryMu.Unlock()
	}

	setInventory([]string{"java_vuser"}, governedPersistentJavaRuntimeFeatures())
	runner.liveInventoryMu.Lock()
	runner.liveInventory[0].RuntimeCapacities = nil
	runner.liveInventoryMu.Unlock()
	if runner.distributedJavaVuserRunnerAvailableLocked(backend, true) {
		t.Fatal("persistent lifecycle was accepted without a runtime capacity advertisement")
	}

	setInventory([]string{"java_vuser"}, governedPersistentJavaRuntimeFeatures())
	runner.liveInventoryMu.Lock()
	runner.liveInventory[0].RuntimeCapacities[0].Available = false
	runner.liveInventoryMu.Unlock()
	if runner.distributedJavaVuserRunnerAvailableLocked(backend, true) {
		t.Fatal("persistent lifecycle was accepted from an unavailable runtime capacity")
	}

	setInventory([]string{"java_vuser"}, governedPersistentJavaRuntimeFeatures())
	runner.liveInventoryMu.Lock()
	runner.liveInventory[0].RuntimeCapacities[0].MaxVUsers = 0
	runner.liveInventoryMu.Unlock()
	if runner.distributedJavaVuserRunnerAvailableLocked(backend, true) {
		t.Fatal("persistent lifecycle was accepted without a positive runtime maximum")
	}

	setInventory([]string{"java_vuser"}, governedPersistentJavaRuntimeFeatures())
	runner.liveInventoryMu.Lock()
	runner.liveInventory[0].RuntimeCapacities[0].OccupancyMode = ""
	runner.liveInventoryMu.Unlock()
	if runner.distributedJavaVuserRunnerAvailableLocked(backend, true) {
		t.Fatal("persistent lifecycle was accepted without runtime occupancy evidence")
	}

	setInventory([]string{"java_vuser"}, []string{
		protocol.FeatureJavaVuserPersistentLifecycleV1,
		protocol.FeatureJavaVuserSessionFrameTokenV1,
	})
	if !runner.distributedJavaVuserRunnerAvailableLocked(backend, true) {
		t.Fatal("fresh persistent-lifecycle feature evidence did not make the runtime available")
	}

	setInventory([]string{"java_vuser"}, []string{protocol.FeatureJavaVuserPersistentLifecycleV1})
	if runner.distributedJavaVuserRunnerAvailableLocked(backend, true) {
		t.Fatal("tokenless persistent-lifecycle feature evidence was accepted")
	}

	setInventory([]string{"java_vuser"}, []string{})
	if runner.distributedJavaVuserRunnerAvailableLocked(backend, true) {
		t.Fatal("fresh empty feature advertisement was overridden by target labels")
	}

	setInventory([]string{}, []string{protocol.FeatureJavaVuserPersistentLifecycleV1})
	if runner.distributedJavaVuserRunnerAvailableLocked(backend, true) {
		t.Fatal("persistent feature without fresh java_vuser protocol support was accepted")
	}
}

func TestRuntimeCapabilityInventoryRefreshRunsWithConfiguredCapacitySource(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	if got := server.runner.GetSystemSettings().CapacityPlanning.Availability.Source; got != capacityAvailabilityConfigured {
		t.Fatalf("capacity availability source = %q, want default %q", got, capacityAvailabilityConfigured)
	}
	server.runner.setExecutionBackend(stubInventoryBackend{entries: []GeneratorInventoryEntry{{
		Target:             "gen-jvm:9090",
		GeneratorID:        "gen-jvm",
		Status:             "connected",
		DrainState:         "active",
		SupportedProtocols: []string{"java_vuser"},
		CompatibleFeatures: governedPersistentJavaRuntimeFeatures(),
		AvailableCapacity:  100,
		MaxVUsers:          100,
		RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{
			Protocol:        "java_vuser",
			Available:       true,
			MaxVUsers:       25,
			AvailableVUsers: 25,
			Features:        governedPersistentJavaRuntimeFeatures(),
			OccupancyMode:   generatorrpc.RuntimeOccupancyModeIdle,
		}},
	}}})

	server.refreshGeneratorInventory(context.Background())

	inventory := server.runner.cachedGeneratorInventory()
	if len(inventory) != 1 {
		t.Fatalf("cached generator inventory = %#v, want one refreshed entry", inventory)
	}
	if !generatorInventoryHasJavaVuserPersistentLifecycle(inventory[0]) {
		t.Fatalf("cached runtime capabilities = %#v, want governed persistent Java evidence", inventory[0])
	}
}
