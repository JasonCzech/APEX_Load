package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/monitorcontract"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

const rcaGraphDependency = "http:https://api.shop.local/cart"

// rcaGraphServer archives a run whose cart requests reach api.shop.local,
// with Dynatrace Smartscape reporting that service, what it calls and where
// it runs.
func rcaGraphServer(t *testing.T) *Server {
	t.Helper()
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "rca-run", func(i int, tx *TransactionEvent) {
		tx.DependencyTarget = "https://api.shop.local/cart"
	})
	writeArchiveSidecar(t, record, "monitor_points.jsonl", []analysis.MetricPoint{}, func(m *RunArchiveManifest) {
		m.MonitorCoverage = []RunMonitorCoverage{{SourceID: "dt-src", Dynatrace: &dynatrace.Selection{}, Topology: []monitorprovider.Entity{
			{ID: "SERVICE-1", Name: "api.shop.local", Type: "SERVICE"},
			{ID: "SERVICE-2", Name: "orders-db", Type: "SERVICE"},
			{ID: "HOST-1", Name: "node-a", Type: "HOST"},
			{ID: "smartscape-edge:1", Name: "calls", Type: "relationship", EntityIDs: map[string]string{"source_id": "SERVICE-1", "target_id": "SERVICE-2", "source_type": "SERVICE", "target_type": "SERVICE"}},
			{ID: "smartscape-edge:2", Name: "runs_on", Type: "relationship", EntityIDs: map[string]string{"source_id": "SERVICE-1", "target_id": "HOST-1", "source_type": "SERVICE", "target_type": "HOST"}},
		}}}
	})
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", storage.DefaultProjectID)
	return server
}

func graphEdge(g rca.Graph, kind, from, to string) *rca.GraphEdge {
	for i := range g.Edges {
		if e := &g.Edges[i]; e.Kind == kind && e.From == from && e.To == to {
			return e
		}
	}
	return nil
}

func TestRCAGraphFromArchivedTopologyAndApprovals(t *testing.T) {
	server := rcaGraphServer(t)
	first := completedRCAAssessment(t, server, "rca-run", map[string]any{})
	graph := first.Graph
	dependency := rca.DependencyNodeID(rcaGraphDependency)
	service := rca.EntityNodeID(monitorprovider.ProviderDynatrace, "SERVICE-1")
	for _, want := range []struct{ kind, from, to string }{
		{rca.EdgeCalls, rca.ActionNodeID("GET /cart"), dependency},
		{rca.EdgeCalls, service, rca.EntityNodeID(monitorprovider.ProviderDynatrace, "SERVICE-2")},
		{rca.EdgeSharesInfrastructure, service, rca.EntityNodeID(monitorprovider.ProviderDynatrace, "HOST-1")},
		{rca.EdgeMonitors, "source:dt-src", service},
	} {
		if e := graphEdge(graph, want.kind, want.from, want.to); e == nil || !e.Used {
			t.Fatalf("%s %s -> %s missing: %+v", want.kind, want.from, want.to, graph.Edges)
		}
	}
	// The name match is listed but not used until an operator approves it.
	inferred := graphEdge(graph, rca.EdgeMapsTo, dependency, service)
	if inferred == nil || inferred.Used || inferred.Confidence != rca.ConfidenceInferred || !rcaLimitation(first, rca.LimitationGraphUnapproved) || first.Inputs.Mapping != "" {
		t.Fatalf("inferred %+v, limitations %+v", inferred, first.Limitations)
	}

	// Approving it changes the frozen inputs, so a new analysis uses it.
	approval := timeslotRequest(t, server, http.MethodPost, "/api/v1/analysis/rca-mappings?project_id="+storage.DefaultProjectID, map[string]any{
		"name": "cart API", "definition": map[string]any{"dependency": rcaGraphDependency, "provider": monitorprovider.ProviderDynatrace, "entity_id": "SERVICE-1", "decision": rca.MappingApproved},
	})
	if approval.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", approval.Code, approval.Body.String())
	}
	var saved storage.AnalysisDefinition
	_ = json.Unmarshal(approval.Body.Bytes(), &saved)
	second := completedRCAAssessment(t, server, "rca-run", map[string]any{})
	approved := graphEdge(second.Graph, rca.EdgeMapsTo, dependency, service)
	if second.AnalysisID == first.AnalysisID || second.Inputs.Mapping == "" || approved == nil || !approved.Used ||
		approved.Confidence != rca.ConfidenceExplicit || approved.Provenance != "mapping:"+saved.ID || approved.Revision != "1" ||
		rcaLimitation(second, rca.LimitationGraphUnapproved) {
		t.Fatalf("approved %+v, inputs %+v, limitations %+v", approved, second.Inputs, second.Limitations)
	}
	for _, c := range second.Coverage {
		if c.Evidence == "topology" && c.Status != rca.CoverageComplete {
			t.Fatalf("topology coverage %+v", c)
		}
	}

	// Rejecting it removes the candidate.
	item := "/api/v1/analysis/rca-mappings/" + saved.ID + "?project_id=" + storage.DefaultProjectID
	if reply := timeslotRequest(t, server, http.MethodPut, item, map[string]any{
		"name": "cart API", "revision": 1, "definition": map[string]any{"dependency": rcaGraphDependency, "provider": monitorprovider.ProviderDynatrace, "entity_id": "SERVICE-1", "decision": rca.MappingRejected},
	}); reply.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", reply.Code, reply.Body.String())
	}
	third := completedRCAAssessment(t, server, "rca-run", map[string]any{})
	if graphEdge(third.Graph, rca.EdgeMapsTo, dependency, service) != nil || rcaLimitation(third, rca.LimitationGraphUnapproved) || third.Inputs.Mapping == second.Inputs.Mapping {
		t.Fatalf("rejected: %+v %+v", third.Graph.Edges, third.Inputs)
	}
	// Deleting the decision brings the inferred candidate back.
	if reply := timeslotRequest(t, server, http.MethodDelete, item+"&revision=2", nil); reply.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", reply.Code, reply.Body.String())
	}
	fourth := completedRCAAssessment(t, server, "rca-run", map[string]any{})
	if e := graphEdge(fourth.Graph, rca.EdgeMapsTo, dependency, service); e == nil || e.Used || fourth.Inputs.Mapping != "" || fourth.AnalysisID == first.AnalysisID {
		t.Fatalf("deleted: %+v %+v", e, fourth.Inputs)
	}

	// An analysis whose mapping decisions cannot be read fails rather than
	// judging without them.
	record := server.runner.archives["rca-run"]
	digest, _ := rcaArchiveDigest(record)
	_, err := server.assessRCAAnalysis(context.Background(), &storage.RCAAnalysis{ID: "a", ProjectID: storage.DefaultProjectID, TestID: "rca-run"},
		rcaAnalysisPayload{Inputs: rca.InputDigests{Archive: digest, EvidenceRevision: 1, Mapping: "not-the-frozen-digest", Engine: rcaEngineVersion}})
	if err == nil || !strings.Contains(err.Error(), "mapping decisions") {
		t.Fatalf("missing decisions: %v", err)
	}
}

func TestRCAMappingDefinitionsAreValidatedAndGated(t *testing.T) {
	server := rcaGraphServer(t)
	path := "/api/v1/analysis/rca-mappings?project_id=" + storage.DefaultProjectID
	for name, definition := range map[string]map[string]any{
		"unknown provider": {"dependency": "x", "provider": "not-a-provider", "entity_id": "E", "decision": rca.MappingApproved},
		"bad decision":     {"dependency": "x", "provider": monitorprovider.ProviderDynatrace, "entity_id": "E", "decision": "maybe"},
		"no entity":        {"dependency": "x", "provider": monitorprovider.ProviderDynatrace, "decision": rca.MappingApproved},
		"unknown field":    {"dependency": "x", "provider": monitorprovider.ProviderDynatrace, "entity_id": "E", "decision": rca.MappingApproved, "extra": true},
	} {
		if reply := timeslotRequest(t, server, http.MethodPost, path, map[string]any{"name": name, "definition": definition}); reply.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, reply.Code, reply.Body.String())
		}
	}
	if err := validateRCAMappingDefinition(json.RawMessage(`{"dependency":"x","provider":"dynatrace","entity_id":"E","decision":"approved"}{"decision":"rejected"}`)); err == nil {
		t.Fatal("trailing content accepted")
	}
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", "")
	if reply := timeslotRequest(t, server, http.MethodGet, path, nil); reply.Code != http.StatusConflict {
		t.Fatalf("gate off: %d", reply.Code)
	}
}

func TestArchiveGraphTopologyNormalizesProviders(t *testing.T) {
	manifest := &RunArchiveManifest{MonitorCoverage: []RunMonitorCoverage{
		{SourceID: "dt", Dynatrace: &dynatrace.Selection{}, Topology: []monitorprovider.Entity{
			// A classic entity lists its relationships as entity IDs.
			{ID: "SERVICE-1", Name: "cart", Type: "SERVICE", EntityIDs: map[string]string{"classic_id": "SERVICE-1", "from.calls.0": "SERVICE-2", "to.calls.0": "SERVICE-0", "from.runsOn.0": "PROCESS_GROUP_INSTANCE-9", "to.isInstanceOf.0": "SERVICE-I"}},
			// A Smartscape entity keeps its classic ID as an alias.
			{ID: "smartscape-host", Name: "node-a", Type: "HOST", EntityIDs: map[string]string{"smartscape_id": "smartscape-host", "classic_id": "HOST-7"}},
		}},
		{SourceID: "appd", Selection: &monitorcontract.AppDynamicsSelection{}, Topology: []monitorprovider.Entity{
			{ID: "7", Name: "web", Type: "tier", ParentID: "1"},
			{ID: "5", Name: "web-1", Type: "node", EntityIDs: map[string]string{"tier": "7"}},
		}},
		{SourceID: "loki"},
		{},
	}}
	sources, entities, relations := archiveGraphTopology(manifest)
	if len(sources) != 3 || sources[0].Provider != monitorprovider.ProviderDynatrace || sources[1].Provider != monitorprovider.ProviderAppDynamics || sources[2].Provider != "" {
		t.Fatalf("sources %+v", sources)
	}
	type rel struct{ kind, from, to string }
	var got []rel
	for _, r := range relations {
		got = append(got, rel{r.Kind, r.From, r.To})
	}
	want := []rel{
		{rca.RelationCalls, "SERVICE-1", "SERVICE-2"},
		{rca.RelationRunsOn, "SERVICE-1", "PROCESS_GROUP_INSTANCE-9"},
		{rca.RelationCalls, "SERVICE-0", "SERVICE-1"},
		// SERVICE-I is an instance of SERVICE-1.
		{rca.RelationContains, "SERVICE-1", "SERVICE-I"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("relations %v", got)
	}
	if len(entities) != 4 || entities[2].ID != "tier:7" || entities[2].ParentID != "application:1" || entities[3].ID != "node:5" || entities[3].ParentID != "tier:7" ||
		!reflect.DeepEqual(entities[1].Aliases, []string{"HOST-7"}) || !entities[1].Infrastructure {
		t.Fatalf("entities %+v", entities)
	}
	// A classic end's type comes from its ID, so a process instance is
	// infrastructure.
	if r := relations[1]; r.ToType != "PROCESS_GROUP_INSTANCE" || !r.ToInfrastructure {
		t.Fatalf("runs_on end %+v", r)
	}
	for kind, want := range map[string]struct {
		relation string
		reversed bool
	}{"calls": {rca.RelationCalls, false}, "called_by": {rca.RelationCalls, true}, "isCalledBy": {rca.RelationCalls, true}, "runs_on": {rca.RelationRunsOn, false}, "belongs_to": {rca.RelationContains, true}, "contains": {rca.RelationContains, false}, "monitors": {"", false}} {
		if got, reversed := normalizeRelation(kind); got != want.relation || reversed != want.reversed {
			t.Errorf("%s: %q %v, want %+v", kind, got, reversed, want)
		}
	}
}

func TestGraphObservationsFromMonitorPoints(t *testing.T) {
	collector := newGraphObservationCollector()
	value := 1.0
	for _, pod := range []string{"web-1", "web-1", "web-2"} {
		collector.observe(analysis.MetricPoint{Source: "prom", Value: &value, Dimensions: map[string]string{"pod": pod, "instance": "10.0.0.5:9100", "le": "0.5"},
			Provider: &analysis.ProviderMeasurement{Name: monitorprovider.ProviderPrometheus}})
	}
	collector.observe(analysis.MetricPoint{Source: "appd", Provider: &analysis.ProviderMeasurement{Name: monitorprovider.ProviderAppDynamics, EntityIDs: map[string]string{"tier": "7"}}})
	observations := collector.list()
	if len(observations) != 2 || !reflect.DeepEqual(observations[0].Entities, []string{"tier:7"}) ||
		!reflect.DeepEqual(observations[1].Infrastructure, map[string][]string{"host": {"10.0.0.5"}, "pod": {"web-1", "web-2"}}) {
		t.Fatalf("%+v", observations)
	}
}
