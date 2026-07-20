package perfevidence

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

const syntheticRampProtocolName = "apexload-performance-ramp-probe"

type Config struct {
	Profile               string
	HarnessIterations     int
	HarnessSamples        int
	HTTPVUsers            int
	HTTPDensityTimeout    time.Duration
	HTTPDensityHold       time.Duration
	RampVUsers            int
	RampSamples           int
	MetricsLatencySamples int
	MetricsInterval       time.Duration
	MetricsProbeTimeout   time.Duration
	HostClass             string
	RunnerImage           string
	VCSStatus             string
}

func DefaultConfig(profile string) (Config, error) {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "smoke":
		return Config{
			Profile:               "smoke",
			HarnessIterations:     10_000,
			HarnessSamples:        3,
			HTTPVUsers:            100,
			HTTPDensityTimeout:    5 * time.Second,
			HTTPDensityHold:       50 * time.Millisecond,
			RampVUsers:            100,
			RampSamples:           2,
			MetricsLatencySamples: 3,
			MetricsInterval:       40 * time.Millisecond,
			MetricsProbeTimeout:   750 * time.Millisecond,
		}, nil
	case "release":
		return defaultReleaseConfig(), nil
	default:
		return Config{}, fmt.Errorf("unknown benchmark profile %q (want smoke or release)", profile)
	}
}

func defaultReleaseConfig() Config {
	return Config{
		Profile:               "release",
		HarnessIterations:     250_000,
		HarnessSamples:        7,
		HTTPVUsers:            10_000,
		HTTPDensityTimeout:    30 * time.Second,
		HTTPDensityHold:       250 * time.Millisecond,
		RampVUsers:            1_000,
		RampSamples:           5,
		MetricsLatencySamples: 7,
		MetricsInterval:       time.Second,
		MetricsProbeTimeout:   4 * time.Second,
	}
}

func (config Config) Validate() error {
	if config.Profile != "smoke" && config.Profile != "release" {
		return fmt.Errorf("profile must be smoke or release")
	}
	if config.HarnessIterations <= 0 || config.HarnessSamples <= 0 {
		return fmt.Errorf("harness iterations and samples must be positive")
	}
	if config.HTTPVUsers <= 0 || config.HTTPVUsers > engine.MaxEngineVUsers {
		return fmt.Errorf("HTTP VUsers must be between 1 and %d", engine.MaxEngineVUsers)
	}
	if config.HTTPDensityTimeout <= 0 || config.HTTPDensityHold < 0 || config.HTTPDensityHold >= config.HTTPDensityTimeout {
		return fmt.Errorf("HTTP density timeout must be positive and greater than the non-negative hold duration")
	}
	if config.HTTPDensityTimeout > 10*time.Minute {
		return fmt.Errorf("HTTP density timeout must not exceed 10 minutes")
	}
	if config.RampVUsers <= 0 || config.RampVUsers > engine.MaxEngineVUsers || config.RampSamples <= 0 {
		return fmt.Errorf("ramp VUsers must be between 1 and %d and ramp samples must be positive", engine.MaxEngineVUsers)
	}
	if config.MetricsLatencySamples <= 0 || config.MetricsInterval <= 0 {
		return fmt.Errorf("metrics latency samples and interval must be positive")
	}
	if config.MetricsProbeTimeout <= config.MetricsInterval {
		return fmt.Errorf("metrics probe timeout must be greater than the collection interval")
	}
	if config.VCSStatus != "" && config.VCSStatus != "clean" && config.VCSStatus != "modified" && config.VCSStatus != "unknown" {
		return fmt.Errorf("VCS status must be clean, modified, unknown, or empty")
	}
	return nil
}

// Run executes all four local component probes and returns a validated report.
// No files are written; the command layer decides whether to print or persist it.
func Run(ctx context.Context, config Config) (Report, error) {
	if err := config.Validate(); err != nil {
		return Report{}, err
	}

	harness, err := measureHarness(config)
	if err != nil {
		return Report{}, fmt.Errorf("measure harness overhead: %w", err)
	}
	density, err := measureHTTPDensity(ctx, config)
	if err != nil {
		return Report{}, fmt.Errorf("measure local HTTP VUser density: %w", err)
	}
	ramp, err := measureSyntheticRamp(ctx, config)
	if err != nil {
		return Report{}, fmt.Errorf("measure synthetic engine ramp: %w", err)
	}
	metricsLatency, err := measureMetricsPublicationLatency(ctx, config)
	if err != nil {
		return Report{}, fmt.Errorf("measure metrics collector latency: %w", err)
	}

	measurements, err := buildMeasurements(harness, density, ramp, metricsLatency)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		SchemaVersion: SchemaVersion,
		EvidenceKind:  EvidenceKind,
		GeneratedAt:   time.Now().UTC(),
		Build:         collectBuildMetadata(config.VCSStatus),
		Environment:   collectEnvironmentMetadata(config),
		Configuration: RunConfiguration{
			Profile:                       config.Profile,
			HarnessIterations:             config.HarnessIterations,
			HarnessSamples:                config.HarnessSamples,
			HTTPVUsers:                    config.HTTPVUsers,
			HTTPDensityTimeoutMillis:      config.HTTPDensityTimeout.Milliseconds(),
			HTTPDensityHoldMillis:         config.HTTPDensityHold.Milliseconds(),
			HTTPDensityReleaseGraceMillis: httpDensityReleaseGrace(config).Milliseconds(),
			RampVUsers:                    config.RampVUsers,
			RampSamples:                   config.RampSamples,
			MetricsLatencySamples:         config.MetricsLatencySamples,
			MetricsIntervalMillis:         config.MetricsInterval.Milliseconds(),
			MetricsProbeTimeoutMillis:     config.MetricsProbeTimeout.Milliseconds(),
		},
		ClaimPolicy: ClaimPolicy{
			TargetAttainmentClaimed: false,
			Reason:                  "These are controlled local component probes; none reproduce the complete production target scope.",
		},
		Measurements: measurements,
	}
	report.Finalize()
	if err := ValidateReport(report); err != nil {
		return Report{}, fmt.Errorf("generated invalid performance evidence: %w", err)
	}
	return report, nil
}

type harnessProbe struct {
	NanosecondsPerTransaction []float64
	Dropped                   int64
}

func measureHarness(config Config) (harnessProbe, error) {
	// Warm the allocation/channel path before collecting independent samples.
	warmupIterations := min(config.HarnessIterations, 2_000)
	if _, err := engine.RunMetricsRecordProbe(warmupIterations); err != nil {
		return harnessProbe{}, err
	}

	result := harnessProbe{NanosecondsPerTransaction: make([]float64, 0, config.HarnessSamples)}
	for range config.HarnessSamples {
		runtime.GC()
		sample, err := engine.RunMetricsRecordProbe(config.HarnessIterations)
		if err != nil {
			return harnessProbe{}, err
		}
		result.NanosecondsPerTransaction = append(
			result.NanosecondsPerTransaction,
			float64(sample.Elapsed.Nanoseconds())/float64(sample.Iterations),
		)
		result.Dropped += sample.Dropped
	}
	return result, nil
}

type httpDensityProbe struct {
	ConfiguredVUsers         int
	PeakConcurrentRequests   int64
	RequestsReceived         int64
	SuccessfulTransactions   int64
	FailedTransactions       int64
	ActionSamples            int64
	ActionErrors             int64
	ActionP95Milliseconds    float64
	ActionP99Milliseconds    float64
	MetricsDroppedResults    int64
	TimeToPeak               time.Duration
	RuntimeGoroutineDelta    int
	StackInUseDeltaBytes     uint64
	HeapAllocDeltaBytes      uint64
	TotalAllocDeltaBytes     uint64
	StackBytesPerPeakRequest float64
	ProcessCPUPercent        float64
	ProcessRSSMegabytes      float64
	OpenFDCount              int32
	ThreadCount              int32
	HeapInUseMegabytes       float64
	GCCountDelta             uint32
	GCPauseMilliseconds      float64
}

type engineOutcome struct {
	result *engine.RunResult
	err    error
}

func measureHTTPDensity(ctx context.Context, config Config) (httpDensityProbe, error) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineGoroutines := runtime.NumGoroutine()
	resourceCollector := engine.NewResourceCollector()
	resourceCollector.Collect()

	releaseRequests := make(chan struct{})
	var received atomic.Int64
	var inFlight atomic.Int64
	var peak atomic.Int64
	var peakAtUnixNano atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		received.Add(1)
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			previous := peak.Load()
			if current <= previous {
				break
			}
			if peak.CompareAndSwap(previous, current) {
				peakAtUnixNano.Store(time.Now().UnixNano())
				break
			}
		}
		select {
		case <-releaseRequests:
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("Cache-Control", "no-store")
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write([]byte(`{"ok":true}`))
		case <-request.Context().Done():
		}
	}))
	defer server.Close()

	script := &dsl.Script{
		ID:       "performance-http-density-probe",
		Name:     "Performance HTTP density probe",
		Version:  "1",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: server.URL,
			HTTP: dsl.HTTPConfig{
				RequestTimeoutMs: int((config.HTTPDensityTimeout + httpDensityReleaseGrace(config)).Milliseconds()),
				ConnectTimeoutMs: int((config.HTTPDensityTimeout + httpDensityReleaseGrace(config)).Milliseconds()),
			},
			RunLogic: dsl.RunLogicConfig{
				Iterations:       1,
				WorkloadModel:    "closed",
				AutoTransactions: "per_iteration",
			},
		},
		Actions: []dsl.Action{{Name: "loopback", Type: http.MethodGet, Method: http.MethodGet, Target: "/benchmark"}},
	}
	eng, err := engine.New(engine.Config{
		Script:   script,
		VUsers:   config.HTTPVUsers,
		Duration: config.HTTPDensityTimeout + httpDensityReleaseGrace(config),
	})
	if err != nil {
		close(releaseRequests)
		return httpDensityProbe{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, config.HTTPDensityTimeout+httpDensityReleaseGrace(config)+time.Second)
	defer cancel()
	runDone := make(chan engineOutcome, 1)
	started := time.Now()
	go func() {
		result, runErr := eng.Run(runCtx)
		runDone <- engineOutcome{result: result, err: runErr}
	}()

	reachedConfiguredConcurrency, earlyOutcome, waitErr := waitForHTTPDensity(
		ctx,
		int64(config.HTTPVUsers),
		config.HTTPDensityTimeout,
		&peak,
		runDone,
	)
	if waitErr != nil {
		close(releaseRequests)
		eng.Stop()
		if earlyOutcome == nil {
			<-runDone
		}
		return httpDensityProbe{}, waitErr
	}
	if reachedConfiguredConcurrency && config.HTTPDensityHold > 0 {
		timer := time.NewTimer(config.HTTPDensityHold)
		var holdErr error
		select {
		case <-timer.C:
		case <-ctx.Done():
			holdErr = ctx.Err()
		}
		timer.Stop()
		if holdErr != nil {
			close(releaseRequests)
			eng.Stop()
			if earlyOutcome == nil {
				<-runDone
			}
			return httpDensityProbe{}, holdErr
		}
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	resources := resourceCollector.Collect()
	peakRequests := peak.Load()
	timeToPeak := time.Duration(0)
	if peakAt := peakAtUnixNano.Load(); peakAt > 0 {
		timeToPeak = time.Unix(0, peakAt).Sub(started)
	}
	runtimeGoroutineDelta := runtime.NumGoroutine() - baselineGoroutines
	close(releaseRequests)
	var outcome engineOutcome
	if earlyOutcome != nil {
		outcome = *earlyOutcome
	} else {
		outcome = <-runDone
	}
	if outcome.err != nil {
		return httpDensityProbe{}, outcome.err
	}
	if outcome.result == nil {
		return httpDensityProbe{}, fmt.Errorf("HTTP density engine returned no result")
	}
	if received.Load() == 0 || peakRequests == 0 {
		return httpDensityProbe{}, fmt.Errorf("HTTP density engine completed without reaching the deterministic local target")
	}
	stackDelta := nonNegativeDelta(after.StackInuse, before.StackInuse)
	heapDelta := nonNegativeDelta(after.HeapAlloc, before.HeapAlloc)
	totalDelta := nonNegativeDelta(after.TotalAlloc, before.TotalAlloc)
	stackPerRequest := float64(0)
	if peakRequests > 0 {
		stackPerRequest = float64(stackDelta) / float64(peakRequests)
	}
	probe := httpDensityProbe{
		ConfiguredVUsers:         config.HTTPVUsers,
		PeakConcurrentRequests:   peakRequests,
		RequestsReceived:         received.Load(),
		SuccessfulTransactions:   successfulTransactionCount(outcome.result),
		FailedTransactions:       outcome.result.TotalErrors,
		TimeToPeak:               timeToPeak,
		RuntimeGoroutineDelta:    max(0, runtimeGoroutineDelta),
		StackInUseDeltaBytes:     stackDelta,
		HeapAllocDeltaBytes:      heapDelta,
		TotalAllocDeltaBytes:     totalDelta,
		StackBytesPerPeakRequest: stackPerRequest,
	}
	if action := outcome.result.FinalSnapshot.ByAction["loopback"]; action != nil {
		probe.ActionSamples = action.Count
		probe.ActionErrors = action.Errors
		probe.ActionP95Milliseconds = float64(action.P95.Nanoseconds()) / float64(time.Millisecond)
		probe.ActionP99Milliseconds = float64(action.P99.Nanoseconds()) / float64(time.Millisecond)
	}
	if telemetry := outcome.result.FinalSnapshot.Telemetry; telemetry != nil {
		probe.MetricsDroppedResults = telemetry.DroppedResults
	}
	if resources != nil {
		probe.ProcessCPUPercent = resources.CPUProcessPct
		probe.ProcessRSSMegabytes = resources.MemProcessMb
		probe.OpenFDCount = resources.OpenFDCount
		probe.ThreadCount = resources.ThreadCount
		probe.HeapInUseMegabytes = resources.HeapInUseMB
		probe.GCCountDelta = resources.GCCountDelta
		probe.GCPauseMilliseconds = resources.GCPauseMs
	}
	return probe, nil
}

func successfulTransactionCount(result *engine.RunResult) int64 {
	if result == nil {
		return 0
	}
	// The engine collector already records successful and failed business
	// transactions in separate counters. Subtracting TotalErrors here would
	// double-count failures and under-report successful transactions.
	return result.TotalTransactions
}

func waitForHTTPDensity(
	ctx context.Context,
	target int64,
	timeout time.Duration,
	peak *atomic.Int64,
	runDone <-chan engineOutcome,
) (reached bool, earlyOutcome *engineOutcome, err error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		if peak.Load() >= target {
			return true, nil, nil
		}
		select {
		case <-ctx.Done():
			return false, nil, ctx.Err()
		case <-deadline.C:
			return false, nil, nil
		case outcome := <-runDone:
			return false, &outcome, nil
		case <-poll.C:
		}
	}
}

func nonNegativeDelta(after, before uint64) uint64 {
	if after <= before {
		return 0
	}
	return after - before
}

func httpDensityReleaseGrace(config Config) time.Duration {
	return max(time.Second, config.HTTPDensityHold+time.Second)
}

type rampProbeState struct {
	target      int64
	initialized atomic.Int64
	mu          sync.Mutex
	completedAt time.Time
}

func (state *rampProbeState) initializedOne() {
	if state.initialized.Add(1) != state.target {
		return
	}
	state.mu.Lock()
	state.completedAt = time.Now()
	state.mu.Unlock()
}

func (state *rampProbeState) completion() time.Time {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.completedAt
}

type syntheticRampProtocol struct {
	state *rampProbeState
}

func (protocolInstance *syntheticRampProtocol) Name() string { return syntheticRampProtocolName }
func (protocolInstance *syntheticRampProtocol) Init(context.Context, map[string]any) error {
	if protocolInstance.state == nil {
		return fmt.Errorf("synthetic ramp probe state is unavailable")
	}
	protocolInstance.state.initializedOne()
	return nil
}
func (*syntheticRampProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{
		ActionName: action.Name,
		StartTime:  time.Now(),
		Duration:   time.Nanosecond,
		Success:    true,
	}, nil
}
func (*syntheticRampProtocol) Teardown(context.Context) error    { return nil }
func (*syntheticRampProtocol) HealthCheck(context.Context) error { return nil }

var (
	rampProtocolRegisterOnce sync.Once
	rampRunMu                sync.Mutex
	activeRampState          atomic.Pointer[rampProbeState]
)

func registerSyntheticRampProtocol() {
	rampProtocolRegisterOnce.Do(func() {
		protocol.Register(syntheticRampProtocolName, func() protocol.Protocol {
			return &syntheticRampProtocol{state: activeRampState.Load()}
		})
	})
}

type rampProbe struct {
	VUsersPerSecond []float64
}

func measureSyntheticRamp(ctx context.Context, config Config) (rampProbe, error) {
	registerSyntheticRampProtocol()
	rampRunMu.Lock()
	defer rampRunMu.Unlock()

	result := rampProbe{VUsersPerSecond: make([]float64, 0, config.RampSamples)}
	for range config.RampSamples {
		if err := ctx.Err(); err != nil {
			return rampProbe{}, err
		}
		state := &rampProbeState{target: int64(config.RampVUsers)}
		activeRampState.Store(state)
		script := &dsl.Script{
			ID:       "performance-ramp-probe",
			Name:     "Performance ramp probe",
			Version:  "1",
			Protocol: syntheticRampProtocolName,
			Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{
				Iterations:       1,
				WorkloadModel:    "closed",
				AutoTransactions: "none",
			}},
			Actions: []dsl.Action{{Name: "noop", Type: "noop"}},
		}
		eng, err := engine.New(engine.Config{
			Script:   script,
			VUsers:   config.RampVUsers,
			Duration: config.MetricsProbeTimeout,
		})
		if err != nil {
			activeRampState.Store(nil)
			return rampProbe{}, err
		}
		started := time.Now()
		_, runErr := eng.Run(ctx)
		activeRampState.Store(nil)
		if runErr != nil {
			return rampProbe{}, runErr
		}
		completedAt := state.completion()
		if completedAt.IsZero() || state.initialized.Load() != int64(config.RampVUsers) {
			return rampProbe{}, fmt.Errorf("initialized %d of %d synthetic VUsers", state.initialized.Load(), config.RampVUsers)
		}
		elapsed := completedAt.Sub(started)
		if elapsed <= 0 {
			return rampProbe{}, fmt.Errorf("synthetic ramp duration was not positive")
		}
		result.VUsersPerSecond = append(result.VUsersPerSecond, float64(config.RampVUsers)/elapsed.Seconds())
	}
	return result, nil
}

type metricsLatencyProbe struct {
	Milliseconds []float64
}

func measureMetricsPublicationLatency(ctx context.Context, config Config) (metricsLatencyProbe, error) {
	result := metricsLatencyProbe{Milliseconds: make([]float64, 0, config.MetricsLatencySamples)}
	for sampleIndex := range config.MetricsLatencySamples {
		collector := engine.NewMetricsCollector(128)
		collector.Start(config.MetricsInterval)
		actionName := fmt.Sprintf("latency-probe-%d", sampleIndex)
		recordedAt := time.Now()
		collector.Record(&protocol.Result{
			ActionName: actionName,
			StartTime:  recordedAt,
			Duration:   time.Millisecond,
			Success:    true,
		})

		deadline := time.NewTimer(config.MetricsProbeTimeout)
		pollEvery := min(config.MetricsInterval/20, 5*time.Millisecond)
		if pollEvery < 100*time.Microsecond {
			pollEvery = 100 * time.Microsecond
		}
		poll := time.NewTicker(pollEvery)
		observed := false
		for !observed {
			select {
			case <-ctx.Done():
				poll.Stop()
				deadline.Stop()
				collector.Stop()
				return metricsLatencyProbe{}, ctx.Err()
			case <-deadline.C:
				poll.Stop()
				collector.Stop()
				return metricsLatencyProbe{}, fmt.Errorf("sample %d was not published within %s", sampleIndex, config.MetricsProbeTimeout)
			case <-poll.C:
				snapshot, ok := collector.LatestSnapshot()
				if !ok || snapshot.ByAction[actionName] == nil || snapshot.ByAction[actionName].Count == 0 {
					continue
				}
				result.Milliseconds = append(result.Milliseconds, float64(time.Since(recordedAt).Nanoseconds())/float64(time.Millisecond))
				observed = true
			}
		}
		poll.Stop()
		deadline.Stop()
		collector.Stop()
	}
	return result, nil
}

func collectBuildMetadata(statusOverride string) BuildMetadata {
	info := buildinfo.Get()
	metadata := BuildMetadata{
		Version:        info.Version,
		DisplayVersion: info.DisplayVersion,
		Commit:         info.Commit,
		Revision:       info.Revision,
		BuildTime:      info.BuildTime,
		GoVersion:      info.GoVersion,
	}
	if debugInfo, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range debugInfo.Settings {
			if setting.Key == "vcs.modified" {
				metadata.VCSStatusAvailable = true
				metadata.VCSModified = setting.Value == "true"
				break
			}
		}
	}
	switch statusOverride {
	case "clean":
		metadata.VCSStatusAvailable = true
		metadata.VCSModified = false
	case "modified":
		metadata.VCSStatusAvailable = true
		metadata.VCSModified = true
	case "unknown":
		metadata.VCSStatusAvailable = false
		metadata.VCSModified = false
	}
	return metadata
}

func collectEnvironmentMetadata(config Config) EnvironmentMetadata {
	metadata := EnvironmentMetadata{
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		LogicalCPUs: runtime.NumCPU(),
		GOMAXPROCS:  runtime.GOMAXPROCS(0),
		HostClass:   strings.TrimSpace(config.HostClass),
		RunnerImage: strings.TrimSpace(config.RunnerImage),
	}
	if count, err := cpu.Counts(false); err == nil {
		metadata.PhysicalCores = count
	}
	if info, err := cpu.Info(); err == nil {
		for _, item := range info {
			if model := strings.TrimSpace(item.ModelName); model != "" {
				metadata.CPUModel = model
				break
			}
		}
	}
	if memory, err := mem.VirtualMemory(); err == nil {
		metadata.PhysicalMemoryBytes = memory.Total
	}
	if kernel, err := host.KernelVersion(); err == nil {
		metadata.KernelVersion = kernel
	}
	return metadata
}

func buildMeasurements(harness harnessProbe, density httpDensityProbe, ramp rampProbe, metrics metricsLatencyProbe) ([]Measurement, error) {
	const targetSource = "AGENTS.md Performance Targets"

	harnessStats, err := NewStatistics("ns/transaction", harness.NanosecondsPerTransaction)
	if err != nil {
		return nil, err
	}
	harnessComparison, err := NewTargetComparison(harnessStats, "p95", Target{
		Name:     "Harness overhead per transaction",
		Operator: "<",
		Value:    50_000,
		Unit:     "ns/transaction",
		Source:   targetSource,
		Scope:    "Complete harness overhead for one transaction",
	}, "The probe measures MetricsCollector.Record only, not full action resolution, redaction, protocol dispatch, result handling, or telemetry delivery.")
	if err != nil {
		return nil, err
	}

	densityStats, err := NewStatistics("count", []float64{float64(density.PeakConcurrentRequests)})
	if err != nil {
		return nil, err
	}
	densityComparison, err := NewTargetComparison(densityStats, "max", Target{
		Name:     "HTTP VUsers per pod",
		Operator: ">=",
		Value:    10_000,
		Unit:     "count",
		Source:   targetSource,
		Scope:    "10,000 executing HTTP VUsers in a 4-vCPU/8-GB generator pod",
	}, "The probe uses real HTTP VUsers and a deterministic loopback target, but the local process is not constrained to the documented 4-vCPU/8-GB generator pod class and does not represent a customer network or script.")
	if err != nil {
		return nil, err
	}

	rampStats, err := NewStatistics("vusers/second", ramp.VUsersPerSecond)
	if err != nil {
		return nil, err
	}
	rampComparison, err := NewTargetComparison(rampStats, "median", Target{
		Name:     "Ramp-up rate",
		Operator: ">=",
		Value:    500,
		Unit:     "vusers/second",
		Source:   targetSource,
		Scope:    "500 VUsers/second/pod under a production protocol workload",
	}, "The engine path is real, but the protocol is an in-process no-op and the process is not running in a resource-constrained generator pod.")
	if err != nil {
		return nil, err
	}

	metricsStats, err := NewStatistics("milliseconds", metrics.Milliseconds)
	if err != nil {
		return nil, err
	}
	metricsComparison, err := NewTargetComparison(metricsStats, "p95", Target{
		Name:     "Metrics latency",
		Operator: "<",
		Value:    2_000,
		Unit:     "milliseconds",
		Source:   targetSource,
		Scope:    "Generator collection through controller delivery and dashboard visibility",
	}, "The probe stops at MetricsCollector snapshot publication; it excludes gRPC transport, controller aggregation, WebSocket delivery, and dashboard rendering.")
	if err != nil {
		return nil, err
	}

	return []Measurement{
		{
			ID:          "harness.metrics_record.duration",
			Description: "Per-call cost of the production MetricsCollector.Record hot path with a concurrent drain.",
			Scope:       "Generator metrics ingestion component",
			Method:      "Repeated wall-clock samples around successful MetricsCollector.Record calls after a warm-up run.",
			Observed:    harnessStats,
			SupportingMetrics: []ScalarMetric{
				{Name: "dropped_results", Unit: "count", Value: float64(harness.Dropped)},
			},
			TargetComparison: harnessComparison,
			Limitations: []string{
				"Does not include protocol execution or network I/O.",
				"Does not measure the complete per-transaction harness path.",
			},
		},
		{
			ID:          "vuser.http_density_local.capacity",
			Description: "Peak concurrent requests from real Engine HTTP VUsers held at a deterministic loopback target.",
			Scope:       "Local engine, production HTTP plugin, runtime goroutines, HTTP clients, and loopback sockets",
			Method:      "Starts one GET iteration per VUser, gates deterministic loopback responses while concurrency and runtime memory are sampled, then releases all requests.",
			Observed:    densityStats,
			SupportingMetrics: []ScalarMetric{
				{Name: "configured_vusers", Unit: "count", Value: float64(density.ConfiguredVUsers)},
				{Name: "requests_received", Unit: "count", Value: float64(density.RequestsReceived)},
				{Name: "successful_transactions", Unit: "count", Value: float64(density.SuccessfulTransactions)},
				{Name: "failed_transactions", Unit: "count", Value: float64(density.FailedTransactions)},
				{Name: "action_samples", Unit: "count", Value: float64(density.ActionSamples)},
				{Name: "action_errors", Unit: "count", Value: float64(density.ActionErrors)},
				{Name: "action_p95", Unit: "milliseconds", Value: density.ActionP95Milliseconds},
				{Name: "action_p99", Unit: "milliseconds", Value: density.ActionP99Milliseconds},
				{Name: "metrics_dropped_results", Unit: "count", Value: float64(density.MetricsDroppedResults)},
				{Name: "time_to_peak", Unit: "milliseconds", Value: float64(density.TimeToPeak.Nanoseconds()) / float64(time.Millisecond)},
				{Name: "runtime_goroutine_delta", Unit: "count", Value: float64(density.RuntimeGoroutineDelta)},
				{Name: "stack_inuse_delta", Unit: "bytes", Value: float64(density.StackInUseDeltaBytes)},
				{Name: "heap_alloc_delta", Unit: "bytes", Value: float64(density.HeapAllocDeltaBytes)},
				{Name: "total_alloc_delta", Unit: "bytes", Value: float64(density.TotalAllocDeltaBytes)},
				{Name: "stack_bytes_per_peak_request", Unit: "bytes/request", Value: density.StackBytesPerPeakRequest},
				{Name: "process_cpu", Unit: "percent", Value: density.ProcessCPUPercent},
				{Name: "process_rss", Unit: "megabytes", Value: density.ProcessRSSMegabytes},
				{Name: "open_file_descriptors", Unit: "count", Value: float64(density.OpenFDCount)},
				{Name: "threads", Unit: "count", Value: float64(density.ThreadCount)},
				{Name: "heap_inuse", Unit: "megabytes", Value: density.HeapInUseMegabytes},
				{Name: "gc_count_delta", Unit: "count", Value: float64(density.GCCountDelta)},
				{Name: "gc_pause", Unit: "milliseconds", Value: density.GCPauseMilliseconds},
			},
			TargetComparison: densityComparison,
			Limitations: []string{
				"Loopback requests exclude production network latency, TLS, authentication, data parameterization, and response complexity.",
				"No 4-vCPU/8-GB cgroup or Kubernetes pod boundary is applied by this local harness.",
			},
		},
		{
			ID:               "ramp.synthetic_engine.start_rate",
			Description:      "Rate at which Engine.Run initializes configured closed-model VUsers using a no-op protocol.",
			Scope:            "Engine VUser construction, protocol factory/init, and goroutine scheduling",
			Method:           "Repeated zero-ramp Engine runs with one no-op iteration; timing ends when the last protocol instance completes Init.",
			Observed:         rampStats,
			TargetComparison: rampComparison,
			Limitations: []string{
				"The synthetic protocol performs no socket, TLS, authentication, or target setup.",
				"The local host is not a generator pod with the documented resource limits.",
			},
		},
		{
			ID:               "metrics.collector.publication_latency",
			Description:      "Delay from MetricsCollector.Record to visibility in LatestSnapshot.",
			Scope:            "In-process engine collection and snapshot publication",
			Method:           "Records one uniquely named result per fresh collector and polls until the periodic snapshot contains it.",
			Observed:         metricsStats,
			TargetComparison: metricsComparison,
			Limitations: []string{
				"Excludes distributed transport and controller merge queues.",
				"Excludes WebSocket transmission, browser scheduling, and UI render time.",
			},
		},
	}, nil
}
