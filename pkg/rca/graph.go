package rca

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// GraphSchemaVersion is the per-run graph contract.
const GraphSchemaVersion = "apexload.rca_graph.v1"

// Node kinds.
const (
	NodeTransaction    = "transaction"
	NodeAction         = "action"
	NodeDependency     = "dependency"
	NodeMonitorSource  = "monitor_source"
	NodeProviderEntity = "provider_entity"
	NodeInfrastructure = "infrastructure"
)

// Edge kinds. contains links a whole to a part of it: a transaction to its
// actions, a tier to its nodes, a process group to its instances.
const (
	EdgeCalls                = "calls"
	EdgeContains             = "contains"
	EdgeMapsTo               = "maps_to"
	EdgeMonitors             = "monitors"
	EdgeSharesInfrastructure = "shares_infrastructure"
)

// Edge confidence: what the run recorded or an operator approved, what a
// provider reported, or a name match that stays unused until approved.
const (
	ConfidenceExplicit = "explicit"
	ConfidenceProvider = "provider"
	ConfidenceInferred = "inferred"
)

// Mapping decisions an operator records for a dependency and an entity.
const (
	MappingApproved = "approved"
	MappingRejected = "rejected"
)

// Provider relation kinds the graph understands; adapters normalize to them.
const (
	RelationCalls    = "calls"
	RelationContains = "contains"
	RelationRunsOn   = "runs_on"
)

// Graph limitations.
const (
	LimitationGraphTruncated  = "graph_truncated"
	LimitationGraphUnapproved = "graph_unapproved_mappings"
	LimitationGraphAmbiguous  = "graph_ambiguous_mappings"
)

// MaxGraphBytes bounds the graph an assessment stores.
const MaxGraphBytes = 1 << 20

// Edge provenance.
const (
	ProvenanceArchiveRows   = "archive_rows"
	ProvenanceTopology      = "monitor_topology"
	ProvenanceMonitorPoints = "monitor_points"
	ProvenanceNameMatch     = "name_match"
	ProvenanceMapping       = "mapping"
)

// GraphNode is one thing the run touched or a provider reported.
type GraphNode struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Label      string            `json:"label"`
	Provider   string            `json:"provider,omitempty"`
	Type       string            `json:"type,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// GraphEdge is one typed relation, with where it came from and how sure it
// is.
type GraphEdge struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	From       string `json:"from"`
	To         string `json:"to"`
	Confidence string `json:"confidence"`
	Provenance string `json:"provenance"`
	// Revision is the approved mapping's revision for a mapped edge.
	Revision string `json:"revision,omitempty"`
	// Used is false for an inferred edge awaiting approval: it is listed but
	// never walked.
	Used bool `json:"used"`
	// Ambiguous marks one of several candidates for the same dependency.
	Ambiguous bool `json:"ambiguous,omitempty"`
}

// Graph is a run's typed topology. Cycles are the sets of nodes that call
// each other through used calls edges; they are allowed and listed.
type Graph struct {
	Schema string      `json:"schema"`
	Nodes  []GraphNode `json:"nodes"`
	Edges  []GraphEdge `json:"edges"`
	Cycles [][]string  `json:"cycles"`
}

// GraphCall is one archived call: an action and the dependency it reached,
// and the business transaction that enclosed the action when recorded.
type GraphCall struct {
	Transaction    string
	Action         string
	DependencyType string
	Target         string
}

// GraphSource is a monitor source bound to the run.
type GraphSource struct {
	ID       string
	Provider string
	Label    string
}

// GraphEntity is a provider entity. ID and ParentID are unique within the
// provider; Infrastructure marks hosts, pods, processes and queues.
type GraphEntity struct {
	Provider       string
	SourceID       string
	ID             string
	Name           string
	Type           string
	ParentID       string
	Infrastructure bool
	Attributes     map[string]string
	// Aliases are other IDs the provider uses for the same entity, such as
	// a Dynatrace classic ID beside its Smartscape ID.
	Aliases []string
}

// GraphRelation is a provider-reported relation between two of its
// entities, with each end's type and whether it is infrastructure.
type GraphRelation struct {
	Provider           string
	SourceID           string
	Kind               string
	From, To           string
	FromType, ToType   string
	FromInfrastructure bool
	ToInfrastructure   bool
}

// GraphObservation is what one monitor source measured: provider entities
// and infrastructure named in its points.
type GraphObservation struct {
	SourceID string
	Provider string
	Entities []string
	// Infrastructure maps an infrastructure type (host, pod, node, queue) to
	// the values seen.
	Infrastructure map[string][]string
}

// GraphMapping is an operator's decision that a dependency is, or is not, a
// provider entity, frozen with its revision.
type GraphMapping struct {
	ID         string `json:"id"`
	Revision   int64  `json:"revision"`
	Dependency string `json:"dependency"`
	Provider   string `json:"provider"`
	EntityID   string `json:"entity_id"`
	EntityName string `json:"entity_name,omitempty"`
	Decision   string `json:"decision"`
}

// GraphInput is the frozen evidence a run's graph is built from.
type GraphInput struct {
	Calls        []GraphCall
	Sources      []GraphSource
	Entities     []GraphEntity
	Relations    []GraphRelation
	Observations []GraphObservation
	Mappings     []GraphMapping
	// Limitations the caller found while collecting the evidence.
	Limitations []Limitation
}

// Node IDs. A transaction or action node shares its name with its detection
// series (transaction/<name>/..., action/<name>/...).
func TransactionNodeID(name string) string { return NodeTransaction + ":" + name }
func ActionNodeID(name string) string      { return NodeAction + ":" + name }

// DependencyLabel names a dependency by type and target.
func DependencyLabel(kind, target string) string {
	if kind == "" {
		return target
	}
	return kind + ":" + target
}
func DependencyNodeID(label string) string { return NodeDependency + ":" + label }

// entityNodePrefix starts every provider entity's node ID.
const entityNodePrefix = "entity:"

func EntityNodeID(provider, id string) string {
	return entityNodePrefix + provider + ":" + id
}
func InfrastructureNodeID(kind, value string) string {
	return NodeInfrastructure + ":" + kind + ":" + value
}
func sourceNodeID(id string) string { return "source:" + id }

// DependencyHost is the host a dependency target names, lower-cased and
// without a scheme, credentials or port, or "" when it names none.
func DependencyHost(target string) string {
	host := strings.TrimSpace(target)
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	if strings.HasPrefix(host, "[") {
		// A bracketed IPv6 address, with or without a port.
		if end := strings.Index(host, "]"); end > 0 {
			host = host[1:end]
		}
	} else if strings.Count(host, ":") == 1 {
		host = host[:strings.Index(host, ":")]
	}
	if host == "" || strings.ContainsAny(host, " \t") {
		return ""
	}
	return strings.ToLower(host)
}

type graphBuilder struct {
	nodes map[string]*GraphNode
	edges map[string]*GraphEdge
	// aliases maps an alias's node ID to the entity's node ID.
	aliases map[string]string
	// placeholders are entity nodes known only by ID, from a relation end
	// or a mapping, until something describes them.
	placeholders map[string]bool
}

func (b *graphBuilder) node(n GraphNode) *GraphNode {
	if existing := b.nodes[n.ID]; existing != nil {
		if existing.Type == "" {
			existing.Type = n.Type
		}
		return existing
	}
	created := n
	b.nodes[n.ID] = &created
	return &created
}

func (b *graphBuilder) edge(e GraphEdge) {
	if e.From == "" || e.To == "" {
		return
	}
	e.ID = e.Kind + ":" + e.From + "->" + e.To
	if existing := b.edges[e.ID]; existing != nil {
		// A stronger source of the same relation wins.
		if confidenceRank(e.Confidence) < confidenceRank(existing.Confidence) {
			*existing = e
		}
		return
	}
	created := e
	b.edges[e.ID] = &created
}

func confidenceRank(c string) int {
	switch c {
	case ConfidenceExplicit:
		return 0
	case ConfidenceProvider:
		return 1
	}
	return 2
}

func nodeKindRank(kind string) int {
	switch kind {
	case NodeTransaction:
		return 0
	case NodeAction:
		return 1
	case NodeDependency:
		return 2
	case NodeMonitorSource:
		return 3
	case NodeProviderEntity:
		return 4
	}
	return 5
}

// entityID resolves a provider entity ID, or one of its aliases, to its node
// ID.
func (b *graphBuilder) entityID(provider, id string) string {
	nodeID := EntityNodeID(provider, id)
	if target, ok := b.aliases[nodeID]; ok {
		return target
	}
	return nodeID
}

// entityNode adds a provider entity or infrastructure node. Without a label
// the node is a placeholder; a later description fills it in.
func (b *graphBuilder) entityNode(provider, id, label, kindType string, infrastructure bool) string {
	nodeID := b.entityID(provider, id)
	kind := NodeProviderEntity
	if infrastructure {
		kind = NodeInfrastructure
	}
	existing := b.nodes[nodeID]
	if existing == nil {
		placeholder := label == ""
		if placeholder {
			label = id
		}
		b.node(GraphNode{ID: nodeID, Kind: kind, Label: label, Provider: provider, Type: kindType})
		b.placeholders[nodeID] = placeholder
		return nodeID
	}
	if b.placeholders[nodeID] && label != "" {
		existing.Label = label
		b.placeholders[nodeID] = false
	}
	if existing.Type == "" {
		existing.Type = kindType
	}
	if infrastructure {
		existing.Kind = NodeInfrastructure
	}
	return nodeID
}

// BuildGraph builds a run's typed graph from frozen evidence, within the
// graph budgets.
func BuildGraph(input GraphInput, budgeter *Budgeter) (Graph, []Limitation) {
	if budgeter == nil {
		budgeter = NewBudgeter(DefaultBudget, nil, nil)
	}
	limitations := append([]Limitation{}, input.Limitations...)
	b := &graphBuilder{nodes: map[string]*GraphNode{}, edges: map[string]*GraphEdge{}, aliases: map[string]string{}, placeholders: map[string]bool{}}

	// What the run recorded: actions, the dependencies they reached and the
	// transactions that enclosed them.
	for _, call := range input.Calls {
		if call.Action == "" {
			if call.Transaction != "" {
				b.node(GraphNode{ID: TransactionNodeID(call.Transaction), Kind: NodeTransaction, Label: call.Transaction})
			}
			continue
		}
		action := b.node(GraphNode{ID: ActionNodeID(call.Action), Kind: NodeAction, Label: call.Action}).ID
		if call.Transaction != "" && call.Transaction != call.Action {
			transaction := b.node(GraphNode{ID: TransactionNodeID(call.Transaction), Kind: NodeTransaction, Label: call.Transaction}).ID
			b.edge(GraphEdge{Kind: EdgeContains, From: transaction, To: action, Confidence: ConfidenceExplicit, Provenance: ProvenanceArchiveRows, Used: true})
		}
		if call.Target == "" {
			continue
		}
		label := DependencyLabel(call.DependencyType, call.Target)
		attributes := map[string]string{"target": call.Target}
		if call.DependencyType != "" {
			attributes["type"] = call.DependencyType
		}
		if host := DependencyHost(call.Target); host != "" {
			attributes["host"] = host
		}
		dependency := b.node(GraphNode{ID: DependencyNodeID(label), Kind: NodeDependency, Label: label, Type: call.DependencyType, Attributes: attributes}).ID
		b.edge(GraphEdge{Kind: EdgeCalls, From: action, To: dependency, Confidence: ConfidenceExplicit, Provenance: ProvenanceArchiveRows, Used: true})
	}

	// What providers reported.
	for _, source := range input.Sources {
		if source.ID == "" {
			continue
		}
		label := source.Label
		if label == "" {
			label = source.ID
		}
		b.node(GraphNode{ID: sourceNodeID(source.ID), Kind: NodeMonitorSource, Label: label, Provider: source.Provider})
	}
	monitors := func(sourceID, to string, provenance string) {
		if sourceID == "" {
			return
		}
		from := sourceNodeID(sourceID)
		if b.nodes[from] == nil {
			b.node(GraphNode{ID: from, Kind: NodeMonitorSource, Label: sourceID})
		}
		b.edge(GraphEdge{Kind: EdgeMonitors, From: from, To: to, Confidence: ConfidenceProvider, Provenance: provenance, Used: true})
	}
	for _, e := range input.Entities {
		for _, alias := range e.Aliases {
			if alias != "" && alias != e.ID && e.Provider != "" {
				b.aliases[EntityNodeID(e.Provider, alias)] = EntityNodeID(e.Provider, e.ID)
			}
		}
	}
	for _, e := range input.Entities {
		if e.ID == "" || e.Provider == "" {
			continue
		}
		name := e.Name
		if name == "" {
			name = e.ID
		}
		id := b.entityNode(e.Provider, e.ID, name, e.Type, e.Infrastructure)
		if len(e.Attributes) > 0 {
			node := b.nodes[id]
			if node.Attributes == nil {
				node.Attributes = map[string]string{}
			}
			for k, v := range e.Attributes {
				node.Attributes[k] = v
			}
		}
		monitors(e.SourceID, id, ProvenanceTopology)
	}
	for _, e := range input.Entities {
		if parent := b.entityID(e.Provider, e.ParentID); e.ParentID != "" && b.nodes[parent] != nil {
			b.edge(GraphEdge{Kind: EdgeContains, From: parent, To: b.entityID(e.Provider, e.ID), Confidence: ConfidenceProvider, Provenance: ProvenanceTopology, Used: true})
		}
	}
	for _, r := range input.Relations {
		if r.From == "" || r.To == "" || r.Provider == "" {
			continue
		}
		from := b.entityNode(r.Provider, r.From, "", r.FromType, r.FromInfrastructure)
		to := b.entityNode(r.Provider, r.To, "", r.ToType, r.ToInfrastructure)
		kind := ""
		switch r.Kind {
		case RelationCalls:
			kind = EdgeCalls
		case RelationContains:
			kind = EdgeContains
		case RelationRunsOn:
			kind = EdgeSharesInfrastructure
		default:
			continue
		}
		b.edge(GraphEdge{Kind: kind, From: from, To: to, Confidence: ConfidenceProvider, Provenance: ProvenanceTopology, Used: true})
	}
	for _, o := range input.Observations {
		for _, entity := range o.Entities {
			if id := b.entityID(o.Provider, entity); b.nodes[id] != nil {
				monitors(o.SourceID, id, ProvenanceMonitorPoints)
			}
		}
		for kind, values := range o.Infrastructure {
			for _, value := range values {
				if value == "" {
					continue
				}
				id := b.node(GraphNode{ID: InfrastructureNodeID(kind, value), Kind: NodeInfrastructure, Label: value, Type: kind}).ID
				monitors(o.SourceID, id, ProvenanceMonitorPoints)
			}
		}
	}

	unapproved, ambiguous := b.mapDependencies(input.Mappings)
	if unapproved > 0 {
		limitations = append(limitations, Limitation{Code: LimitationGraphUnapproved, Detail: fmt.Sprintf("%d dependencies have inferred entity matches awaiting approval; they are not used", unapproved)})
	}
	if ambiguous > 0 {
		limitations = append(limitations, Limitation{Code: LimitationGraphAmbiguous, Detail: fmt.Sprintf("%d dependencies match more than one entity", ambiguous)})
	}

	graph, cut := b.bounded(budgeter)
	if cut != "" {
		limitations = append(limitations, Limitation{Code: LimitationGraphTruncated, Detail: cut})
	}
	graph.Cycles = callCycles(graph)
	return graph, limitations
}

// mapDependencies links dependencies to provider entities: approved mappings
// as explicit edges; otherwise entities or infrastructure whose name is the
// dependency's host, as inferred edges left unused. It returns how many
// dependencies have unapproved candidates and how many have several.
func (b *graphBuilder) mapDependencies(mappings []GraphMapping) (int, int) {
	approved := map[string][]GraphMapping{}
	rejected := map[string]bool{}
	for _, m := range mappings {
		switch m.Decision {
		case MappingApproved:
			approved[m.Dependency] = append(approved[m.Dependency], m)
		case MappingRejected:
			rejected[m.Dependency+"\x00"+b.entityID(m.Provider, m.EntityID)] = true
		}
	}
	// Candidate targets by lower-cased name: provider entities, including
	// provider-reported infrastructure, which a decision can name. Hosts and
	// pods seen only in point dimensions cannot be decided, so they are not
	// candidates.
	byName := map[string][]string{}
	for id, n := range b.nodes {
		if !strings.HasPrefix(id, entityNodePrefix) {
			continue
		}
		names := []string{strings.ToLower(n.Label)}
		if host := n.Attributes["host"]; host != "" {
			names = append(names, strings.ToLower(host))
		}
		for _, name := range names {
			if name != "" {
				byName[name] = append(byName[name], n.ID)
			}
		}
	}
	unapproved, ambiguous := 0, 0
	for _, id := range sortedNodeIDs(b.nodes, NodeDependency) {
		dependency := b.nodes[id]
		if decisions := approved[dependency.Label]; len(decisions) > 0 {
			for _, m := range decisions {
				label := m.EntityName
				target := b.entityNode(m.Provider, m.EntityID, label, "", false)
				b.edge(GraphEdge{Kind: EdgeMapsTo, From: id, To: target, Confidence: ConfidenceExplicit, Provenance: ProvenanceMapping + ":" + m.ID, Revision: strconv.FormatInt(m.Revision, 10), Used: true})
			}
			continue
		}
		host := dependency.Attributes["host"]
		if host == "" {
			continue
		}
		var candidates []string
		for _, target := range uniqueSorted(byName[host]) {
			if !rejected[dependency.Label+"\x00"+target] {
				candidates = append(candidates, target)
			}
		}
		for _, target := range candidates {
			b.edge(GraphEdge{Kind: EdgeMapsTo, From: id, To: target, Confidence: ConfidenceInferred, Provenance: ProvenanceNameMatch, Used: false, Ambiguous: len(candidates) > 1})
		}
		if len(candidates) > 0 {
			unapproved++
		}
		if len(candidates) > 1 {
			ambiguous++
		}
	}
	return unapproved, ambiguous
}

func sortedNodeIDs(nodes map[string]*GraphNode, kind string) []string {
	var ids []string
	for id, n := range nodes {
		if n.Kind == kind {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func uniqueSorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	unique := out[:0]
	for i, v := range out {
		if i == 0 || v != out[i-1] {
			unique = append(unique, v)
		}
	}
	return unique
}

// bounded orders the graph and keeps what the node, edge and byte budgets
// allow: the run's own nodes first, then sources, entities and
// infrastructure; explicit edges before provider and inferred ones. It
// describes what it cut, or returns "".
func (b *graphBuilder) bounded(budgeter *Budgeter) (Graph, string) {
	nodes := make([]GraphNode, 0, len(b.nodes))
	for _, n := range b.nodes {
		nodes = append(nodes, *n)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if ri, rj := nodeKindRank(nodes[i].Kind), nodeKindRank(nodes[j].Kind); ri != rj {
			return ri < rj
		}
		return nodes[i].ID < nodes[j].ID
	})
	edges := make([]GraphEdge, 0, len(b.edges))
	for _, e := range b.edges {
		edges = append(edges, *e)
	}
	sortEdges(edges)
	var cuts []string
	if keep := budgeter.GraphNodes(len(nodes)); keep < len(nodes) {
		cuts = append(cuts, fmt.Sprintf("%d of %d nodes", keep, len(nodes)))
		nodes = nodes[:keep]
	}
	edges = edgesWithin(edges, nodes)
	if keep := budgeter.GraphEdges(len(edges)); keep < len(edges) {
		cuts = append(cuts, fmt.Sprintf("%d of %d edges", keep, len(edges)))
		edges = edges[:keep]
	}
	graph := Graph{Schema: GraphSchemaVersion, Nodes: nodes, Edges: edges}
	size := func() int {
		body, _ := json.Marshal(graph)
		return len(body)
	}
	if size() > MaxGraphBytes {
		offered := len(graph.Edges)
		for size() > MaxGraphBytes && len(graph.Edges) > 0 {
			graph.Edges = graph.Edges[:len(graph.Edges)/2]
		}
		offeredNodes := len(graph.Nodes)
		for size() > MaxGraphBytes && len(graph.Nodes) > 0 {
			graph.Nodes = graph.Nodes[:len(graph.Nodes)/2]
			graph.Edges = edgesWithin(graph.Edges, graph.Nodes)
		}
		cuts = append(cuts, fmt.Sprintf("%d of %d edges and %d of %d nodes fit %d bytes", len(graph.Edges), offered, len(graph.Nodes), offeredNodes, MaxGraphBytes))
	}
	if graph.Nodes == nil {
		graph.Nodes = []GraphNode{}
	}
	if graph.Edges == nil {
		graph.Edges = []GraphEdge{}
	}
	return graph, strings.Join(cuts, "; ")
}

func sortEdges(edges []GraphEdge) {
	sort.Slice(edges, func(i, j int) bool {
		if ri, rj := confidenceRank(edges[i].Confidence), confidenceRank(edges[j].Confidence); ri != rj {
			return ri < rj
		}
		return edges[i].ID < edges[j].ID
	})
}

// edgesWithin keeps the edges whose ends are both among nodes.
func edgesWithin(edges []GraphEdge, nodes []GraphNode) []GraphEdge {
	kept := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		kept[n.ID] = true
	}
	out := edges[:0:0]
	for _, e := range edges {
		if kept[e.From] && kept[e.To] {
			out = append(out, e)
		}
	}
	return out
}

// callCycles finds the strongly connected sets of nodes joined by used calls
// edges (Tarjan), each sorted, largest first; a node that calls itself is a
// cycle of one.
func callCycles(graph Graph) [][]string {
	next := map[string][]string{}
	self := map[string]bool{}
	for _, e := range graph.Edges {
		if e.Kind != EdgeCalls || !e.Used {
			continue
		}
		if e.From == e.To {
			self[e.From] = true
			continue
		}
		next[e.From] = append(next[e.From], e.To)
	}
	for id := range next {
		sort.Strings(next[id])
	}
	index, lowlink := map[string]int{}, map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var cycles [][]string
	counter := 0
	var visit func(string)
	visit = func(v string) {
		index[v], lowlink[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range next[v] {
			if _, seen := index[w]; !seen {
				visit(w)
				lowlink[v] = min(lowlink[v], lowlink[w])
			} else if onStack[w] {
				lowlink[v] = min(lowlink[v], index[w])
			}
		}
		if lowlink[v] == index[v] {
			var component []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				component = append(component, w)
				if w == v {
					break
				}
			}
			if len(component) > 1 || self[v] {
				sort.Strings(component)
				cycles = append(cycles, component)
			}
		}
	}
	var starts []string
	for _, n := range graph.Nodes {
		starts = append(starts, n.ID)
	}
	for _, v := range starts {
		if _, seen := index[v]; !seen {
			visit(v)
		}
	}
	sort.Slice(cycles, func(i, j int) bool {
		if len(cycles[i]) != len(cycles[j]) {
			return len(cycles[i]) > len(cycles[j])
		}
		return cycles[i][0] < cycles[j][0]
	})
	if cycles == nil {
		cycles = [][]string{}
	}
	return cycles
}
