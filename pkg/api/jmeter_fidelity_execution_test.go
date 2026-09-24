package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestIterationScenarioAdmissionAndPersistence(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := iterationCappedScript("iteration-script", "Iterations", 200)
	// The scenario mode must preserve script-authored iterations even without a script-level mode.
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Iteration scenario", TerminationMode: "iterations", Groups: []CreateGroupEntry{{Name: "users", ScriptID: script.ID, VUsers: 50, RampUp: "1s"}}})
	if err != nil {
		t.Fatal(err)
	}
	if scenario.Duration != "" || scenario.TerminationMode != "iterations" {
		t.Fatalf("scenario=%+v", scenario)
	}
	run, err := server.runner.CreateTest(CreateTestRequest{Name: "Iteration test", ScenarioID: scenario.ID})
	if err != nil {
		t.Fatal(err)
	}
	if run.Duration != 0 || run.TerminationMode != "iterations" || run.groupSpecs[0].Script.Config.RunLogic.Iterations != 200 || run.TotalVUsers != 50 {
		t.Fatalf("run=%+v", run)
	}
	if err := server.runner.AdjustDuration(run.ID, time.Minute); err == nil {
		t.Fatal("duration change allowed")
	}
	if err := server.runner.ScaleVUsers(run.ID, "users", 60); err == nil {
		t.Fatal("scaling allowed")
	}
	if _, err := server.runner.CreateTest(CreateTestRequest{Name: "Bad zero", Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}}}); err == nil {
		t.Fatal("implicit zero duration admitted")
	}
	if _, err := server.runner.CreateScenarioSchedule(scenario.ID, CreateScenarioScheduleRequest{Name: "bad", CronExpr: "0 * * * *"}); err == nil {
		t.Fatal("schedule admitted")
	}
}

func TestDeclaredCSVExportUsesFinalizedSamples(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	fallback := "NOT_FOUND"
	declaration := RunArchiveCSVExport{Group: "users", Export: dsl.CSVExport{ID: "trace", FileName: "traceId-results.csv", ActionName: "sample", Columns: []dsl.CSVExportColumn{{Name: "traceId", Extraction: "idpTraceId", DefaultValue: &fallback}, {Name: "latencyMs", Metric: "latency_ms"}}}}
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "trace-export", Name: "Trace"}, RunArchiveProvenance{CSVExports: []RunArchiveCSVExport{declaration}})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []*protocol.Result{
		{Group: "users", ActionName: "sample", Success: true, Extractions: map[string]string{"idpTraceId": "a,b"}, TTFB: 1234 * time.Microsecond},
		{Group: "users", ActionName: "sample", Success: false},
		{Group: "users", ActionName: "sample", BusinessTransaction: true},
	} {
		if err := writer.RecordResult(result); err != nil {
			t.Fatal(err)
		}
	}
	record, err := writer.Finalize("COMPLETE", time.Now(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.CSVExports) != 1 || manifest.CSVExports[0].Status != "complete" || manifest.CSVExports[0].Rows != 2 {
		t.Fatalf("export=%+v", manifest.CSVExports)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, "traceId-results.csv"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || strings.Join(rows[0], ",") != "traceId,latencyMs" || rows[1][0] != "a,b" || rows[1][1] != "1" || rows[2][0] != "NOT_FOUND" || rows[2][1] != "0" {
		t.Fatalf("rows=%v", rows)
	}
	for _, tc := range []struct {
		name      string
		truncated bool
		count     int64
	}{{"count", false, 5}, {"retention", true, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			event := TransactionEvent{Group: "users", ActionName: "sample", ExtractionsIncomplete: tc.truncated}
			b, _ := json.Marshal(event)
			if err := os.WriteFile(filepath.Join(root, "transactions.jsonl"), append(b, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			m := &RunArchiveManifest{TransactionsFile: "transactions.jsonl", TransactionCount: tc.count}
			if _, err := writeDeclaredCSVExport(context.Background(), root, m, declaration, &strings.Builder{}); err == nil {
				t.Fatal("partial export accepted")
			}
		})
	}
}

func TestIterationLaunchWindowHasNoInventedDeadline(t *testing.T) {
	start := time.Now().UTC()
	w := launchWindowForTest(&ManagedTest{TerminationMode: "iterations"}, start)
	payload, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if !w.OpenEnded || w.DurationSeconds != 0 || strings.Contains(string(payload), "end_at") {
		t.Fatalf("window=%s", payload)
	}
	if !w.EndAt.After(start.AddDate(10, 0, 0)) {
		t.Fatal("capacity reservation ended before the run")
	}
}

func TestCSVExportRejectsFailedWritesAndMissingShards(t *testing.T) {
	root := t.TempDir()
	export := RunArchiveCSVExport{Group: "users", Export: dsl.CSVExport{ID: "trace", FileName: "traceId-results.csv", ActionName: "sample", Columns: []dsl.CSVExportColumn{{Name: "latencyMs", Metric: "latency_ms"}}}}
	data, _ := json.Marshal(TransactionEvent{Group: "users", ActionName: "sample"})
	if err := os.WriteFile(filepath.Join(root, "transactions.jsonl"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := &RunArchiveManifest{CompletedAt: time.Now(), TransactionsFile: "transactions.jsonl", TransactionCount: 1, Provenance: RunArchiveProvenance{CSVExports: []RunArchiveCSVExport{export}}}
	if _, err := writeDeclaredCSVExport(context.Background(), root, manifest, export, errorCSVWriter{}); err == nil {
		t.Fatal("write failure reported success")
	}
	manifest.CSVExportIncomplete = "generator shard unavailable"
	materializeDeclaredCSVExports(root, manifest)
	if len(manifest.CSVExports) != 1 || manifest.CSVExports[0].Status != "incomplete" {
		t.Fatalf("manifest=%+v", manifest.CSVExports)
	}
	if _, err := os.Stat(filepath.Join(root, export.Export.FileName)); !os.IsNotExist(err) {
		t.Fatalf("partial export published: %v", err)
	}
	bounded := &csvExportBudgetWriter{destination: &strings.Builder{}, remaining: 1}
	if _, err := bounded.Write([]byte("too large")); err == nil {
		t.Fatal("export byte budget ignored")
	}
}

type errorCSVWriter struct{}

func (errorCSVWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("disk full") }

func TestCSVExportConcurrentArchiveAndDistributedMerge(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	declaration := RunArchiveCSVExport{Group: "users", Export: dsl.CSVExport{ID: "trace", FileName: "traceId-results.csv", ActionName: "sample", Columns: []dsl.CSVExportColumn{{Name: "traceId", Extraction: "trace"}, {Name: "latencyMs", Metric: "latency_ms"}}}}
	var bundles []remoteArchiveBundle
	for shard := 0; shard < 2; shard++ {
		writer, err := newRunArchiveWriter(&ManagedTest{ID: fmt.Sprintf("shard-%d", shard)}, RunArchiveProvenance{})
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for user := 0; user < 4; user++ {
			wg.Add(1)
			go func(user int) {
				defer wg.Done()
				for iteration := 0; iteration < 50; iteration++ {
					if err := writer.RecordResult(&protocol.Result{Group: "users", ActionName: "sample", Success: iteration%2 == 0, Extractions: map[string]string{"trace": fmt.Sprintf("%d,%d,%d", shard, user, iteration)}, TTFB: 1500 * time.Microsecond}); err != nil {
						t.Error(err)
					}
				}
			}(user)
		}
		wg.Wait()
		record, err := writer.Finalize("COMPLETE", time.Now(), "", false, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := loadRunArchiveManifestFromRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		bundles = append(bundles, remoteArchiveBundle{target: fmt.Sprintf("gen-%d", shard), manifest: *manifest, transactionsPath: filepath.Join(record.ArchivePath, manifest.TransactionsFile)})
	}
	root := t.TempDir()
	err := mergeRemoteArchiveDataset(filepath.Join(root, "transactions.jsonl"), bundles, "transactions", func(b remoteArchiveBundle) string { return b.transactionsPath }, func(b remoteArchiveBundle) int64 { return b.manifest.TransactionCount })
	if err != nil {
		t.Fatal(err)
	}
	manifest := &RunArchiveManifest{TransactionsFile: "transactions.jsonl", TransactionCount: 400}
	var first, second strings.Builder
	for _, out := range []*strings.Builder{&first, &second} {
		count, err := writeDeclaredCSVExport(context.Background(), root, manifest, declaration, out)
		if err != nil || count != 400 {
			t.Fatalf("count=%d err=%v", count, err)
		}
	}
	if first.String() != second.String() {
		t.Fatal("merged CSV nondeterministic")
	}
	rows, err := csv.NewReader(strings.NewReader(first.String())).ReadAll()
	if err != nil || len(rows) != 401 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	unique := map[string]bool{}
	for _, row := range rows[1:] {
		if unique[row[0]] {
			t.Fatal("duplicate sample")
		}
		unique[row[0]] = true
		if row[1] != "1" {
			t.Fatal("latency changed")
		}
	}
}

func TestIterationCapabilityRejectsOlderGeneratorBeforeAssignment(t *testing.T) {
	client := &startupLifecycleFakeClient{accept: true}
	client.heartbeatClient = legacyIterationHeartbeatClient(t)
	backend := newDistributedExecutionBackend([]string{"older"}, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil })
	script := &dsl.Script{ID: "finite", Name: "finite", Protocol: "http", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 2}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &ManagedTest{ID: "iteration-old-generator", Name: "finite", Status: "INITIALIZING", TerminationMode: "iterations", executionCtx: ctx, cancel: cancel, Groups: []TestGroup{{Name: "users", ScriptID: script.ID, ScriptName: script.Name, Protocol: "http", VUsers: 1, script: script}}}
	runner := NewTestRunner(1, nil, nil)
	runner.tests[run.ID] = run
	backend.Run(runner, run)
	client.mu.Lock()
	assigned := client.assigned
	client.mu.Unlock()
	if assigned != 0 || run.Status != "FAILED" || !strings.Contains(run.Error, "iteration_termination_v1") {
		t.Fatalf("assigned=%d status=%s error=%s", assigned, run.Status, run.Error)
	}
	if allowed, _ := groupAllowsGeneratorLossReshard(TestGroup{Protocol: "http", script: script}); allowed {
		t.Fatal("finite work may replay after generator loss")
	}
}

// Exercise a rolling-upgrade peer over the real heartbeat wire contract.
type legacyIterationHeartbeat struct{ *generatorrpc.Service }
type legacyIterationHeartbeatStream struct {
	pb.GeneratorControl_HeartbeatServer
}

func (s legacyIterationHeartbeatStream) Send(pong *pb.HeartbeatPong) error {
	var features []string
	for _, feature := range pong.CompatibleFeatures {
		if feature != generatorrpc.FeatureIterationTerminationV1 {
			features = append(features, feature)
		}
	}
	pong.CompatibleFeatures = features
	return s.GeneratorControl_HeartbeatServer.Send(pong)
}
func (s legacyIterationHeartbeat) Heartbeat(stream pb.GeneratorControl_HeartbeatServer) error {
	return s.Service.Heartbeat(legacyIterationHeartbeatStream{stream})
}
func legacyIterationHeartbeatClient(t *testing.T) *generatorrpc.Client {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	service := generatorrpc.NewService("older", 10)
	pb.RegisterGeneratorControlServer(server, legacyIterationHeartbeat{service})
	go func() { _ = server.Serve(listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := generatorrpc.Dial(ctx, "older", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = service.Shutdown(context.Background())
		server.Stop()
		_ = listener.Close()
	})
	return client
}

func TestCSVExportAppliesArchiveRedaction(t *testing.T) {
	root := t.TempDir()
	event := TransactionEvent{Group: "users", ActionName: "sample", Extractions: map[string]string{"client_secret": "sensitive-literal", "trace": "trace-safe"}}
	b, _ := json.Marshal(event)
	if err := os.WriteFile(filepath.Join(root, "transactions.jsonl"), append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	d := RunArchiveCSVExport{Group: "users", Export: dsl.CSVExport{ActionName: "sample", Columns: []dsl.CSVExportColumn{{Name: "secret", Extraction: "client_secret"}, {Name: "traceId", Extraction: "trace"}}}}
	var output strings.Builder
	if _, err := writeDeclaredCSVExport(context.Background(), root, &RunArchiveManifest{TransactionsFile: "transactions.jsonl", TransactionCount: 1}, d, &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "sensitive-literal") || !strings.Contains(output.String(), "trace-safe") {
		t.Fatalf("redaction changed: %s", output.String())
	}
}
