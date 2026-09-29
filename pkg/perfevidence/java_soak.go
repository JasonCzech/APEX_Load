package perfevidence

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

const javaSoakSchedule = "staged-admission-permanent-closure/v1"

type JavaSoakUser struct {
	StartSeconds       float64 `json:"start_seconds"`
	StopSeconds        float64 `json:"stop_seconds"`
	InitializedSeconds float64 `json:"initialized_seconds"`
	ClosedSeconds      float64 `json:"closed_seconds"`
	CleanupConfirmed   bool    `json:"cleanup_confirmed"`
}

type JavaSoakEvidence struct {
	Schedule         string                    `json:"schedule"`
	RequestedSeconds float64                   `json:"requested_seconds"`
	WarmupSeconds    float64                   `json:"warmup_seconds"`
	ActiveSeconds    float64                   `json:"active_seconds"`
	Users            []JavaSoakUser            `json:"users"`
	Iterations       int64                     `json:"iterations"`
	Transactions     int64                     `json:"transactions"`
	Errors           int64                     `json:"errors"`
	DroppedResults   int64                     `json:"dropped_results"`
	ResourceFailures int64                     `json:"resource_failures"`
	MemoryBytes      []float64                 `json:"memory_bytes"`
	CgroupPeakBytes  float64                   `json:"cgroup_peak_bytes"`
	Complete         bool                      `json:"complete"`
	HostDiagnostics  []engine.JavaHostEvidence `json:"host_diagnostics,omitempty"`
}

// Reserve the full population once. One cohort starts immediately, another at
// 1/4 and the last at 1/2. Odd VUsers close at 3/4, while even survivors run to
// the end. No closed identity is reused and no active Java state is migrated.
func javaSoakLifetimes(users int, warmup, duration time.Duration) []engine.VUserLifetime {
	result := make([]engine.VUserLifetime, users)
	for i := range result {
		start := time.Duration(0)
		if i > 0 {
			start = warmup + duration*time.Duration((i*3/users))/4
		}
		stop := warmup + duration
		if i%2 == 1 {
			stop = warmup + duration*3/4
		}
		result[i] = engine.VUserLifetime{StartAfter: start, StopAfter: stop}
	}
	return result
}

func runJavaSoak(ctx context.Context, cfg JavaRunConfig, script *dsl.Script, pilot *javaPilotRun) (JavaSoakEvidence, error) {
	return collectJavaSoak(ctx, cfg, script, pilot, func() (javaMemoryReading, error) {
		return readJavaMemory(cfg.CgroupPath, "/proc", os.Getpid(), cfg.RunnerPID)
	})
}

// The injected reader is a filesystem seam for deterministic collector tests;
// the engine, lifecycle scheduling and protocol execution remain real.
func collectJavaSoak(ctx context.Context, cfg JavaRunConfig, script *dsl.Script, pilot *javaPilotRun, read func() (javaMemoryReading, error)) (JavaSoakEvidence, error) {
	result := JavaSoakEvidence{Schedule: javaSoakSchedule, RequestedSeconds: cfg.Soak.Seconds(), WarmupSeconds: cfg.Warmup.Seconds(), Complete: true}
	lifetimes := javaSoakLifetimes(cfg.VUsers, cfg.Warmup, cfg.Soak)
	for _, s := range lifetimes {
		result.Users = append(result.Users, JavaSoakUser{StartSeconds: s.StartAfter.Seconds(), StopSeconds: s.StopAfter.Seconds(), InitializedSeconds: -1, ClosedSeconds: -1})
	}
	initial, err := read()
	if err != nil {
		result.Complete = false
		return result, err
	}
	var mu sync.Mutex
	var origin time.Time
	ecfg := engine.Config{Script: script, VUsers: cfg.VUsers, Duration: cfg.Warmup + cfg.Soak + 2*time.Minute, EvidenceSchedule: lifetimes,
		EvidenceStarted: func(at time.Time) { mu.Lock(); origin = at; mu.Unlock() },
		InitializationObserver: func(id int) {
			mu.Lock()
			defer mu.Unlock()
			if result.Users[id].InitializedSeconds >= 0 {
				result.Complete = false
			}
			result.Users[id].InitializedSeconds = time.Since(origin).Seconds()
		},
		CompletionObserver: func(id int, ok bool) {
			mu.Lock()
			defer mu.Unlock()
			if result.Users[id].ClosedSeconds >= 0 {
				result.Complete = false
			}
			result.Users[id].ClosedSeconds = time.Since(origin).Seconds()
			result.Users[id].CleanupConfirmed = ok
		},
		TimingObserver: func(r engine.ResultTiming) {
			mu.Lock()
			defer mu.Unlock()
			if r.BusinessTransaction {
				result.Transactions++
			}
			if !r.Success {
				result.Errors++
			}
		},
	}
	eng, err := newJavaEvidenceEngine(ecfg, pilot)
	if err != nil {
		result.Complete = false
		return result, errors.New("Java soak engine configuration failed")
	}
	type outcome struct {
		r   *engine.RunResult
		err error
	}
	done := make(chan outcome, 1)
	go func() { r, e := eng.Run(ctx); done <- outcome{r, e} }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	sample := func() {
		reading, e := read()
		mu.Lock()
		defer mu.Unlock()
		if e != nil || reading.limitEvents < initial.limitEvents || len(result.MemoryBytes) >= 100000 {
			result.Complete = false
			return
		}
		result.ResourceFailures = max(result.ResourceFailures, reading.limitEvents-initial.limitEvents)
		result.MemoryBytes = append(result.MemoryBytes, reading.pss)
		result.CgroupPeakBytes = max(result.CgroupPeakBytes, reading.cgroup)
	}
	for {
		select {
		case <-ticker.C:
			sample()
		case out := <-done:
			sample()
			result.HostDiagnostics = eng.JavaPooledDiagnostics()
			if out.r != nil {
				result.Iterations = out.r.CompletedIterations
				result.DroppedResults = out.r.DroppedResults
				result.Errors = max(result.Errors, out.r.TotalErrors) + out.r.TeardownErrors
			}
			if out.err != nil || ctx.Err() != nil || out.r == nil {
				result.Complete = false
			}
			first, last := -1.0, 0.0
			for _, u := range result.Users {
				if u.InitializedSeconds >= 0 && (first < 0 || u.InitializedSeconds < first) {
					first = u.InitializedSeconds
				}
				last = max(last, u.ClosedSeconds)
			}
			if first >= 0 {
				result.ActiveSeconds = max(0, last-first)
			}
			if !result.passed(cfg.VUsers) || pilot != nil && !completeJavaDiagnostics(result.HostDiagnostics, cfg.VUsers) {
				return result, errors.New("Java soak did not satisfy lifecycle and evidence checks")
			}
			return result, nil
		}
	}
}

func (s JavaSoakEvidence) passed(users int) bool {
	if !s.Complete || s.Schedule != javaSoakSchedule || len(s.Users) != users || users < 3 || s.Iterations < int64(users)*2 || s.Transactions == 0 || s.Errors != 0 || s.DroppedResults != 0 || s.ResourceFailures != 0 || len(s.MemoryBytes) < 2 || s.ActiveSeconds < s.RequestedSeconds {
		return false
	}
	for _, u := range s.Users {
		// The two-second tolerance is explicit evidence policy, not silent rescheduling.
		if !u.CleanupConfirmed || u.InitializedSeconds < u.StartSeconds || u.InitializedSeconds > u.StartSeconds+2 || u.ClosedSeconds < u.StopSeconds || u.ClosedSeconds > u.StopSeconds+2 {
			return false
		}
	}
	return true
}
