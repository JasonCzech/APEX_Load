// Command java-shared-jvm-bench runs one java_vuser group through the real
// engine against a Java VUser runner sidecar on this host's loopback, with a
// built-in order service as the system under test, and prints a JSON summary.
// With -script it runs an APEX java_vuser DSL script, such as an imported one,
// instead of the built-in fixture.
// run.sh starts it inside the runner's container (as a generator shares the
// runner's pod) so it can also sample the runner's JVMs and cgroup.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

type summary struct {
	Sharing          string                   `json:"sharing"`
	VUsers           int                      `json:"vusers"`
	Placements       []engine.JavaVMPlacement `json:"placements,omitempty"`
	RunSeconds       float64                  `json:"run_seconds"`
	AllInitSeconds   float64                  `json:"all_vusers_initialized_seconds,omitempty"`
	MeasuredFrom     float64                  `json:"latency_measured_after_seconds"`
	Iterations       int64                    `json:"iterations"`
	Transactions     int64                    `json:"transactions"`
	Errors           int64                    `json:"errors"`
	FailedTx         int64                    `json:"failed_transactions"` // business transactions that ended failed, warm-up included
	TransactionsPerS float64                  `json:"transactions_per_second"`
	AvgMS            float64                  `json:"avg_ms"`
	P50MS            float64                  `json:"p50_ms"`
	P90MS            float64                  `json:"p90_ms"`
	P95MS            float64                  `json:"p95_ms"`
	P99MS            float64                  `json:"p99_ms"`
	PeakJVMs         int                      `json:"peak_jvms"`
	PeakJVMPSSMB     float64                  `json:"peak_jvm_pss_mb"`
	PeakContainerMB  float64                  `json:"peak_container_mb"`
	AvgCPUCores      float64                  `json:"avg_cpu_cores"`
	SUTRequests      int64                    `json:"sut_requests"`
	Error            string                   `json:"error,omitempty"`
}

func main() {
	sharing := flag.String("sharing", "shared", "dedicated or shared")
	vusers := flag.Int("vusers", 25, "VUsers in the group")
	perJVM := flag.Int("vusers-per-jvm", 0, "VUsers per shared JVM (0 keeps the script's value, else the runner's)")
	duration := flag.Duration("duration", time.Minute, "steady run length")
	rampUp := flag.Duration("ramp-up", 0, "ramp-up period")
	target := flag.String("target", "HeavyActions", "Actions class")
	classpath := flag.String("classpath", "", "comma-separated jar paths on the runner, target jar first (with -script, replaces the script's classpath)")
	vmArgs := flag.String("vm-args", "", "comma-separated script VM parameters (with -script, replaces the script's)")
	paceMS := flag.Int("pace-ms", 0, "fixed iteration interval in ms (0 runs iterations back to back, or keeps a -script script's pacing)")
	warmup := flag.Duration("warmup", 0, "leave transactions that start in this first part of the run out of the latency figures")
	waitReady := flag.Duration("wait-ready", 30*time.Second, "how long to wait for the runner")
	scriptPath := flag.String("script", "", "run this APEX java_vuser DSL script (JSON) instead of the built-in fixture")
	flag.Parse()
	targetSet := false
	flag.Visit(func(f *flag.Flag) { targetSet = targetSet || f.Name == "target" })

	out := summary{Sharing: *sharing, VUsers: *vusers}
	var err error
	switch {
	case *sharing != dsl.JavaVuserJVMSharingShared && *sharing != dsl.JavaVuserJVMSharingDedicated:
		err = errors.New("sharing must be dedicated or shared")
	case *scriptPath != "" && targetSet:
		err = errors.New("-target names the built-in fixture's class; a -script script names its own")
	case *scriptPath != "":
		err = runScriptFile(&out, *scriptPath, *sharing, *vusers, *perJVM, *duration, *rampUp, *warmup, split(*classpath), split(*vmArgs), *paceMS, *waitReady)
	default:
		err = run(&out, *sharing, *vusers, *perJVM, *duration, *rampUp, *warmup, *target, split(*classpath), split(*vmArgs), *paceMS, *waitReady)
	}
	if err != nil {
		out.Error = err.Error()
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(out)
	if err != nil {
		os.Exit(1)
	}
}

func split(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func run(out *summary, sharing string, vusers, perJVM int, duration, rampUp, warmup time.Duration, target string, classpath, vmArgs []string, paceMS int, waitReady time.Duration) error {
	if len(classpath) == 0 {
		return errors.New("classpath is required")
	}
	if err := waitForRunner(os.Getenv("APEXLOAD_JAVA_VUSER_RUNNER_URL"), waitReady); err != nil {
		return err
	}
	endpoint, requests, stopSUT, err := startOrderService()
	if err != nil {
		return err
	}
	defer stopSUT()

	call := func(method string) []dsl.Action {
		return []dsl.Action{{Name: method, Type: "java_vuser_run", Target: target, Params: map[string]any{"method": method, "lr_parameters": map[string]any{"Endpoint": endpoint}}}}
	}
	script := &dsl.Script{ID: "java-shared-jvm-bench", Name: "Java shared JVM bench", Protocol: "java_vuser", Init: call("init"), Actions: call("action"), Teardown: call("end")}
	configure(script, sharing, perJVM, classpath, vmArgs, paceMS)
	err = execute(out, script, vusers, duration, rampUp, warmup)
	out.SUTRequests = requests()
	return err
}

// runScriptFile runs the APEX java_vuser DSL script in a JSON file, such as
// an imported script, with the flags' sharing and overrides.
func runScriptFile(out *summary, path, sharing string, vusers, perJVM int, duration, rampUp, warmup time.Duration, classpath, vmArgs []string, paceMS int, waitReady time.Duration) error {
	script, err := dsl.ParseFile(path)
	if err != nil {
		return err
	}
	configure(script, sharing, perJVM, classpath, vmArgs, paceMS)
	if err := waitForRunner(os.Getenv("APEXLOAD_JAVA_VUSER_RUNNER_URL"), waitReady); err != nil {
		return err
	}
	return execute(out, script, vusers, duration, rampUp, warmup)
}

// configure applies the run's JVM sharing and any classpath, VM parameters
// and pacing the flags give to script, keeping the script's own otherwise.
func configure(script *dsl.Script, sharing string, perJVM int, classpath, vmArgs []string, paceMS int) {
	java := &script.Config.JavaVuser
	java.JVMSharing = sharing
	if sharing != dsl.JavaVuserJVMSharingShared {
		java.VUsersPerJVM = 0 // only valid with shared JVMs
	} else if perJVM > 0 {
		java.VUsersPerJVM = perJVM
	}
	if len(classpath) > 0 {
		// Uploaded jar bindings name entries of the replaced classpath.
		java.Classpath, java.ClasspathAssets = classpath, nil
	}
	if len(vmArgs) > 0 {
		java.AdditionalVMParameters = vmArgs
	}
	if paceMS > 0 {
		script.Config.Pacing = dsl.PacingConfig{Type: "fixed_interval", IntervalMs: paceMS}
	}
}

// execute runs script through the engine and fills out with its outcome,
// whole-run latency of successful business transactions, and the sampled
// JVM memory and CPU.
func execute(out *summary, script *dsl.Script, vusers int, duration, rampUp, warmup time.Duration) error {
	// Every successful business transaction's duration, for whole-run
	// percentiles (the final metrics snapshot covers only its last window).
	var mu sync.Mutex
	var start time.Time
	var latencies []time.Duration
	var failed int64
	var initialized int
	var allInitialized time.Time
	e, err := engine.New(engine.Config{Script: script, VUsers: vusers, Duration: duration, RampUp: rampUp, TerminationMode: dsl.TerminationDuration,
		TimingObserver: func(timing engine.ResultTiming) {
			if !timing.BusinessTransaction {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if !timing.Success {
				failed++
				return
			}
			if !timing.StartTime.Before(start.Add(warmup)) {
				latencies = append(latencies, timing.Duration)
			}
		},
		InitializationObserver: func(int) {
			mu.Lock()
			initialized++
			if initialized == vusers {
				allInitialized = time.Now()
			}
			mu.Unlock()
		},
	})
	if err != nil {
		return err
	}

	sampler := startSampler()
	mu.Lock()
	start = time.Now()
	mu.Unlock()
	result, runErr := e.Run(context.Background())
	out.RunSeconds = time.Since(start).Seconds()
	sampler.stop(out)
	out.MeasuredFrom = warmup.Seconds()
	mu.Lock()
	defer mu.Unlock()
	out.FailedTx = failed
	if !allInitialized.IsZero() {
		out.AllInitSeconds = allInitialized.Sub(start).Seconds()
	}
	if len(latencies) > 0 {
		slices.Sort(latencies)
		var total time.Duration
		for _, latency := range latencies {
			total += latency
		}
		ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
		at := func(q float64) float64 { return ms(latencies[min(len(latencies)-1, int(q*float64(len(latencies))))]) }
		out.AvgMS, out.P50MS, out.P90MS, out.P95MS, out.P99MS = ms(total/time.Duration(len(latencies))), at(0.50), at(0.90), at(0.95), at(0.99)
	}
	if result != nil {
		out.Placements = result.JavaVMPlacements
		out.Iterations = result.CompletedIterations
		out.Transactions = result.TotalTransactions
		out.Errors = result.TotalErrors
		if result.Duration > 0 {
			out.TransactionsPerS = float64(result.TotalTransactions) / result.Duration.Seconds()
		}
	}
	return runErr
}

func waitForRunner(base string, limit time.Duration) error {
	if base == "" {
		return errors.New("APEXLOAD_JAVA_VUSER_RUNNER_URL is required")
	}
	deadline := time.Now().Add(limit)
	for {
		response, err := http.Get(strings.TrimRight(base, "/") + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("runner not ready at %s", base)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// startOrderService serves the order flow the evidence fixtures expect:
// GET /order returns order 17 with a 4 KiB payload, POST /order answers ok.
func startOrderService() (string, func() int64, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, nil, err
	}
	order, _ := json.Marshal(map[string]any{"id": 17, "payload": strings.Repeat("x", 4096)})
	var mu sync.Mutex
	var count int64
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		if r.URL.Path != "/order" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(order)
	})}
	go func() { _ = server.Serve(listener) }()
	requests := func() int64 {
		mu.Lock()
		defer mu.Unlock()
		return count
	}
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
	return "http://" + listener.Addr().String(), requests, stop, nil
}

// sampler records the peak JVM count and proportional set size of every java
// process in this PID namespace, the container cgroup's peak sampled memory
// (memory.peak would span earlier runs in the same container) and its average
// CPU use.
type sampler struct {
	done     chan struct{}
	finished chan struct{}
	start    time.Time
	cpuStart int64

	jvms      int
	pssBytes  int64
	container int64
}

func startSampler() *sampler {
	s := &sampler{done: make(chan struct{}), finished: make(chan struct{}), start: time.Now(), cpuStart: cgroupCPUMicros()}
	go func() {
		defer close(s.finished)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			s.sample()
			select {
			case <-s.done:
				return
			case <-ticker.C:
			}
		}
	}()
	return s
}

func (s *sampler) sample() {
	jvms, pss := javaProcessesPSS()
	s.jvms = max(s.jvms, jvms)
	s.pssBytes = max(s.pssBytes, pss)
	if current, ok := readInt("/sys/fs/cgroup/memory.current"); ok {
		s.container = max(s.container, current)
	}
}

func (s *sampler) stop(out *summary) {
	close(s.done)
	<-s.finished
	s.sample()
	out.PeakJVMs = s.jvms
	out.PeakJVMPSSMB = float64(s.pssBytes) / (1 << 20)
	out.PeakContainerMB = float64(s.container) / (1 << 20)
	if elapsed := time.Since(s.start).Seconds(); elapsed > 0 && s.cpuStart >= 0 {
		if used := cgroupCPUMicros(); used >= s.cpuStart {
			out.AvgCPUCores = float64(used-s.cpuStart) / 1e6 / elapsed
		}
	}
}

func javaProcessesPSS() (int, int64) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, 0
	}
	count, total := 0, int64(0)
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil || strings.TrimSpace(string(comm)) != "java" {
			continue
		}
		file, err := os.Open(filepath.Join("/proc", entry.Name(), "smaps_rollup"))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 && fields[0] == "Pss:" {
				if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					total += kb << 10
					count++
				}
				break
			}
		}
		_ = file.Close()
	}
	return count, total
}

func cgroupCPUMicros() int64 {
	data, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "usage_usec "); ok {
			if micros, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				return micros
			}
		}
	}
	return -1
}

func readInt(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	return value, err == nil
}
