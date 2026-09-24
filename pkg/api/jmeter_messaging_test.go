package api

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestJMeterMessagingDraftPersistenceAndLaunchGate(t *testing.T) {
	b, e := os.ReadFile("../convert/jmeter/testdata/messaging_original.jmx")
	if e != nil {
		t.Fatal(e)
	}
	r, e := jmeter.ConvertJMX(b, jmeter.ConvertOptions{ScenarioDraft: true})
	if e != nil {
		t.Fatal(e)
	}
	server, store := newAuditTestServer(t)
	defer store.Close()
	preview := server.newScriptImportResponseWithSourceSecrets(httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter", nil), scriptImportResponseOptions{Scripts: r.Scripts, Script: r.Script, ScenarioDraft: r.ScenarioDraft})
	payload, e := json.Marshal(map[string]any{"scripts": preview["scripts"], "scenario_draft": r.ScenarioDraft})
	if e != nil {
		t.Fatal(e)
	}
	response := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", payload, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("save status %d: %s", response.Code, response.Body.String())
	}
	var saved struct {
		Scripts  []StoredScript `json:"scripts"`
		Scenario StoredScenario `json:"scenario"`
	}
	if e := json.Unmarshal(response.Body.Bytes(), &saved); e != nil {
		t.Fatal(e)
	}
	stored := saved.Scenario
	if stored.Duration != "1h5m0s" || stored.Assignment == nil || stored.Assignment.SourceAffinity != "single_generator" || len(stored.Scripts) != 3 {
		t.Fatalf("scenario fields lost: %+v", stored)
	}
	for i, s := range saved.Scripts {
		read, ok := server.runner.GetScript(s.ID)
		if !ok || len(read.Script.Config.SamplerSchedules) != 1 || read.Script.Config.JMeter.SourceGroupID != r.Scripts[i].Config.JMeter.SourceGroupID {
			t.Fatal("script compatibility fields lost")
		}
	}
	if _, e := server.runner.CreateTest(CreateTestRequest{Name: "blocked messaging", ScenarioID: stored.ID}); e == nil {
		t.Fatal("unsupported messaging profile launched")
	}
	copy := cloneJMeterScenarioDraft(r.ScenarioDraft)
	copy.Groups[0].SamplerSchedules[0].Segments[0].EndRPS = 99
	if _, e := buildJMeterScenarioRequest(r.Scripts, copy); e == nil {
		t.Fatal("changed scenario schedule silently accepted")
	}
	if r.ScenarioDraft.Groups[0].SamplerSchedules[0].Segments[0].EndRPS != 84 {
		t.Fatal("recovery clone shared schedule backing array")
	}
	copy = cloneJMeterScenarioDraft(r.ScenarioDraft)
	copy.Groups[0].SourceGroupID = "wrong"
	if _, e := buildJMeterScenarioRequest(r.Scripts, copy); e == nil {
		t.Fatal("changed source identity silently accepted")
	}
}
func TestSourceAffinityForbidsRecovery(t *testing.T) {
	if _, e := normalizeGeneratorAssignmentSettings(&GeneratorAssignmentSettings{SourceAffinity: "single_generator", TolerateGeneratorLoss: true}); e == nil {
		t.Fatal("recovery admitted")
	}
	if _, e := normalizeGeneratorAssignmentSettings(&GeneratorAssignmentSettings{SourceAffinity: "unknown"}); e == nil {
		t.Fatal("unknown affinity admitted")
	}
}

func TestJMeterMessagingRequestRetainsCompletenessCounts(t *testing.T) {
	b, e := os.ReadFile("../convert/jmeter/testdata/messaging_original.jmx")
	if e != nil {
		t.Fatal(e)
	}
	r, e := convertJMeterImportRequest(&jmeterImportRequest{Data: b, Opts: jmeter.ConvertOptions{ScenarioDraft: true}})
	if e != nil {
		t.Fatal(e)
	}
	if r.Stats.EnabledWorkloadSamplers != 3 || r.Stats.ConvertedWorkloadSamplers != 3 || r.Stats.BlockedWorkloadSamplers != 3 || r.Stats.DisabledWorkloadSamplers != 2 {
		t.Fatalf("request aggregation lost counts: %+v", r.Stats)
	}
}

func TestJMeterMessagingScenarioRequiresCompleteSourceAssociation(t *testing.T) {
	b, err := os.ReadFile("../convert/jmeter/testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	r, err := jmeter.ConvertJMX(b, jmeter.ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*jmeter.ScenarioDraft){
		"missing identity": func(d *jmeter.ScenarioDraft) { d.Groups[0].SourceGroupID = "" },
		"missing schedule": func(d *jmeter.ScenarioDraft) { d.Groups[0].SamplerSchedules = nil },
		"duplicate group":  func(d *jmeter.ScenarioDraft) { d.Groups = append(d.Groups, d.Groups[0]) },
		"omitted group":    func(d *jmeter.ScenarioDraft) { d.Groups = d.Groups[1:] },
	} {
		t.Run(name, func(t *testing.T) {
			d := cloneJMeterScenarioDraft(r.ScenarioDraft)
			change(d)
			if _, err := buildJMeterScenarioRequest(r.Scripts, d); err == nil {
				t.Fatal("incomplete association accepted")
			}
		})
	}
}

func TestJMeterMessagingDoesNotCombineIndependentScenarioSchedules(t *testing.T) {
	b, err := os.ReadFile("../convert/jmeter/testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	_, err = convertJMeterImportRequest(&jmeterImportRequest{Documents: []jmeterImportDocument{{Data: b, FileName: "one.jmx"}, {Data: b, FileName: "two.jmx"}}, Opts: jmeter.ConvertOptions{ScenarioDraft: true}})
	if err == nil {
		t.Fatal("independent plan schedules silently combined")
	}
}
