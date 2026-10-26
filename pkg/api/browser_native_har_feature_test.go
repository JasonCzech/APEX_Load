package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

type stripNativeHARServer struct {
	pb.GeneratorControlServer
}

type stripNativeHARHeartbeatStream struct {
	pb.GeneratorControl_HeartbeatServer
}

func (s stripNativeHARHeartbeatStream) Send(pong *pb.HeartbeatPong) error {
	cloned := proto.Clone(pong).(*pb.HeartbeatPong)
	cloned.CompatibleFeatures = slices.DeleteFunc(append([]string(nil), pong.GetCompatibleFeatures()...), func(f string) bool {
		return strings.EqualFold(f, generatorrpc.FeatureBrowserNativeHARV1)
	})
	return s.GeneratorControl_HeartbeatServer.Send(cloned)
}

func (s stripNativeHARServer) Heartbeat(stream pb.GeneratorControl_HeartbeatServer) error {
	return s.GeneratorControlServer.Heartbeat(stripNativeHARHeartbeatStream{GeneratorControl_HeartbeatServer: stream})
}

func startNativeHARTestPeer(t *testing.T, id string, maxVUsers int, stripNativeHAR bool) *bufconn.Listener {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	epochPath := filepath.Join(t.TempDir(), id+"-epoch.json")
	var handler pb.GeneratorControlServer = generatorrpc.NewService(
		id,
		maxVUsers,
		generatorrpc.WithExecution(),
		generatorrpc.WithExecutionEpochStatePath(epochPath),
	)
	if stripNativeHAR {
		handler = stripNativeHARServer{GeneratorControlServer: handler}
	}
	pb.RegisterGeneratorControlServer(server, handler)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener
}

func TestMixedVersionFleetRefusesRequestedNativeHARBeforePreflightOrTargetTraffic(t *testing.T) {
	if !browser.NativeHARSupported() {
		t.Skip("current host platform does not support native HAR capture")
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_DIR", t.TempDir())
	const stateJSON = `{"cookies":[{"name":"sid","value":"ok","domain":"127.0.0.1","path":"/"}]}`
	t.Setenv(engine.ProjectSecretEnvironmentName("unassigned", "STATE_1"), stateJSON)
	t.Setenv(engine.ProjectSecretEnvironmentName("unassigned", "STATE_2"), stateJSON)
	t.Setenv("APEXLOAD_GRPC_TLS_CA", "configured-ca")
	t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", strings.Repeat("t", 32))

	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>ok</body></html>"))
	}))
	defer target.Close()

	currentListener := startNativeHARTestPeer(t, "gen-current", 1, false)
	legacyListener := startNativeHARTestPeer(t, "gen-legacy", 1, true)

	dial := func(_ context.Context, addr string) (generatorClient, error) {
		var listener *bufconn.Listener
		switch addr {
		case "gen-current":
			listener = currentListener
		case "gen-legacy":
			listener = legacyListener
		default:
			return nil, context.DeadlineExceeded
		}
		conn, err := grpc.NewClient(
			"passthrough:///"+addr,
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				return listener.Dial()
			}),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			return nil, err
		}
		return generatorrpc.NewClient(conn), nil
	}

	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "native-har-mixed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	hub := NewHub()
	runner := NewTestRunner(10, hub, store)
	t.Cleanup(runner.beginShutdown)
	backend := newDistributedExecutionBackend([]string{"gen-current", "gen-legacy"}, 5*time.Second, dial)
	runner.setExecutionBackend(backend)

	sessionConfig := &dsl.BrowserSessionConfig{
		Strategy:     dsl.BrowserSessionStorageStatePool,
		PreflightURL: "/preflight",
		Identities: []dsl.BrowserIdentity{
			{Username: "u1@example.test", StorageState: "{{secret.STATE_1}}"},
			{Username: "u2@example.test", StorageState: "{{secret.STATE_2}}"},
		},
	}

	for _, policy := range []string{"always", "on_error", "on_sla_breach", "manual"} {
		t.Run("refuses_"+policy, func(t *testing.T) {
			headless := true
			scriptID := "script-har-" + policy
			runner.StoreScript(&dsl.Script{
				ID:       scriptID,
				Name:     "Browser HAR " + policy,
				Protocol: "browser",
				Version:  "1.0.0",
				Config: dsl.ScriptConfig{
					BaseURL: target.URL,
					Browser: dsl.BrowserConfig{
						Headless:   &headless,
						HARCapture: policy,
						Session:    sessionConfig,
					},
				},
				Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "/"}},
			})
			created, createErr := runner.CreateTest(CreateTestRequest{
				Name:     "Mixed fleet " + policy,
				Duration: "5s",
				Groups: []CreateGroupEntry{{
					Name:     "Observers",
					ScriptID: scriptID,
					VUsers:   2,
					RampUp:   "0s",
					Assignment: &GeneratorAssignmentSettings{
						Mode:            generatorAssignmentModeSpecificHosts,
						SelectedTargets: []string{"gen-current", "gen-legacy"},
					},
				}},
			})
			if createErr != nil {
				t.Fatalf("CreateTest: %v", createErr)
			}
			if startErr := runner.StartTest(created.ID); startErr != nil {
				t.Fatalf("StartTest: %v", startErr)
			}

			deadline := time.Now().Add(5 * time.Second)
			var terminal *ManagedTest
			for time.Now().Before(deadline) {
				snap, ok := runner.GetTest(created.ID)
				if ok && (snap.Status == "FAILED" || snap.Status == "COMPLETE" || snap.Status == "STOPPED") {
					terminal = snap
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if terminal == nil || terminal.Status != "FAILED" {
				t.Fatalf("status = %+v, want FAILED", terminal)
			}
			if !strings.Contains(terminal.Error, dsl.FeatureBrowserNativeHARV1) {
				t.Fatalf("Error = %q, want %s", terminal.Error, dsl.FeatureBrowserNativeHARV1)
			}
			if got := targetHits.Load(); got != 0 {
				t.Fatalf("target received %d HTTP requests before mixed-version refusal", got)
			}
		})
	}

	t.Run("refuses_multi_group_before_other_group_preflight", func(t *testing.T) {
		headless := true
		runner.StoreScript(&dsl.Script{
			ID:       "script-har-off-preflight",
			Name:     "Browser HAR Off With Preflight",
			Protocol: "browser",
			Version:  "1.0.0",
			Config: dsl.ScriptConfig{
				BaseURL: target.URL,
				Browser: dsl.BrowserConfig{
					Headless:   &headless,
					HARCapture: "off",
					Session:    sessionConfig,
				},
			},
			Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "/"}},
		})
		runner.StoreScript(&dsl.Script{
			ID:       "script-har-always-legacy",
			Name:     "Browser HAR Always On Legacy",
			Protocol: "browser",
			Version:  "1.0.0",
			Config: dsl.ScriptConfig{
				BaseURL: target.URL,
				Browser: dsl.BrowserConfig{
					Headless:   &headless,
					HARCapture: "always",
					Session:    sessionConfig,
				},
			},
			Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "/"}},
		})
		created, createErr := runner.CreateTest(CreateTestRequest{
			Name:     "Multi-group mixed fleet",
			Duration: "5s",
			Groups: []CreateGroupEntry{
				{
					Name:     "GroupCurrentOff",
					ScriptID: "script-har-off-preflight",
					VUsers:   1,
					RampUp:   "0s",
					Assignment: &GeneratorAssignmentSettings{
						Mode:            generatorAssignmentModeSpecificHosts,
						SelectedTargets: []string{"gen-current"},
					},
				},
				{
					Name:     "GroupLegacyHAR",
					ScriptID: "script-har-always-legacy",
					VUsers:   1,
					RampUp:   "0s",
					Assignment: &GeneratorAssignmentSettings{
						Mode:            generatorAssignmentModeSpecificHosts,
						SelectedTargets: []string{"gen-legacy"},
					},
				},
			},
		})
		if createErr != nil {
			t.Fatalf("CreateTest: %v", createErr)
		}
		if startErr := runner.StartTest(created.ID); startErr != nil {
			t.Fatalf("StartTest: %v", startErr)
		}
		deadline := time.Now().Add(5 * time.Second)
		var terminal *ManagedTest
		for time.Now().Before(deadline) {
			snap, ok := runner.GetTest(created.ID)
			if ok && (snap.Status == "FAILED" || snap.Status == "COMPLETE" || snap.Status == "STOPPED") {
				terminal = snap
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if terminal == nil || terminal.Status != "FAILED" {
			t.Fatalf("status = %+v, want FAILED", terminal)
		}
		if !strings.Contains(terminal.Error, dsl.FeatureBrowserNativeHARV1) {
			t.Fatalf("Error = %q, want %s", terminal.Error, dsl.FeatureBrowserNativeHARV1)
		}
		if got := targetHits.Load(); got != 0 {
			t.Fatalf("target received %d HTTP requests before multi-group refusal", got)
		}
	})

	// Verify HAR capture "off" (including policy override) still admits mixed peers at placement.
	offScript := &dsl.Script{
		ID:       "script-har-off",
		Name:     "Browser HAR Off",
		Protocol: "browser",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: target.URL,
			Browser: dsl.BrowserConfig{
				HARCapture:       "always",
				HARCapturePolicy: "off",
			},
		},
		Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "/"}},
	}
	group := TestGroup{
		Name:     "Web",
		Protocol: "browser",
		VUsers:   2,
		script:   offScript,
		Assignment: &GeneratorAssignmentSettings{
			Mode:            generatorAssignmentModeSpecificHosts,
			SelectedTargets: []string{"gen-current", "gen-legacy"},
		},
	}
	if required := groupRequiredRuntimeFeature(group); strings.Contains(required, dsl.FeatureBrowserNativeHARV1) {
		t.Fatalf("har off group required %s: %q", dsl.FeatureBrowserNativeHARV1, required)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cCurrent, err := dial(ctx, "gen-current")
	if err != nil {
		t.Fatal(err)
	}
	defer cCurrent.Close()
	cLegacy, err := dial(ctx, "gen-legacy")
	if err != nil {
		t.Fatal(err)
	}
	defer cLegacy.Close()
	loads := map[string]generatorLoadSnapshot{
		"gen-current": backend.probeGeneratorLoad(ctx, "gen-current", cCurrent),
		"gen-legacy":  backend.probeGeneratorLoad(ctx, "gen-legacy", cLegacy),
	}
	configurePodLoads(SystemSettings{}, loads)
	specs := []generatorTargetSpec{{Address: "gen-current"}, {Address: "gen-legacy"}}
	shards := planDistributedGroupShardsForCandidates([]TestGroup{group}, loads, loads, []string{"gen-current", "gen-legacy"}, []string{"gen-current", "gen-legacy"}, nil, specs)
	if len(shards["Web"]) != 2 {
		t.Fatalf("mixed fleet HAR-off shards = %+v, want 2 shards across current and legacy peers", shards["Web"])
	}
}

func TestEvacuationShardAdjustmentRefusesReplacementWithoutNativeHAR(t *testing.T) {
	if !browser.NativeHARSupported() {
		t.Skip("current host platform does not support native HAR capture")
	}
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_DIR", t.TempDir())
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	legacyListener := startNativeHARTestPeer(t, "gen-legacy", 5, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	legacyClient, err := generatorrpc.Dial(
		ctx,
		"gen-legacy",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return legacyListener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer legacyClient.Close()

	backend := newDistributedExecutionBackend([]string{"gen-a", "gen-legacy"}, 5*time.Second, nil)
	runner := NewTestRunner(5, NewHub(), nil)
	t.Cleanup(runner.beginShutdown)

	script := &dsl.Script{
		ID:       "evac-har",
		Name:     "Evac HAR",
		Protocol: "browser",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: target.URL,
			Browser: dsl.BrowserConfig{HARCapture: "always"},
		},
		Actions: []dsl.Action{{Name: "Open", Type: "browser_navigate", Target: "/"}},
	}
	group := TestGroup{Name: "web", ScriptID: "evac-har", ScriptName: "Evac HAR", Protocol: "browser", VUsers: 1, script: script}
	managed := &ManagedTest{ID: "run-evac-har", Status: "RUNNING", Groups: []TestGroup{group}}

	_, _, _, _, err = backend.applyEvacuationShardAdjustments(
		ctx,
		ctx,
		runner,
		managed,
		managed.ID,
		1,
		false,
		10*time.Second,
		time.Second,
		false,
		map[string]TestGroup{"web": group},
		map[string]generatorClient{"gen-legacy": legacyClient},
		map[string]*generatorrpc.MetricsStream{},
		map[string][]GeneratorShardSummary{"web": {{Group: "web", Target: "gen-a", VUsers: 1}}},
		map[string][]GeneratorShardSummary{"web": {{Group: "web", Target: "gen-legacy", VUsers: 1, ShardOrdinal: 0, ShardCount: 1}}},
		"gen-a",
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), dsl.FeatureBrowserNativeHARV1) {
		t.Fatalf("applyEvacuationShardAdjustments err = %v, want %s", err, dsl.FeatureBrowserNativeHARV1)
	}
	if got := targetHits.Load(); got != 0 {
		t.Fatalf("evacuation emitted %d target HTTP requests", got)
	}
}

func TestBrowserHARCaptureFeatureSurvivesTerminalRuntimeTrim(t *testing.T) {
	yes := true
	script := &dsl.Script{
		Protocol: "browser",
		Config: dsl.ScriptConfig{
			Browser: dsl.BrowserConfig{
				CaptureDocumentMetrics: &yes,
				HARCapture:             "manual",
			},
		},
	}
	run := &ManagedTest{
		ID:     "har-trim",
		Status: "COMPLETE",
		Groups: []TestGroup{{
			Name:                            "Browser",
			Protocol:                        "browser",
			BrowserDocumentCaptureRequested: true,
			script:                          script,
		}},
	}
	frozen := cloneArchiveTestGroups(run.Groups)
	if !frozen[0].browserHARCaptureRequested {
		t.Fatal("cloneArchiveTestGroups lost browserHARCaptureRequested")
	}
	trimTerminalManagedTestRuntime(run)
	if run.Groups[0].script != nil || !groupRequestsBrowserHARCapture(run.Groups[0]) {
		t.Fatal("trimTerminalManagedTestRuntime lost browserHARCaptureRequested")
	}
	features := requiredBrowserFeaturesForGroup(run.Groups[0])
	if !slices.Contains(features, dsl.FeatureBrowserDocumentEvidenceV2) || !slices.Contains(features, dsl.FeatureBrowserNativeHARV1) {
		t.Fatalf("requiredBrowserFeaturesForGroup after trim = %v, want both %s and %s", features, dsl.FeatureBrowserDocumentEvidenceV2, dsl.FeatureBrowserNativeHARV1)
	}
}
