package api

import (
	"bufio"
	"context"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestDistributedRunArchivesPerGeneratorClockObservations(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer target.Close()

	// Two generators that each hold one VUser, so the run's two groups land
	// on both.
	listeners := map[string]*bufconn.Listener{}
	for _, name := range []string{"gen-a", "gen-b"} {
		listener := bufconn.Listen(1 << 20)
		server := grpc.NewServer()
		pb.RegisterGeneratorControlServer(server, generatorrpc.NewService(name, 1, generatorrpc.WithExecution()))
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(func() {
			server.Stop()
			_ = listener.Close()
		})
		listeners[name] = listener
	}
	runner := NewTestRunner(1, NewHub(), nil)
	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-b"}, 5*time.Second, func(ctx context.Context, address string) (generatorClient, error) {
		listener := listeners[address]
		return generatorrpc.Dial(ctx, address,
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
	})
	// Probe and keep observations ten times a second so a short fixture run
	// shows re-measurement across the run.
	backend.clockProbeInterval = 100 * time.Millisecond
	runner.clockObservationSpacing = 100 * time.Millisecond
	runner.setExecutionBackend(backend)
	runner.StoreScript(&dsl.Script{ID: "clock-script", Name: "Clock", Protocol: "http", Version: "1.0.0", Config: dsl.ScriptConfig{BaseURL: target.URL}, Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}}})
	run, err := runner.CreateTest(CreateTestRequest{Name: "Clock observations", Duration: "2s", Groups: []CreateGroupEntry{{Name: "browse", ScriptID: "clock-script", VUsers: 1, RampUp: "0s"}, {Name: "buy", ScriptID: "clock-script", VUsers: 1, RampUp: "0s"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.StartTest(run.ID); err != nil {
		t.Fatal(err)
	}
	var manifest *RunArchiveManifest
	deadline := time.Now().Add(15 * time.Second)
	for {
		if completed, ok := runner.GetTest(run.ID); ok && completed.Status == "COMPLETE" && completed.Archive != nil {
			if manifest, err = runner.GetRunArchiveManifest(run.ID); err == nil && manifest.ClockObservationsFile != "" {
				break
			}
		}
		if time.Now().After(deadline) {
			completed, _ := runner.GetTest(run.ID)
			t.Fatalf("no archived clock observations: status=%q error=%q manifest=%+v err=%v", completed.Status, completed.Error, manifest, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	completed, _ := runner.GetTest(run.ID)
	if len(completed.GeneratorShards) != 2 {
		t.Fatalf("run placed on %d generators, want 2: %+v", len(completed.GeneratorShards), completed.GeneratorShards)
	}
	file, err := os.Open(filepath.Join(completed.Archive.ArchivePath, manifest.ClockObservationsFile))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	bySource := map[string][]ClockObservation{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var observation ClockObservation
		if err := json.Unmarshal(scanner.Bytes(), &observation); err != nil {
			t.Fatal(err)
		}
		if observation.Schema != clockObservationSchema || observation.ClockSource != analysis.ClockSourceGenerator || observation.OffsetStatus != analysis.OffsetMeasured || math.Abs(observation.UncertaintyMs*2-observation.RoundTripMs) > 1e-5 {
			t.Fatalf("observation %+v", observation)
		}
		// One host, one clock: any offset is within the measurement's error.
		if math.Abs(observation.OffsetMs) > observation.UncertaintyMs+5 {
			t.Fatalf("same-clock offset %vms exceeds ±%vms", observation.OffsetMs, observation.UncertaintyMs)
		}
		bySource[observation.Source] = append(bySource[observation.Source], observation)
	}
	if int64(len(bySource["gen-a"])+len(bySource["gen-b"])) != manifest.ClockObservationCount {
		t.Fatalf("manifest count %d, file %v", manifest.ClockObservationCount, bySource)
	}
	for _, generator := range []string{"gen-a", "gen-b"} {
		observations := bySource[generator]
		// Re-measured while the run was live, not only at the start barrier.
		if len(observations) < 2 || !observations[len(observations)-1].ObservedAt.After(observations[0].ObservedAt) || observations[0].SourceID != generator {
			t.Fatalf("%s observations across the run: %+v", generator, observations)
		}
	}
}
