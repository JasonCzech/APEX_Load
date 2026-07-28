package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestEnterpriseJavaImportResponseCanBeApprovedUnchanged(t *testing.T) {
	const projectID = "project-java-import-approval-flow"
	server := newJavaVuserGovernanceServer(t, projectID)
	archive := buildJavaEnterpriseArchive(t, "JavaApprovalFlow", "Java Vuser", `import lrapi.lr;
import java.sql.*;

public class Actions {
  private int iterations;

  public int init() throws Throwable {
    lr.save_int(7, "seed");
    return 0;
  }

  public int action() throws Throwable {
    iterations++;
    lr.start_transaction("business-flow");
    String endpoint = "https://services.web.example.test/orders";
    lr.end_transaction("business-flow", lr.AUTO);
    return 0;
  }

  public int end() throws Throwable {
    return iterations >= 0 ? 0 : 1;
  }
}`)

	importRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/enterprise-script-archive?project_id="+projectID+"&allow_java_vuser_runner=false",
		bytes.NewReader(archive),
	)
	importRequest.Header.Set("Content-Type", "application/zip")
	importResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(importResponse, importRequest)
	if importResponse.Code != http.StatusOK {
		t.Fatalf("enterprise Java import status = %d body=%s", importResponse.Code, importResponse.Body.String())
	}
	var imported struct {
		Script         dsl.Script   `json:"script"`
		Scripts        []dsl.Script `json:"scripts"`
		FidelityReport struct {
			PromotionStatus string   `json:"promotion_status"`
			Blockers        []string `json:"blockers"`
		} `json:"fidelity_report"`
	}
	if err := json.Unmarshal(importResponse.Body.Bytes(), &imported); err != nil {
		t.Fatalf("decode enterprise Java import: %v", err)
	}
	if len(imported.Scripts) != 1 || imported.Script.Protocol != "java_vuser" ||
		!equalScripts(&imported.Script, &imported.Scripts[0]) {
		t.Fatalf("import response = %#v, want one combined Java draft referenced by script", imported)
	}
	if imported.FidelityReport.PromotionStatus != "promotable" || len(imported.FidelityReport.Blockers) != 0 {
		t.Fatalf("fidelity report = %#v, want promotable combined draft", imported.FidelityReport)
	}

	approval := approveJavaVuserDraftThroughAPI(t, server, projectID, &imported.Script)
	if approval.Session == nil || approval.Session.Status != "approved" ||
		approval.Script == nil || approval.Script.Protocol != "java_vuser" {
		t.Fatalf("approval response = %#v, want approved singleton Java library script", approval)
	}
	if evidence, ok := readJavaVuserAuthoringApproval(approval.Script.Script); !ok {
		t.Fatal("approved imported response omitted governance evidence")
	} else if evidence.contentHash != hashLibraryScript(approval.Script.Script) {
		t.Fatalf(
			"approved imported response hash = %q, canonical response hash = %q",
			evidence.contentHash,
			hashLibraryScript(approval.Script.Script),
		)
	}
	if conflict := server.runner.javaVuserApprovalConflict(approval.Script.Script); conflict != "" {
		t.Fatalf("approved imported script failed governance verification: %s", conflict)
	}
}

func TestPendingCombinedJavaPromotionRevalidatesRunnerCapability(t *testing.T) {
	const projectID = "project-java-pending-capability"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"},
		time.Second,
		nil,
	))
	setPreflightJavaVuserInventory(server.runner, protocol.FeatureJavaVuserPersistentLifecycleV1)
	pending, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Pending combined Java", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 1,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	now := time.Now().UTC()
	server.runner.mu.Lock()
	server.runner.tests[pending.ID].Status = "PENDING"
	server.runner.tests[pending.ID].PendingSince = &now
	server.runner.mu.Unlock()

	setPreflightJavaVuserInventory(server.runner, protocol.FeatureJavaVuserPhaseLifecycleV1)
	server.runner.promotePendingTests()

	deferred, ok := server.runner.GetTest(pending.ID)
	if !ok || deferred.Status != "PENDING" {
		t.Fatalf("pending test after runner downgrade = %#v, want durable PENDING", deferred)
	}
	if !strings.Contains(deferred.PendingReason, "java_vuser_runner_required") {
		t.Fatalf("pending reason = %q, want Java runner capability detail", deferred.PendingReason)
	}
}

func TestPendingCombinedJavaPromotionRevalidatesAggregateCapacity(t *testing.T) {
	const projectID = "project-java-pending-capacity"
	server := newJavaVuserGovernanceServer(t, projectID)
	approved := approveCombinedJavaVuserThroughAPI(t, server, projectID)
	server.runner.setExecutionBackend(newDistributedExecutionBackend(
		[]string{"gen-jvm:9090|protocols=java_vuser|labels=runtime:java_vuser"},
		time.Second,
		nil,
	))
	features := governedPersistentJavaRuntimeFeatures()
	server.runner.setCachedGeneratorInventory([]GeneratorInventoryEntry{{
		Target:             "gen-jvm:9090",
		GeneratorID:        "gen-jvm",
		Status:             "connected",
		DrainState:         "active",
		AvailableCapacity:  100,
		MaxVUsers:          100,
		SupportedProtocols: []string{"java_vuser"},
		CompatibleFeatures: features,
		RuntimeCapacities: []generatorrpc.ProtocolRuntimeCapacity{{
			Protocol:        "java_vuser",
			Available:       true,
			MaxVUsers:       6,
			AvailableVUsers: 6,
			Features:        features,
			OccupancyMode:   generatorrpc.RuntimeOccupancyModeIdle,
		}},
	}}, time.Now())
	active, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Active combined Java", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 4,
	})
	if err != nil {
		t.Fatalf("CreateTest(active): %v", err)
	}
	pending, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Pending combined Java", ProjectID: projectID, Duration: "5m", ScriptID: approved.Script.ID, VUsers: 3,
	})
	if err != nil {
		t.Fatalf("CreateTest(pending): %v", err)
	}
	now := time.Now().UTC()
	server.runner.mu.Lock()
	server.runner.tests[active.ID].Status = "INITIALIZING"
	server.runner.tests[pending.ID].Status = "PENDING"
	server.runner.tests[pending.ID].PendingSince = &now
	server.runner.mu.Unlock()

	server.runner.promotePendingTests()

	deferred, ok := server.runner.GetTest(pending.ID)
	if !ok || deferred.Status != "PENDING" {
		t.Fatalf("pending test after Java capacity pressure = %#v, want durable PENDING", deferred)
	}
	if !strings.Contains(deferred.PendingReason, "generator_placement") ||
		!strings.Contains(deferred.PendingReason, "available java_vuser runtime slots") {
		t.Fatalf("pending reason = %q, want aggregate Java capacity detail", deferred.PendingReason)
	}
}

func equalScripts(left, right *dsl.Script) bool {
	if left == nil || right == nil {
		return left == right
	}
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
