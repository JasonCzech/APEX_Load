package perfevidence

// VUser memory-footprint benchmark.
//
// Measures the marginal (per-VUser) memory cost of an idle, connected VUser for
// each protocol, independent of payload size. Every script performs one
// minimal action to establish its protocol state (connection, session, pool)
// and then parks in a long think time, so no request or response body is in
// flight when memory is sampled.
//
// Method:
//   - Loopback targets run in a separate child process, so server-side
//     per-connection memory is not attributed to the VUsers.
//   - Each (protocol, VUser count) sample runs in a fresh process, so heap
//     freed by an earlier run cannot be reused and hide growth.
//   - The per-VUser figure is the least-squares slope across the VUser counts,
//     which cancels fixed engine/process overhead.
//
// Run (opt-in; not part of the normal suite):
//
//	APEXLOAD_VUSER_FOOTPRINT=1 go test ./pkg/perfevidence -run '^TestVUserFootprint$' -v -timeout 30m
//
// Optional: APEXLOAD_VUSER_FOOTPRINT_COUNTS=250,1000,2000
//           APEXLOAD_VUSER_FOOTPRINT_PROTOCOLS=noop,http,websocket
//           APEXLOAD_VUSER_FOOTPRINT_OUT=/path/results.json

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v3/process"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	_ "github.com/jasonczech/apexload/pkg/protocol/database"
	_ "github.com/jasonczech/apexload/pkg/protocol/grpc"
	_ "github.com/jasonczech/apexload/pkg/protocol/network"
	_ "github.com/jasonczech/apexload/pkg/protocol/soap"
	_ "github.com/jasonczech/apexload/pkg/protocol/websocket"
)

const (
	footprintEnableEnv    = "APEXLOAD_VUSER_FOOTPRINT"
	footprintCountsEnv    = "APEXLOAD_VUSER_FOOTPRINT_COUNTS"
	footprintProtocolsEnv = "APEXLOAD_VUSER_FOOTPRINT_PROTOCOLS"
	footprintOutEnv       = "APEXLOAD_VUSER_FOOTPRINT_OUT"
	footprintRoleEnv      = "APEXLOAD_VUSER_FOOTPRINT_ROLE"
	footprintProtocolEnv  = "APEXLOAD_VUSER_FOOTPRINT_SAMPLE_PROTOCOL"
	footprintVUsersEnv    = "APEXLOAD_VUSER_FOOTPRINT_SAMPLE_VUSERS"
	footprintTargetsEnv   = "APEXLOAD_VUSER_FOOTPRINT_TARGETS"

	footprintTargetsPrefix = "FOOTPRINT_TARGETS "
	footprintSamplePrefix  = "FOOTPRINT_SAMPLE "
	footprintActionName    = "footprint"
	footprintNoopProtocol  = "apexload-footprint-noop"
)

var footprintDefaultProtocols = []string{"noop", "http", "soap", "websocket", "grpc", "network", "database"}

type footprintTargets struct {
	HTTP string `json:"http"`
	WS   string `json:"ws"`
	GRPC string `json:"grpc"`
	TCP  string `json:"tcp"`
}

type footprintSample struct {
	Protocol       string `json:"protocol"`
	VUsers         int    `json:"vusers"`
	ActionSamples  int64  `json:"action_samples"`
	ActionErrors   int64  `json:"action_errors"`
	BaseHeapAlloc  uint64 `json:"base_heap_alloc"`
	BaseStackInuse uint64 `json:"base_stack_inuse"`
	BaseRSS        uint64 `json:"base_rss"`
	BaseGoroutines int    `json:"base_goroutines"`
	HeapAlloc      uint64 `json:"heap_alloc"`
	HeapInuse      uint64 `json:"heap_inuse"`
	StackInuse     uint64 `json:"stack_inuse"`
	Sys            uint64 `json:"sys"`
	RSS            uint64 `json:"rss"`
	Goroutines     int    `json:"goroutines"`
}

type footprintResult struct {
	Protocol            string            `json:"protocol"`
	Samples             []footprintSample `json:"samples"`
	HeapLiveKBPerVUser  float64           `json:"heap_live_kb_per_vuser"`
	StackKBPerVUser     float64           `json:"stack_kb_per_vuser"`
	GoMemoryKBPerVUser  float64           `json:"go_memory_kb_per_vuser"`
	RSSKBPerVUser       float64           `json:"rss_kb_per_vuser"`
	GoroutinesPerVUser  float64           `json:"goroutines_per_vuser"`
	GoMemoryFitRSquared float64           `json:"go_memory_fit_r_squared"`
}

// TestVUserFootprint is the driver. It starts the loopback targets, runs one
// fresh sample process per (protocol, VUser count), and fits the slopes.
func TestVUserFootprint(t *testing.T) {
	if os.Getenv(footprintRoleEnv) != "" {
		t.Skip("driver does not run inside a helper process")
	}
	if os.Getenv(footprintEnableEnv) != "1" {
		t.Skipf("set %s=1 to run the VUser memory-footprint benchmark", footprintEnableEnv)
	}
	counts := footprintCounts(t)
	protocols := footprintProtocols()

	targets, stopTargets := startFootprintTargets(t)
	defer stopTargets()
	targetsJSON, err := json.Marshal(targets)
	if err != nil {
		t.Fatal(err)
	}

	results := make([]footprintResult, 0, len(protocols))
	for _, name := range protocols {
		result := footprintResult{Protocol: name}
		for _, vusers := range counts {
			sample, err := runFootprintSample(name, vusers, string(targetsJSON))
			if err != nil {
				t.Errorf("%s @ %d VUsers: %v", name, vusers, err)
				continue
			}
			if sample.ActionErrors > 0 {
				t.Errorf("%s @ %d VUsers: %d action errors; footprint would not reflect connected VUsers", name, vusers, sample.ActionErrors)
			}
			t.Logf("%s @ %d VUsers: heap=%s stack=%s rss=%s goroutines=%d", name, vusers,
				footprintBytes(sample.HeapAlloc), footprintBytes(sample.StackInuse), footprintBytes(sample.RSS), sample.Goroutines)
			result.Samples = append(result.Samples, sample)
		}
		if len(result.Samples) < 2 {
			continue
		}
		result.HeapLiveKBPerVUser, _ = footprintSlope(result.Samples, func(s footprintSample) float64 { return float64(s.HeapAlloc) / 1024 })
		result.StackKBPerVUser, _ = footprintSlope(result.Samples, func(s footprintSample) float64 { return float64(s.StackInuse) / 1024 })
		result.GoMemoryKBPerVUser, result.GoMemoryFitRSquared = footprintSlope(result.Samples, func(s footprintSample) float64 {
			return float64(s.HeapAlloc+s.StackInuse) / 1024
		})
		result.RSSKBPerVUser, _ = footprintSlope(result.Samples, func(s footprintSample) float64 { return float64(s.RSS) / 1024 })
		result.GoroutinesPerVUser, _ = footprintSlope(result.Samples, func(s footprintSample) float64 { return float64(s.Goroutines) })
		results = append(results, result)
	}

	var table strings.Builder
	table.WriteString("\n| Protocol | Live heap KB/VUser | Stack KB/VUser | Go memory KB/VUser | RSS KB/VUser | Goroutines/VUser | R² |\n")
	table.WriteString("|---|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range results {
		fmt.Fprintf(&table, "| %s | %.1f | %.1f | %.1f | %.1f | %.2f | %.4f |\n",
			r.Protocol, r.HeapLiveKBPerVUser, r.StackKBPerVUser, r.GoMemoryKBPerVUser, r.RSSKBPerVUser, r.GoroutinesPerVUser, r.GoMemoryFitRSquared)
	}
	t.Log(table.String())

	if out := os.Getenv(footprintOutEnv); out != "" {
		data, err := json.MarshalIndent(map[string]any{
			"go_version": runtime.Version(),
			"goos":       runtime.GOOS,
			"goarch":     runtime.GOARCH,
			"num_cpu":    runtime.NumCPU(),
			"counts":     counts,
			"results":    results,
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestVUserFootprintHelper is the child-process entry point for both the
// loopback targets and individual samples. It is a no-op in normal runs.
func TestVUserFootprintHelper(t *testing.T) {
	switch os.Getenv(footprintRoleEnv) {
	case "targets":
		serveFootprintTargets(t)
	case "sample":
		measureFootprintSample(t)
	default:
		t.Skip("helper process only")
	}
}

func footprintCounts(t *testing.T) []int {
	raw := strings.TrimSpace(os.Getenv(footprintCountsEnv))
	if raw == "" {
		return []int{250, 1000, 2000}
	}
	var counts []int
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n <= 0 {
			t.Fatalf("%s: invalid count %q", footprintCountsEnv, part)
		}
		counts = append(counts, n)
	}
	if len(counts) < 2 {
		t.Fatalf("%s needs at least two counts to fit a slope", footprintCountsEnv)
	}
	return counts
}

func footprintProtocols() []string {
	raw := strings.TrimSpace(os.Getenv(footprintProtocolsEnv))
	if raw == "" {
		return footprintDefaultProtocols
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func footprintHelperCommand(ctx context.Context, env ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestVUserFootprintHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

func startFootprintTargets(t *testing.T) (footprintTargets, func()) {
	t.Helper()
	cmd := footprintHelperCommand(context.Background(), footprintRoleEnv+"=targets")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stop := func() {
		// Closing stdin tells the child to exit; kill is the backstop.
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}

	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, footprintTargetsPrefix) {
				lines <- strings.TrimPrefix(line, footprintTargetsPrefix)
				break
			}
		}
		_, _ = io.Copy(io.Discard, stdout)
	}()
	select {
	case line := <-lines:
		var targets footprintTargets
		if err := json.Unmarshal([]byte(line), &targets); err != nil {
			stop()
			t.Fatalf("decode targets: %v", err)
		}
		return targets, stop
	case <-time.After(30 * time.Second):
		stop()
		t.Fatal("targets process did not report its addresses")
	}
	return footprintTargets{}, stop
}

func runFootprintSample(name string, vusers int, targetsJSON string) (footprintSample, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := footprintHelperCommand(ctx,
		footprintRoleEnv+"=sample",
		footprintProtocolEnv+"="+name,
		footprintVUsersEnv+"="+strconv.Itoa(vusers),
		footprintTargetsEnv+"="+targetsJSON,
	)
	output, runErr := cmd.CombinedOutput()
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, footprintSamplePrefix) {
			var sample footprintSample
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, footprintSamplePrefix)), &sample); err != nil {
				return footprintSample{}, fmt.Errorf("decode sample: %w", err)
			}
			return sample, nil
		}
	}
	tail := string(output)
	if len(tail) > 4000 {
		tail = tail[len(tail)-4000:]
	}
	return footprintSample{}, fmt.Errorf("sample process produced no measurement (err=%v):\n%s", runErr, tail)
}

// serveFootprintTargets runs minimal loopback servers until stdin closes.
// Responses are constant and tiny so payload size does not influence results.
func serveFootprintTargets(t *testing.T) {
	upgrader := gorillawebsocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/soap", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte(`<Envelope><Body><ok/></Body></Envelope>`))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: mux}
	go func() { _ = httpServer.Serve(httpListener) }()

	grpcListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := googlegrpc.NewServer()
	healthpb.RegisterHealthServer(grpcServer, health.NewServer())
	reflection.Register(grpcServer)
	go func() { _ = grpcServer.Serve(grpcListener) }()

	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := tcpListener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 16)
				_, _ = conn.Read(buf)
				_, _ = conn.Write([]byte("pong"))
			}(conn)
		}
	}()

	targets := footprintTargets{
		HTTP: "http://" + httpListener.Addr().String(),
		WS:   "ws://" + httpListener.Addr().String(),
		GRPC: grpcListener.Addr().String(),
		TCP:  tcpListener.Addr().String(),
	}
	data, _ := json.Marshal(targets)
	fmt.Println(footprintTargetsPrefix + string(data))

	_, _ = io.Copy(io.Discard, os.Stdin)
	grpcServer.Stop()
	_ = httpServer.Close()
	_ = tcpListener.Close()
}

type footprintNoop struct{}

func (footprintNoop) Name() string                               { return footprintNoopProtocol }
func (footprintNoop) Init(context.Context, map[string]any) error { return nil }
func (footprintNoop) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{ActionName: action.Name, StartTime: time.Now(), Duration: time.Nanosecond, Success: true}, nil
}
func (footprintNoop) Teardown(context.Context) error    { return nil }
func (footprintNoop) HealthCheck(context.Context) error { return nil }

func footprintScript(t *testing.T, name string, targets footprintTargets) *dsl.Script {
	t.Helper()
	// Park every VUser after its single action; the run is stopped long
	// before this elapses.
	park := dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 30 * 60 * 1000}
	action := dsl.Action{Name: footprintActionName, ThinkTimeAfter: true, ThinkTime: park}
	script := &dsl.Script{
		ID:      "vuser-footprint-" + name,
		Name:    "VUser footprint " + name,
		Version: "1",
		Config: dsl.ScriptConfig{
			RunLogic: dsl.RunLogicConfig{Iterations: 1, WorkloadModel: "closed"},
		},
	}
	switch name {
	case "noop":
		script.Protocol = footprintNoopProtocol
		action.Type = "noop"
	case "http":
		script.Protocol = "http"
		script.Config.BaseURL = targets.HTTP
		action.Type, action.Method, action.Target = http.MethodGet, http.MethodGet, "/ok"
	case "soap":
		script.Protocol = "soap"
		script.Config.BaseURL = targets.HTTP
		action.Type, action.Method, action.Target = http.MethodPost, http.MethodPost, "/soap"
		action.Body = `<Envelope><Body><Ping/></Body></Envelope>`
		action.SOAP = &dsl.SOAPActionConfig{Action: "urn:footprint#Ping"}
	case "websocket":
		script.Protocol = "websocket"
		script.Config.BaseURL = targets.WS
		action.Type, action.Target = "ws_connect", "/ws"
	case "grpc":
		script.Protocol = "grpc"
		script.Config.BaseURL = targets.GRPC
		script.Config.GRPC = dsl.GRPCConfig{PlainText: true, UseReflection: true}
		action.Type, action.Target, action.Body = "grpc_unary", "grpc.health.v1.Health/Check", `{}`
	case "network":
		script.Protocol = "network"
		action.Type, action.Target, action.Body = "tcp_request", targets.TCP, "ping"
		action.Params = map[string]any{"read": true}
	case "database":
		script.Protocol = "database"
		script.Config.Database = dsl.DatabaseConfig{Driver: "sqlite", DSN: ":memory:"}
		action.Type, action.Target = "db_query", "SELECT 1"
	default:
		t.Fatalf("unknown footprint protocol %q", name)
	}
	script.Actions = []dsl.Action{action}
	return script
}

func footprintRSS() uint64 {
	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return 0
	}
	info, err := proc.MemoryInfo()
	if err != nil || info == nil {
		return 0
	}
	return info.RSS
}

func measureFootprintSample(t *testing.T) {
	name := os.Getenv(footprintProtocolEnv)
	vusers, err := strconv.Atoi(os.Getenv(footprintVUsersEnv))
	if err != nil || vusers <= 0 {
		t.Fatalf("invalid %s", footprintVUsersEnv)
	}
	var targets footprintTargets
	if err := json.Unmarshal([]byte(os.Getenv(footprintTargetsEnv)), &targets); err != nil {
		t.Fatalf("decode targets: %v", err)
	}
	if name == "noop" {
		protocol.Register(footprintNoopProtocol, func() protocol.Protocol { return footprintNoop{} })
	}
	script := footprintScript(t, name, targets)

	runtime.GC()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	sample := footprintSample{
		Protocol:       name,
		VUsers:         vusers,
		BaseHeapAlloc:  before.HeapAlloc,
		BaseStackInuse: before.StackInuse,
		BaseRSS:        footprintRSS(),
		BaseGoroutines: runtime.NumGoroutine(),
	}

	eng, err := engine.New(engine.Config{Script: script, VUsers: vusers, Duration: 30 * time.Minute})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() {
		_, runErr := eng.Run(runCtx)
		runDone <- runErr
	}()

	deadline := time.Now().Add(3 * time.Minute)
	for {
		if snapshot, ok := eng.LatestSnapshot(); ok {
			if action := snapshot.ByAction[footprintActionName]; action != nil && action.Count >= int64(vusers) {
				sample.ActionSamples, sample.ActionErrors = action.Count, action.Errors
				break
			}
		}
		select {
		case runErr := <-runDone:
			t.Fatalf("engine exited before all VUsers were parked: %v", runErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d parked VUsers", vusers)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Let post-response goroutines and pooled connections settle.
	time.Sleep(2 * time.Second)
	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	sample.HeapAlloc = after.HeapAlloc
	sample.HeapInuse = after.HeapInuse
	sample.StackInuse = after.StackInuse
	sample.Sys = after.Sys
	sample.RSS = footprintRSS()
	sample.Goroutines = runtime.NumGoroutine()

	data, _ := json.Marshal(sample)
	fmt.Println(footprintSamplePrefix + string(data))

	eng.Stop()
	select {
	case <-runDone:
	case <-time.After(30 * time.Second):
	}
}

// footprintSlope returns the least-squares slope of metric against VUsers and
// the fit's coefficient of determination.
func footprintSlope(samples []footprintSample, metric func(footprintSample) float64) (float64, float64) {
	n := float64(len(samples))
	var sumX, sumY, sumXY, sumXX float64
	for _, s := range samples {
		x, y := float64(s.VUsers), metric(s)
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	denominator := n*sumXX - sumX*sumX
	if denominator == 0 {
		return 0, 0
	}
	slope := (n*sumXY - sumX*sumY) / denominator
	intercept := (sumY - slope*sumX) / n
	var ssRes, ssTot float64
	meanY := sumY / n
	for _, s := range samples {
		y := metric(s)
		predicted := intercept + slope*float64(s.VUsers)
		ssRes += (y - predicted) * (y - predicted)
		ssTot += (y - meanY) * (y - meanY)
	}
	if ssTot == 0 {
		return slope, 1
	}
	return slope, math.Max(0, 1-ssRes/ssTot)
}

func footprintBytes(b uint64) string {
	return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
}
