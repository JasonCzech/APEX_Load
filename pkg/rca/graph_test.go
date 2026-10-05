package rca

import (
	"encoding/json"
	"reflect"
	"testing"
)

// graphFixture is a run that called a service through its host name, with
// Dynatrace Smartscape reporting that service, what it calls and where it
// runs.
func graphFixture() GraphInput {
	return GraphInput{
		Calls: []GraphCall{
			{Transaction: "checkout", Action: "GET /cart", DependencyType: "http", Target: "https://api.shop.local:443/cart"},
			{Transaction: "checkout", Action: "POST /order", DependencyType: "http", Target: "https://api.shop.local/order"},
			{Action: "publish", DependencyType: "kafka", Target: "orders"},
		},
		Sources: []GraphSource{{ID: "dt-src", Provider: "dynatrace", Label: "Dynatrace prod"}},
		Entities: []GraphEntity{
			{Provider: "dynatrace", SourceID: "dt-src", ID: "SERVICE-1", Name: "api.shop.local", Type: "SERVICE"},
			{Provider: "dynatrace", SourceID: "dt-src", ID: "SERVICE-2", Name: "orders-db", Type: "SERVICE"},
			{Provider: "dynatrace", SourceID: "dt-src", ID: "HOST-1", Name: "node-a", Type: "HOST", Infrastructure: true},
		},
		Relations: []GraphRelation{
			{Provider: "dynatrace", SourceID: "dt-src", Kind: RelationCalls, From: "SERVICE-1", To: "SERVICE-2", FromType: "SERVICE", ToType: "SERVICE"},
			{Provider: "dynatrace", SourceID: "dt-src", Kind: RelationRunsOn, From: "SERVICE-1", To: "HOST-1", FromType: "SERVICE", ToType: "HOST", ToInfrastructure: true},
		},
	}
}

func edgeOf(g Graph, kind, from, to string) *GraphEdge {
	for i := range g.Edges {
		if e := &g.Edges[i]; e.Kind == kind && e.From == from && e.To == to {
			return e
		}
	}
	return nil
}

func nodeOf(g Graph, id string) *GraphNode {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

func TestGraphFromSmartscapeAndArchiveDependencies(t *testing.T) {
	graph, limitations := BuildGraph(graphFixture(), nil)
	api := DependencyNodeID("http:https://api.shop.local:443/cart")
	service := EntityNodeID("dynatrace", "SERVICE-1")
	for _, want := range []struct {
		kind, from, to, confidence string
		used                       bool
	}{
		{EdgeContains, TransactionNodeID("checkout"), ActionNodeID("GET /cart"), ConfidenceExplicit, true},
		{EdgeCalls, ActionNodeID("GET /cart"), api, ConfidenceExplicit, true},
		{EdgeCalls, ActionNodeID("publish"), DependencyNodeID("kafka:orders"), ConfidenceExplicit, true},
		{EdgeCalls, service, EntityNodeID("dynatrace", "SERVICE-2"), ConfidenceProvider, true},
		{EdgeSharesInfrastructure, service, EntityNodeID("dynatrace", "HOST-1"), ConfidenceProvider, true},
		{EdgeMonitors, "source:dt-src", service, ConfidenceProvider, true},
		// The host name matches the service, but only an approval uses it.
		{EdgeMapsTo, api, service, ConfidenceInferred, false},
	} {
		e := edgeOf(graph, want.kind, want.from, want.to)
		if e == nil || e.Confidence != want.confidence || e.Used != want.used || e.Provenance == "" {
			t.Fatalf("%s %s -> %s: %+v", want.kind, want.from, want.to, e)
		}
	}
	if n := nodeOf(graph, EntityNodeID("dynatrace", "HOST-1")); n == nil || n.Kind != NodeInfrastructure {
		t.Fatalf("host node %+v", n)
	}
	if n := nodeOf(graph, api); n == nil || n.Attributes["host"] != "api.shop.local" {
		t.Fatalf("dependency node %+v", n)
	}
	if !hasLimitation(limitations, LimitationGraphUnapproved) || graph.Schema != GraphSchemaVersion || len(graph.Cycles) != 0 {
		t.Fatalf("%+v %+v", limitations, graph.Cycles)
	}
	// The run's own nodes come first.
	if graph.Nodes[0].Kind != NodeTransaction || graph.Nodes[len(graph.Nodes)-1].Kind != NodeInfrastructure {
		t.Fatalf("node order %+v", graph.Nodes)
	}
}

func TestGraphUsesOnlyApprovedMappings(t *testing.T) {
	label := "http:https://api.shop.local:443/cart"
	input := graphFixture()
	input.Mappings = []GraphMapping{{ID: "m1", Revision: 3, Dependency: label, Provider: "dynatrace", EntityID: "SERVICE-1", Decision: MappingApproved}}
	graph, limitations := BuildGraph(input, nil)
	e := edgeOf(graph, EdgeMapsTo, DependencyNodeID(label), EntityNodeID("dynatrace", "SERVICE-1"))
	if e == nil || !e.Used || e.Confidence != ConfidenceExplicit || e.Provenance != "mapping:m1" || e.Revision != "3" {
		t.Fatalf("%+v", e)
	}
	// The other dependency on the same host still awaits approval.
	if other := edgeOf(graph, EdgeMapsTo, DependencyNodeID("http:https://api.shop.local/order"), EntityNodeID("dynatrace", "SERVICE-1")); other == nil || other.Used {
		t.Fatalf("%+v", other)
	}
	if !hasLimitation(limitations, LimitationGraphUnapproved) {
		t.Fatalf("%+v", limitations)
	}
	// A rejection removes the candidate.
	input.Mappings = []GraphMapping{
		{ID: "m1", Revision: 4, Dependency: label, Provider: "dynatrace", EntityID: "SERVICE-1", Decision: MappingRejected},
		{ID: "m2", Revision: 1, Dependency: "http:https://api.shop.local/order", Provider: "dynatrace", EntityID: "SERVICE-1", Decision: MappingRejected},
	}
	graph, limitations = BuildGraph(input, nil)
	for _, e := range graph.Edges {
		if e.Kind == EdgeMapsTo {
			t.Fatalf("rejected mapping kept: %+v", e)
		}
	}
	if hasLimitation(limitations, LimitationGraphUnapproved) {
		t.Fatalf("%+v", limitations)
	}
	// An approval for an entity the run's topology lacks still maps.
	input.Mappings = []GraphMapping{{ID: "m3", Revision: 1, Dependency: "kafka:orders", Provider: "dynatrace", EntityID: "QUEUE-9", EntityName: "orders queue", Decision: MappingApproved}}
	graph, _ = BuildGraph(input, nil)
	if n := nodeOf(graph, EntityNodeID("dynatrace", "QUEUE-9")); n == nil || n.Label != "orders queue" || edgeOf(graph, EdgeMapsTo, DependencyNodeID("kafka:orders"), n.ID) == nil {
		t.Fatalf("%+v", graph.Nodes)
	}
}

func TestGraphAmbiguousCandidatesAreKeptAndFlagged(t *testing.T) {
	input := graphFixture()
	input.Entities = append(input.Entities, GraphEntity{Provider: "appdynamics", ID: "tier:7", Name: "API.shop.local", Type: "tier"})
	input.Observations = []GraphObservation{{SourceID: "prom", Infrastructure: map[string][]string{"host": {"api.shop.local"}}}}
	graph, limitations := BuildGraph(input, nil)
	api := DependencyNodeID("http:https://api.shop.local:443/cart")
	var candidates []GraphEdge
	for _, e := range graph.Edges {
		if e.Kind == EdgeMapsTo && e.From == api {
			candidates = append(candidates, e)
		}
	}
	// The Dynatrace service and the AppDynamics tier are candidates; the host
	// seen only in point dimensions is not, because no decision can name it.
	if len(candidates) != 2 || !hasLimitation(limitations, LimitationGraphAmbiguous) {
		t.Fatalf("%+v %+v", candidates, limitations)
	}
	for _, e := range candidates {
		if !e.Ambiguous || e.Used || e.To == InfrastructureNodeID("host", "api.shop.local") {
			t.Fatalf("%+v", e)
		}
	}
}

func TestGraphAliasesAndPlaceholders(t *testing.T) {
	input := GraphInput{
		Entities: []GraphEntity{{Provider: "dynatrace", SourceID: "dt", ID: "smartscape-svc", Name: "cart", Type: "SERVICE", Aliases: []string{"SERVICE-ABC"}}},
		// A classic relation and the points name the service by its classic ID.
		Relations:    []GraphRelation{{Provider: "dynatrace", Kind: RelationCalls, From: "SERVICE-ABC", To: "SERVICE-DEF"}},
		Observations: []GraphObservation{{SourceID: "dt", Provider: "dynatrace", Entities: []string{"SERVICE-ABC"}}},
		Calls:        []GraphCall{{Action: "GET /", DependencyType: "http", Target: "def.local"}},
		Mappings:     []GraphMapping{{ID: "m", Revision: 1, Dependency: "http:def.local", Provider: "dynatrace", EntityID: "SERVICE-DEF", EntityName: "def service", Decision: MappingApproved}},
	}
	graph, _ := BuildGraph(input, nil)
	service := EntityNodeID("dynatrace", "smartscape-svc")
	if edgeOf(graph, EdgeCalls, service, EntityNodeID("dynatrace", "SERVICE-DEF")) == nil || edgeOf(graph, EdgeMonitors, "source:dt", service) == nil || nodeOf(graph, EntityNodeID("dynatrace", "SERVICE-ABC")) != nil {
		t.Fatalf("%+v", graph)
	}
	// The relation end was a placeholder until the decision named it.
	if n := nodeOf(graph, EntityNodeID("dynatrace", "SERVICE-DEF")); n == nil || n.Label != "def service" {
		t.Fatalf("%+v", n)
	}
}

func TestGraphCyclesAreDetectedAndAllowed(t *testing.T) {
	input := GraphInput{Relations: []GraphRelation{
		{Provider: "dynatrace", Kind: RelationCalls, From: "A", To: "B"},
		{Provider: "dynatrace", Kind: RelationCalls, From: "B", To: "C"},
		{Provider: "dynatrace", Kind: RelationCalls, From: "C", To: "A"},
		{Provider: "dynatrace", Kind: RelationCalls, From: "C", To: "D"},
		{Provider: "dynatrace", Kind: RelationCalls, From: "E", To: "E"},
	}}
	graph, _ := BuildGraph(input, nil)
	want := [][]string{
		{EntityNodeID("dynatrace", "A"), EntityNodeID("dynatrace", "B"), EntityNodeID("dynatrace", "C")},
		{EntityNodeID("dynatrace", "E")},
	}
	if !reflect.DeepEqual(graph.Cycles, want) || len(graph.Edges) != 5 {
		t.Fatalf("cycles %v, %d edges", graph.Cycles, len(graph.Edges))
	}
}

func TestGraphParentsObservationsAndBudgets(t *testing.T) {
	input := GraphInput{
		Sources: []GraphSource{{ID: "appd", Provider: "appdynamics"}, {ID: "prom", Provider: "prometheus"}},
		Entities: []GraphEntity{
			{Provider: "appdynamics", SourceID: "appd", ID: "tier:1", Name: "web", Type: "tier"},
			{Provider: "appdynamics", SourceID: "appd", ID: "node:5", Name: "web-1", Type: "node", ParentID: "tier:1"},
		},
		Observations: []GraphObservation{{SourceID: "prom", Provider: "prometheus", Infrastructure: map[string][]string{"pod": {"web-1-abc"}, "queue": {"orders"}}}},
		Calls:        []GraphCall{{Action: "GET /", Target: "web"}},
	}
	graph, _ := BuildGraph(input, nil)
	if edgeOf(graph, EdgeContains, EntityNodeID("appdynamics", "tier:1"), EntityNodeID("appdynamics", "node:5")) == nil ||
		edgeOf(graph, EdgeMonitors, "source:prom", InfrastructureNodeID("pod", "web-1-abc")) == nil ||
		edgeOf(graph, EdgeMonitors, "source:prom", InfrastructureNodeID("queue", "orders")) == nil {
		t.Fatalf("%+v", graph.Edges)
	}
	budget := DefaultBudget
	// Six nodes keep the action, its dependency, both sources and the AppDynamics
	// tier and node, which share five edges; one edge fits, the explicit call.
	budget.MaxGraphNodes, budget.MaxGraphEdges = 6, 1
	budgeter := NewBudgeter(budget, nil, nil)
	small, limitations := BuildGraph(input, budgeter)
	if len(small.Nodes) != 6 || len(small.Edges) != 1 || small.Edges[0].Kind != EdgeCalls || !hasLimitation(limitations, LimitationGraphTruncated) || small.Nodes[0].Kind != NodeAction || len(budgeter.Truncations()) != 2 {
		t.Fatalf("%+v %+v %+v", small, limitations, budgeter.Truncations())
	}
}

func TestGraphIsDeterministic(t *testing.T) {
	a, _ := BuildGraph(graphFixture(), nil)
	input := graphFixture()
	input.Calls[0], input.Calls[2] = input.Calls[2], input.Calls[0]
	input.Entities[0], input.Entities[2] = input.Entities[2], input.Entities[0]
	b, _ := BuildGraph(input, nil)
	first, _ := json.Marshal(a)
	second, _ := json.Marshal(b)
	if string(first) != string(second) {
		t.Fatalf("%s\n%s", first, second)
	}
}

func TestDependencyHost(t *testing.T) {
	for target, want := range map[string]string{
		"https://API.shop.local:443/cart": "api.shop.local",
		"user:pw@db.local:5432/orders":    "db.local",
		"http://[::1]:8080/x":             "::1",
		"orders":                          "orders",
		"broker.local:9092":               "broker.local",
		"":                                "",
		"has space":                       "",
	} {
		if got := DependencyHost(target); got != want {
			t.Errorf("%q: %q, want %q", target, got, want)
		}
	}
}
