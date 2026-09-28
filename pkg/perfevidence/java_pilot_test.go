package perfevidence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

func TestJavaPilotEngineSelectionAndTrialIdentity(t *testing.T) {
	script, err := JavaFixtureScript("light", "http://127.0.0.1:18099", []string{"/approved/fixture.jar"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := engine.Config{Script: script, VUsers: 2, Duration: time.Second}
	if _, err = newJavaEvidenceEngine(cfg, nil); err != nil {
		t.Fatal("dedicated configuration changed", err)
	}
	if _, err = newJavaEvidenceEngine(cfg, &javaPilotRun{}); err == nil {
		t.Fatal("invalid pilot fell back to dedicated")
	}
	client, err := javasidecar.NewClient("http://127.0.0.1:8098", nil)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := engine.JavaPooledScriptSHA256(script)
	pilot := &javaPilotRun{preparation: time.Minute, options: engine.JavaPooledPilotOptions{Client: client, ScriptSHA256: digest, PreparationTimeout: time.Minute, ActionTimeout: time.Second, Run: javasidecar.PooledClientOptions{ExecutionID: "trial", GroupID: "group", Decision: javasidecar.PooledControlDecision{InvocationEnabled: true, TargetClass: script.Actions[0].Target, MaxVUsers: 2}}}}
	if _, err = newJavaEvidenceEngine(cfg, pilot); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		trial := javaPilotTrial(pilot, i)
		id := trial.options.Run.ExecutionID
		if seen[id] || id == pilot.options.Run.ExecutionID {
			t.Fatal("trial reused Java identity")
		}
		seen[id] = true
		if trial.options.Run.Decision != pilot.options.Run.Decision || trial.options.ScriptSHA256 != digest {
			t.Fatal("qualification changed")
		}
	}
	if javaPilotTrial(nil, 0) != nil || pilot.options.Run.ExecutionID != "trial" {
		t.Fatal("trial mutation")
	}
}
func TestJavaPilotEvidenceBindingDoesNotGrantQualification(t *testing.T) {
	pilot := &javaPilotRun{options: engine.JavaPooledPilotOptions{ScriptSHA256: strings.Repeat("a", 64), Run: javasidecar.PooledClientOptions{Decision: javasidecar.PooledControlDecision{ConfigurationSHA256: strings.Repeat("b", 64)}}}}
	report := JavaEvidence{Mode: "dedicated", Identity: JavaEvidenceIdentity{Backend: "delegated_linux_sidecar", MemoryMethod: "aggregate_process_pss_bytes"}}
	before := report.Identity
	configureJavaPilotReport(&report, pilot)
	if report.Mode != "pooled" || report.Identity != before || report.SoakPassed || report.SoakSeconds != 0 || report.RealWorkload {
		t.Fatal("unmeasured qualification or different comparison identity", report)
	}
	digest := report.ConfigurationSHA256
	pilot.options.Run.Decision.MaxHosts++
	configureJavaPilotReport(&report, pilot)
	if digest == report.ConfigurationSHA256 {
		t.Fatal("capacity policy omitted from configuration binding")
	}
	digest = report.ConfigurationSHA256
	pilot.options.ScriptSHA256 = strings.Repeat("c", 64)
	configureJavaPilotReport(&report, pilot)
	if digest == report.ConfigurationSHA256 {
		t.Fatal("DSL approval omitted from binding")
	}
}
func TestJavaPilotOwnerKeeperFencesWithoutReclaim(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal_stop", true: "fenced"}[failure], func(t *testing.T) {
			var calls atomic.Int32
			renewed := make(chan struct{}, 1)
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var response any
				switch r.URL.Path {
				case "/v1/owner/claim":
					if calls.Add(1) != 1 {
						t.Error("heartbeat reclaimed owner")
					}
					response = javasidecar.ClaimOwnerResponse{Contract: javasidecar.ContractVersion, OwnerID: "owner", Epoch: "epoch", Claimed: true, LeaseTTLMS: javasidecar.RunnerOwnerLeaseTTL.Milliseconds()}
				case "/v1/owner/renew":
					if failure {
						w.WriteHeader(http.StatusConflict)
						response = javasidecar.ErrorResponse{Contract: javasidecar.ContractVersion, ErrorCode: "owner_fenced"}
					} else {
						response = javasidecar.RenewOwnerResponse{Contract: javasidecar.ContractVersion, OwnerID: "owner", OwnerEpoch: "epoch", Renewed: true, LeaseTTLMS: javasidecar.RunnerOwnerLeaseTTL.Milliseconds()}
					}
					select {
					case renewed <- struct{}{}:
					default:
					}
				default:
					t.Error("unexpected path", r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer h.Close()
			c, err := javasidecar.NewClient(h.URL, h.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.ClaimOwner(context.Background(), "owner"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- keepJavaPilotOwner(ctx, c, cancel) }()
			select {
			case <-renewed:
			case <-time.After(3 * time.Second):
				t.Fatal("owner not renewed")
			}
			if !failure {
				cancel()
			}
			select {
			case err := <-done:
				if failure && (err == nil || errors.Is(err, context.Canceled) || ctx.Err() == nil) {
					t.Fatal("fence lost", err)
				}
				if !failure && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("keeper did not stop")
			}
			if calls.Load() != 1 {
				t.Fatal("owner reclaimed")
			}
		})
	}
}
func TestJavaPilotRequiresExplicitManifestAndBounds(t *testing.T) {
	for _, cfg := range []JavaPilotRunConfig{{}, {ManifestPath: "/approved/manifest"}, {ManifestPath: "/approved/manifest", PreparationTimeout: 2 * time.Hour}} {
		report, err := RunJavaPooled(context.Background(), cfg)
		if err == nil || report.Schema != "" {
			t.Fatal("invalid pilot produced evidence")
		}
	}
}

func TestJavaPilotOwnerFailureSurvivesReportCompletion(t *testing.T) {
	for _, cause := range []error{context.Canceled, errors.New("owner renewal failed")} {
		done := make(chan error, 1)
		done <- cause
		report := JavaEvidence{Samples: []JavaEvidenceSample{{Complete: true}}}
		ctx, cancel := context.WithCancel(context.Background())
		original := errors.New("sample failure")
		var runErr error = original
		finishJavaPilotOwner(cancel, done, &report, &runErr)
		if ctx.Err() == nil || !errors.Is(runErr, original) {
			t.Fatal("completion lost cancellation or primary failure")
		}
		if errors.Is(cause, context.Canceled) {
			if !report.Samples[0].Complete {
				t.Fatal("normal keeper shutdown invalidated evidence")
			}
		} else {
			if report.Samples[0].Complete || !errors.Is(runErr, cause) {
				t.Fatal("late owner failure hidden")
			}
		}
	}
}
