package perfevidence

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
)

type JavaRunConfig struct {
	ScriptPath, CgroupPath, Workload, Profile           string
	RuntimeSHA256, Environment, ResourceProfile, Target string
	RunnerPID, VUsers, Repetitions                      int
	Warmup, Measurement, Soak                           time.Duration
}

// JavaPilotRunConfig selects the explicitly approved single-group Linux pilot.
// The manifest is the same operator-controlled file loaded by the local runner.
type JavaPilotRunConfig struct {
	JavaRunConfig
	ManifestPath       string
	PreparationTimeout time.Duration
}

type javaPilotRun struct {
	manifest    string
	preparation time.Duration
	options     engine.JavaPooledPilotOptions
}

func RunJavaPooled(ctx context.Context, cfg JavaPilotRunConfig) (JavaEvidence, error) {
	if cfg.ManifestPath == "" || cfg.PreparationTimeout < time.Millisecond || cfg.PreparationTimeout > time.Hour {
		return JavaEvidence{}, errors.New("invalid Java pilot configuration")
	}
	return runJavaEvidence(ctx, cfg.JavaRunConfig, &javaPilotRun{manifest: cfg.ManifestPath, preparation: cfg.PreparationTimeout})
}

// RunJavaDedicated uses the ordinary engine and authenticated sidecar. It never
// enables a runner, bypasses containment, compiles user code, or changes VM flags.
// The operator must place runner and benchmark in one exclusive cgroup subtree;
// the SUT must be outside it. This stage produces baseline evidence, not a pass.
func RunJavaDedicated(ctx context.Context, cfg JavaRunConfig) (JavaEvidence, error) {
	return runJavaEvidence(ctx, cfg, nil)
}

func runJavaEvidence(ctx context.Context, cfg JavaRunConfig, pilot *javaPilotRun) (report JavaEvidence, runErr error) {
	if runtime.GOOS != "linux" {
		return report, errors.New("Java PSS evidence requires Linux with delegated cgroup v2")
	}
	if cfg.Repetitions < 1 || cfg.Repetitions > 5 || cfg.VUsers < 1 || cfg.VUsers > 10000 || cfg.RunnerPID <= 0 ||
		cfg.Warmup < time.Second || cfg.Warmup > time.Hour || cfg.Measurement < time.Second || cfg.Measurement > time.Hour ||
		!filepath.IsAbs(cfg.CgroupPath) || cfg.Soak != 0 && (cfg.Soak < time.Hour || cfg.Soak > 2*time.Hour || cfg.VUsers < 3 || cfg.Repetitions != 5) {
		return report, errors.New("invalid Java benchmark bounds")
	}
	script, err := dsl.ParseFile(cfg.ScriptPath)
	if err != nil {
		return report, errors.New("Java benchmark script is invalid")
	}
	if script.Protocol != "java_vuser" || len(script.Init) != 1 || len(script.Actions) != 1 || len(script.Teardown) != 1 ||
		script.Config.RunLogic.Iterations != 0 || script.Config.RunLogic.GlobalIterations != 0 || script.Config.RunLogic.MaxTransactions != 0 ||
		script.Config.RunLogic.WorkloadModel == "open" || script.Config.Pacing.Type != "fixed_interval" || script.Config.Pacing.IntervalMs <= 0 {
		return report, errors.New("Java baseline requires one persistent init/action/end and duration-based fixed-interval pacing")
	}
	for index, a := range []dsl.Action{script.Init[0], script.Actions[0], script.Teardown[0]} {
		if a.Type != "java_vuser_run" || a.Params["method"] != []string{"init", "action", "end"}[index] {
			return report, errors.New("Java baseline requires explicit persistent lifecycle methods")
		}
	}
	operator := config.LoadJavaVuserOperatorConfig()
	if !operator.Enabled || operator.RunnerMode != config.JavaVuserRunnerModeSidecar {
		return report, errors.New("Java baseline requires an operator-enabled sidecar")
	}
	client, err := javasidecar.NewClientWithToken(operator.RunnerURL, operator.AuthToken, nil)
	if err != nil {
		return report, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	capabilities, err := client.Capabilities(probeCtx)
	cancel()
	if err != nil || !capabilities.Healthy || !protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		!protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureJavaVuserSessionFrameTokenV1) {
		return report, errors.New("Java baseline requires healthy persistent containment and authenticated session frames")
	}
	if pilot == nil && cfg.VUsers > capabilities.MaxProcesses {
		return report, errors.New("Java baseline exceeds dedicated process capacity")
	}
	if pilot != nil {
		for _, feature := range []string{javasidecar.FeaturePooledReservationsV1, javasidecar.FeaturePooledPreparationV1, javasidecar.FeaturePooledCommitV1, javasidecar.FeaturePooledInvocationV1, javasidecar.FeaturePooledDiagnosticsV1} {
			if !protocol.RuntimeFeaturesSatisfy(capabilities.Features, feature) {
				return report, errors.New("Java pilot requires explicit pooled capability negotiation")
			}
		}
		settings, loadErr := javasidecar.LoadPooledPilotManifest(ctx, pilot.manifest, capabilities.MaxProcesses)
		if loadErr != nil {
			return report, loadErr
		}
		actionTimeout := time.Duration(script.Config.JavaVuser.ActionTimeoutMs) * time.Millisecond
		if actionTimeout == 0 {
			actionTimeout = javasidecar.DefaultExecutionTimeout
		}
		pilot.options = engine.JavaPooledPilotOptions{CollectDiagnostics: true, Client: client, Run: javasidecar.PooledClientOptions{ExecutionID: "evidence-validation", GroupID: "evidence", Decision: settings.Decision}, ScriptSHA256: settings.ScriptSHA256, PreparationTimeout: pilot.preparation, ActionTimeout: actionTimeout}
		// Validate the approved DSL and effective capacity before claiming an owner.
		d := settings.Decision
		hosts := (cfg.VUsers + d.SlotsPerHost - 1) / d.SlotsPerHost
		if hosts > d.MaxHosts || int64(hosts) > d.MaxMemoryBytes/d.HostMemoryBytes {
			return report, errors.New("Java pilot exceeds prepared host capacity")
		}
		if _, err = engine.NewJavaPooledPilot(engine.Config{Script: script, VUsers: cfg.VUsers, Duration: cfg.Warmup + cfg.Measurement + 2*time.Minute}, pilot.options); err != nil {
			return report, errors.New("Java pilot script or profile is not eligible")
		}
	}
	if _, err = readJavaMemory(cfg.CgroupPath, "/proc", os.Getpid(), cfg.RunnerPID); err != nil {
		return report, err
	}
	// Separate the workload identity from the exact tuning profile. VM tokens
	// are not emitted; the operator runtime identity binds JDK and all artifacts.
	identityScript := *script
	identityScript.Config = script.Config
	identityScript.Config.JavaVuser.AdditionalVMParameters = nil
	data, err := json.Marshal(identityScript)
	if err != nil {
		return report, errors.New("cannot identify Java workload")
	}
	digest := sha256.Sum256(data)
	configurationBytes, _ := json.Marshal(script)
	configurationDigest := sha256.Sum256(configurationBytes)
	report = JavaEvidence{Schema: JavaEvidenceSchema, GeneratedAt: time.Now().UTC(), Workload: cfg.Workload, Profile: cfg.Profile, Mode: "dedicated",
		ConfigurationSHA256: hex.EncodeToString(configurationDigest[:]),
		Identity: JavaEvidenceIdentity{WorkloadSHA256: hex.EncodeToString(digest[:]), RuntimeSHA256: cfg.RuntimeSHA256, Environment: cfg.Environment,
			Backend: "delegated_linux_sidecar", ResourceProfile: cfg.ResourceProfile, Target: cfg.Target, VUsers: cfg.VUsers,
			OfferedTPS: float64(cfg.VUsers) * 1000 / float64(script.Config.Pacing.IntervalMs), WarmupSeconds: cfg.Warmup.Seconds(), MeasurementSeconds: cfg.Measurement.Seconds(),
			MemoryMethod: "aggregate_process_pss_bytes", AccountingScope: "exclusive_engine_runner_cgroup_subtree"},
		Limitations: []string{"dedicated_baseline_only", "operator_attests_exclusive_cgroup_and_runtime_identity", "no_soak_or_production_qualification"}}
	if err = report.Validate(); err != nil {
		return report, err
	}
	if pilot != nil {
		configureJavaPilotReport(&report, pilot)
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return report, errors.New("Java pilot owner identity unavailable")
		}
		claimCtx, cancelClaim := context.WithTimeout(ctx, javasidecar.RunnerOwnerLeaseTTL)
		_, err = client.ClaimOwner(claimCtx, "java-evidence-"+hex.EncodeToString(nonce[:]))
		cancelClaim()
		if err != nil {
			return report, errors.New("Java pilot owner claim failed")
		}
		lifetime, cancelLifetime := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- keepJavaPilotOwner(lifetime, client, cancelLifetime) }()
		defer finishJavaPilotOwner(cancelLifetime, done, &report, &runErr)
		ctx = lifetime
		pilot.options.Run.ExecutionID = "java-evidence-" + hex.EncodeToString(nonce[:])
	}
	for i := 0; i < cfg.Repetitions; i++ {
		trial := javaPilotTrial(pilot, i)
		sample, runErr := runJavaSampleWithPilot(ctx, cfg, script, trial)
		report.Samples = append(report.Samples, sample)
		if runErr != nil {
			return report, runErr
		}
	}
	if cfg.Soak > 0 {
		soak, err := runJavaSoak(ctx, cfg, script, javaPilotTrial(pilot, cfg.Repetitions))
		report.Soak = &soak
		report.SoakSeconds = soak.ActiveSeconds
		report.SoakPassed = err == nil && soak.passed(cfg.VUsers)
		if err != nil {
			return report, err
		}
		report.Limitations = append(report.Limitations, "synthetic_soak_only_no_production_qualification")
		kept := report.Limitations[:0]
		for _, l := range report.Limitations {
			if l != "no_soak_or_production_qualification" {
				kept = append(kept, l)
			}
		}
		report.Limitations = kept
	}
	return report, report.Validate()
}

func runJavaSampleWithPilot(ctx context.Context, cfg JavaRunConfig, script *dsl.Script, pilot *javaPilotRun) (JavaEvidenceSample, error) {
	sample := JavaEvidenceSample{Complete: true, FunctionalChecksPassed: true}
	initialMemory, err := readJavaMemory(cfg.CgroupPath, "/proc", os.Getpid(), cfg.RunnerPID)
	if err != nil {
		sample.incomplete("initial_memory_unavailable")
		return sample, err
	}
	var mu sync.Mutex
	var start, end time.Time
	initialized := make(map[int]bool)
	var durations []float64
	observe := func(r engine.ResultTiming) {
		mu.Lock()
		defer mu.Unlock()
		recordJavaTiming(&sample, &durations, start, end, r)
	}
	engineCfg := engine.Config{Script: script, VUsers: cfg.VUsers, Duration: cfg.Warmup + cfg.Measurement + 2*time.Minute, TimingObserver: observe,
		InitializationObserver: func(id int) { mu.Lock(); initialized[id] = true; mu.Unlock() },
	}
	eng, err := newJavaEvidenceEngine(engineCfg, pilot)
	if err != nil {
		sample.incomplete("engine_setup_failed")
		return sample, errors.New("Java evidence engine configuration failed")
	}
	type outcome struct {
		r   *engine.RunResult
		err error
	}
	done := make(chan outcome, 1)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { r, e := eng.Run(runCtx); done <- outcome{r, e} }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	startupDeadline := time.Now().Add(2 * time.Minute)
	if pilot != nil {
		startupDeadline = startupDeadline.Add(pilot.preparation)
	}
	var warmupEnd time.Time
	var cpuStart float64
	measurementStopped := false
	for {
		select {
		case out := <-done:
			sample.HostDiagnostics = eng.JavaPooledDiagnostics()
			if pilot != nil && !completeJavaDiagnostics(sample.HostDiagnostics, cfg.VUsers) {
				sample.incomplete("host_diagnostics_incomplete")
			}
			finalMemory, memoryErr := readJavaMemory(cfg.CgroupPath, "/proc", os.Getpid(), cfg.RunnerPID)
			mu.Lock()
			if memoryErr != nil || finalMemory.limitEvents < initialMemory.limitEvents {
				sample.incomplete("final_memory_unavailable")
			} else {
				sample.ResourceFailures = max(sample.ResourceFailures, finalMemory.limitEvents-initialMemory.limitEvents)
			}
			if out.r != nil {
				sample.DroppedResults = out.r.DroppedResults
				sample.HarnessErrors += out.r.TeardownErrors
				if out.r.TotalErrors > sample.Errors+sample.HarnessErrors {
					sample.HarnessErrors += out.r.TotalErrors - sample.Errors - sample.HarnessErrors
				}
			}
			if !measurementStopped || out.err != nil {
				sample.incomplete("measurement_not_completed")
			}
			sample.LatencySamples = int64(len(durations))
			sample.TPS = float64(sample.Transactions) / cfg.Measurement.Seconds()
			sort.Float64s(durations)
			if len(durations) > 0 {
				sample.P95MS = percentile(durations, .95)
				sample.P99MS = percentile(durations, .99)
			} else {
				sample.incomplete("latency_samples_missing")
			}
			sample.FunctionalChecksPassed = sample.Errors == 0 && sample.HarnessErrors == 0
			mu.Unlock()
			if out.err != nil {
				sample.incomplete("engine_run_failed")
				return sample, errors.New("Java evidence engine run failed")
			}
			if !sample.Complete {
				return sample, errors.New("Java evidence measurement was incomplete")
			}
			return sample, nil
		case <-ctx.Done():
			cancel()
			out := <-done
			sample.HostDiagnostics = eng.JavaPooledDiagnostics()
			_ = out
			mu.Lock()
			sample.incomplete("caller_cancelled")
			mu.Unlock()
			return sample, ctx.Err()
		case <-ticker.C:
			if measurementStopped {
				continue
			}
			reading, readErr := readJavaMemory(cfg.CgroupPath, "/proc", os.Getpid(), cfg.RunnerPID)
			now := time.Now()
			mu.Lock()
			if readErr != nil {
				sample.incomplete(javaMemoryFailureCode(readErr))
			} else {
				if reading.limitEvents < initialMemory.limitEvents {
					sample.incomplete("resource_counter_regressed")
				} else {
					sample.ResourceFailures = max(sample.ResourceFailures, reading.limitEvents-initialMemory.limitEvents)
				}
				if start.IsZero() {
					sample.StartupPeakBytes = max(sample.StartupPeakBytes, reading.pss)
				}
				sample.CgroupPeakBytes = max(sample.CgroupPeakBytes, reading.cgroup)
			}
			snap, ok := eng.LatestSnapshot()
			if warmupEnd.IsZero() && len(initialized) == cfg.VUsers && ok && snap.ActiveVUsers == int64(cfg.VUsers) {
				warmupEnd = now.Add(cfg.Warmup)
			}
			if start.IsZero() && !warmupEnd.IsZero() && !now.Before(warmupEnd) {
				start = now
				end = now.Add(cfg.Measurement)
				cpuStart = reading.cpuSeconds
			}
			if !start.IsZero() && now.Before(end) {
				if readErr == nil {
					sample.MemoryBytes = append(sample.MemoryBytes, reading.pss)
				}
				if !ok || snap.ActiveVUsers != int64(cfg.VUsers) {
					sample.incomplete("active_vuser_mismatch")
				}
			}
			if !end.IsZero() && !now.Before(end) {
				sample.CPUSeconds = max(0, reading.cpuSeconds-cpuStart)
				measurementStopped = true
				eng.GracefulStop()
			}
			if warmupEnd.IsZero() && now.After(startupDeadline) {
				sample.incomplete("initialization_timeout")
				measurementStopped = true
				cancel()
			}
			mu.Unlock()
		}
	}
}

// Throughput counts completions in [start,end); latency follows starts in that
// window through graceful drain. Never censor slow requests at the end boundary.
// The caller serializes access and waits for the metrics consumer to drain.
func recordJavaTiming(sample *JavaEvidenceSample, durations *[]float64, start, end time.Time, r engine.ResultTiming) {
	if start.IsZero() || end.IsZero() {
		return
	}
	completed := r.StartTime.Add(r.Duration)
	startedInWindow := !r.StartTime.Before(start) && r.StartTime.Before(end)
	completedInWindow := !completed.Before(start) && completed.Before(end)
	if !startedInWindow && !completedInWindow {
		return
	}
	if !r.Success {
		if r.BusinessTransaction {
			sample.Errors++
		} else {
			sample.HarnessErrors++
		}
	}
	if !r.BusinessTransaction {
		return
	}
	if completedInWindow {
		sample.Transactions++
	}
	if startedInWindow {
		if len(*durations) >= 1_000_000 || r.Duration < 0 {
			sample.incomplete("latency_sample_invalid")
			return
		}
		*durations = append(*durations, float64(r.Duration)/float64(time.Millisecond))
	}
}

// Keep the original owner alive between repetitions as well as during samples.
// Renewal cannot reacquire an epoch after takeover; failure cancels all evidence.
func keepJavaPilotOwner(ctx context.Context, client *javasidecar.Client, cancelRun context.CancelFunc) error {
	ticker := time.NewTicker(javasidecar.RunnerOwnerLeaseRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			callCtx, cancel := context.WithTimeout(ctx, javasidecar.RunnerOwnerLeaseRenewInterval)
			err := client.RenewOwner(callCtx)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				cancelRun()
				return errors.New("Java pilot owner renewal failed")
			}
		}
	}
}

func newJavaEvidenceEngine(cfg engine.Config, pilot *javaPilotRun) (*engine.Engine, error) {
	if pilot == nil {
		return engine.New(cfg)
	}
	return engine.NewJavaPooledPilot(cfg, pilot.options)
}
func javaPilotTrial(pilot *javaPilotRun, index int) *javaPilotRun {
	if pilot == nil {
		return nil
	}
	snapshot := *pilot
	snapshot.options.Run.ExecutionID += "-" + strconv.Itoa(index+1)
	return &snapshot
}
func configureJavaPilotReport(report *JavaEvidence, pilot *javaPilotRun) {
	report.Mode = "pooled"
	report.Limitations = []string{"experimental_pooled_pilot_only", "operator_attests_exclusive_cgroup_and_runtime_identity", "no_soak_or_production_qualification"}
	binding, _ := json.Marshal(struct {
		Script   string
		Decision javasidecar.PooledControlDecision
	}{pilot.options.ScriptSHA256, pilot.options.Run.Decision})
	sum := sha256.Sum256(binding)
	report.ConfigurationSHA256 = hex.EncodeToString(sum[:])
}

func finishJavaPilotOwner(cancel context.CancelFunc, done <-chan error, report *JavaEvidence, runErr *error) {
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		*runErr = errors.Join(*runErr, err)
		report.SoakPassed = false
		if report.Soak != nil {
			report.Soak.Complete = false
		}
		if len(report.Samples) > 0 {
			report.Samples[len(report.Samples)-1].incomplete("owner_lease_lost")
		}
	}
}
