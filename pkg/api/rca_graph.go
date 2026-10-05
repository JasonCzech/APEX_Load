package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Graph evidence limitations the archive adapter adds.
const limitationGraphEvidenceCut = "graph_evidence_cut"

// Bounds on the graph evidence one archive contributes, beyond the graph's
// own node and edge budgets.
const (
	maxGraphCalls              = 20000
	maxGraphObservedPerSource  = 1000
	maxRCAMappingsPerProject   = 1000
	rcaMappingDefinitionsPage  = 100
	maxRCAMappingFieldBytes    = 512
	maxRCAMappingEntityIDBytes = 256
)

// rcaMappingDefinition is the body of an rca_mapping analysis definition.
type rcaMappingDefinition struct {
	Dependency string `json:"dependency"`
	Provider   string `json:"provider"`
	EntityID   string `json:"entity_id"`
	EntityName string `json:"entity_name,omitempty"`
	Decision   string `json:"decision"`
}

func plainText(value string, limit int) bool {
	return value != "" && len(value) <= limit && strings.IndexFunc(value, unicode.IsControl) < 0
}

// validateRCAMappingDefinition checks a mapping decision: a dependency label
// as the graph names it, a governed provider, an entity ID and a decision.
func validateRCAMappingDefinition(raw json.RawMessage) error {
	var m rcaMappingDefinition
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid root-cause mapping")
	}
	switch {
	case !plainText(m.Dependency, maxRCAMappingFieldBytes):
		return errors.New("dependency must be a dependency label of at most 512 bytes")
	case !monitorprovider.KnownProviderName(m.Provider):
		return errors.New("provider must be a governed monitoring provider")
	case !plainText(m.EntityID, maxRCAMappingEntityIDBytes):
		return errors.New("entity_id must be at most 256 bytes")
	case m.EntityName != "" && !plainText(m.EntityName, maxRCAMappingEntityIDBytes):
		return errors.New("entity_name must be at most 256 bytes")
	case m.Decision != rca.MappingApproved && m.Decision != rca.MappingRejected:
		return errors.New("decision must be approved or rejected")
	}
	return nil
}

// loadRCAMappings reads a project's current mapping decisions, ordered by
// ID, and a digest that changes whenever any of them does. A project with
// none has an empty digest.
func loadRCAMappings(store any, project string) ([]rca.GraphMapping, string, error) {
	definitions, ok := store.(storage.AnalysisDefinitionStore)
	if !ok {
		return nil, "", nil
	}
	var mappings []rca.GraphMapping
	for offset := 0; offset < maxRCAMappingsPerProject; offset += rcaMappingDefinitionsPage {
		page, err := definitions.ListAnalysisDefinitions(project, storage.AnalysisDefinitionKindRCAMapping, rcaMappingDefinitionsPage, offset)
		if err != nil {
			return nil, "", err
		}
		for _, d := range page {
			var m rcaMappingDefinition
			if d.Deleted || json.Unmarshal(d.Definition, &m) != nil {
				continue
			}
			mappings = append(mappings, rca.GraphMapping{ID: d.ID, Revision: d.Revision, Dependency: m.Dependency, Provider: m.Provider, EntityID: m.EntityID, EntityName: m.EntityName, Decision: m.Decision})
		}
		if len(page) < rcaMappingDefinitionsPage {
			break
		}
	}
	if len(mappings) == 0 {
		return []rca.GraphMapping{}, "", nil
	}
	slices.SortFunc(mappings, func(a, b rca.GraphMapping) int { return strings.Compare(a.ID, b.ID) })
	body, err := json.Marshal(mappings)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(body)
	return mappings, hex.EncodeToString(sum[:]), nil
}

// graphCallCollector keeps a run's distinct archived calls, within bounds.
type graphCallCollector struct {
	calls     map[rca.GraphCall]bool
	truncated bool
}

func newGraphCallCollector() *graphCallCollector {
	return &graphCallCollector{calls: map[rca.GraphCall]bool{}}
}

// add records the call an archived row made: its action, the dependency it
// reached and the business transaction that enclosed it.
func (c *graphCallCollector) add(tx *TransactionEvent) {
	population := eventPopulation(tx.Population, tx.BusinessTransaction, tx.DependencyType)
	var call rca.GraphCall
	switch population {
	case "transaction":
		call = rca.GraphCall{Transaction: tx.ActionName}
	case "action":
		call = rca.GraphCall{Action: tx.ActionName, DependencyType: tx.DependencyType, Target: tx.DependencyTarget}
		if tx.SourceTransaction != tx.ActionName {
			call.Transaction = tx.SourceTransaction
		}
	default:
		return
	}
	if call.Action == "" && call.Transaction == "" || c.calls[call] {
		return
	}
	if len(c.calls) >= maxGraphCalls {
		c.truncated = true
		return
	}
	c.calls[call] = true
}

func (c *graphCallCollector) list() []rca.GraphCall {
	out := slices.Collect(maps.Keys(c.calls))
	slices.SortFunc(out, func(a, b rca.GraphCall) int {
		return strings.Compare(a.Transaction+"\x00"+a.Action+"\x00"+a.DependencyType+"\x00"+a.Target, b.Transaction+"\x00"+b.Action+"\x00"+b.DependencyType+"\x00"+b.Target)
	})
	return out
}

// graphObservationCollector records what each monitor source's points name:
// provider entities and infrastructure.
type graphObservationCollector struct {
	sources   map[string]*rca.GraphObservation
	seen      map[string]bool
	truncated bool
}

func newGraphObservationCollector() *graphObservationCollector {
	return &graphObservationCollector{sources: map[string]*rca.GraphObservation{}, seen: map[string]bool{}}
}

// infrastructureDimensions maps point dimensions to infrastructure types.
var infrastructureDimensions = map[string]string{
	"pod": "pod", "pod_name": "pod", "kubernetes_pod_name": "pod", "k8s_pod_name": "pod", "k8s.pod.name": "pod",
	"instance": "host", "host": "host", "hostname": "host", "host_name": "host", "host.name": "host",
	"node": "node", "kubernetes_node": "node", "k8s_node_name": "node", "k8s.node.name": "node",
	"queue": "queue", "queue_name": "queue", "topic": "queue", "destination": "queue",
}

func (c *graphObservationCollector) observe(point analysis.MetricPoint) {
	if point.Provider == nil || point.Source == "" {
		return
	}
	o := c.sources[point.Source]
	if o == nil {
		o = &rca.GraphObservation{SourceID: point.Source, Provider: point.Provider.Name, Infrastructure: map[string][]string{}}
		c.sources[point.Source] = o
	}
	add := func(key string, record func()) {
		if c.seen[point.Source+"\x00"+key] {
			return
		}
		if len(o.Entities)+infrastructureCount(o) >= maxGraphObservedPerSource {
			c.truncated = true
			return
		}
		c.seen[point.Source+"\x00"+key] = true
		record()
	}
	for _, key := range slices.Sorted(maps.Keys(point.Provider.EntityIDs)) {
		id := monitorEntityKey(point.Provider.Name, key, point.Provider.EntityIDs[key])
		if id != "" {
			add("entity\x00"+id, func() { o.Entities = append(o.Entities, id) })
		}
	}
	for _, key := range slices.Sorted(maps.Keys(point.Dimensions)) {
		kind, ok := infrastructureDimensions[strings.ToLower(key)]
		if !ok {
			continue
		}
		value := strings.TrimSpace(point.Dimensions[key])
		if kind == "host" {
			value = rca.DependencyHost(value)
		}
		if value != "" {
			add("infra\x00"+kind+"\x00"+value, func() { o.Infrastructure[kind] = append(o.Infrastructure[kind], value) })
		}
	}
}

func infrastructureCount(o *rca.GraphObservation) int {
	n := 0
	for _, values := range o.Infrastructure {
		n += len(values)
	}
	return n
}

func (c *graphObservationCollector) list() []rca.GraphObservation {
	out := make([]rca.GraphObservation, 0, len(c.sources))
	for _, id := range slices.Sorted(maps.Keys(c.sources)) {
		out = append(out, *c.sources[id])
	}
	return out
}

// monitorEntityKey names the provider entity a point's entity ID refers to,
// in the form archiveGraphTopology gives that provider's entities.
func monitorEntityKey(provider, key, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if provider == monitorprovider.ProviderAppDynamics {
		return key + ":" + value
	}
	return value
}

// coverageProvider is the governed provider a monitor job's coverage
// describes, or "" when it is not one with topology.
func coverageProvider(coverage RunMonitorCoverage) string {
	switch {
	case coverage.Dynatrace != nil:
		return monitorprovider.ProviderDynatrace
	case coverage.PrometheusSelection != nil:
		return monitorprovider.ProviderPrometheus
	case coverage.Selection != nil:
		return monitorprovider.ProviderAppDynamics
	}
	return ""
}

// infrastructureEntityTypes are the provider entity types where software
// runs: hosts, processes, containers, pods, nodes and queues.
var infrastructureEntityTypes = map[string]bool{
	"HOST": true, "PROCESS_GROUP_INSTANCE": true, "PROCESS": true, "CONTAINER": true, "CONTAINER_GROUP_INSTANCE": true,
	"KUBERNETES_NODE": true, "KUBERNETES_CLUSTER": true, "KUBERNETES_POD": true, "CLOUD_APPLICATION_INSTANCE": true,
	"K8S_POD": true, "K8S_NODE": true, "K8S_CLUSTER": true, "QUEUE": true, "MACHINE": true,
}

func infrastructureEntity(kind string) bool {
	return infrastructureEntityTypes[strings.ToUpper(strings.TrimSpace(kind))]
}

// dynatraceIDType is the entity type a Dynatrace ID's prefix names
// (PROCESS_GROUP_INSTANCE-1A2B), or "".
func dynatraceIDType(id string) string {
	if i := strings.LastIndex(id, "-"); i > 0 {
		return id[:i]
	}
	return ""
}

// normalizeRelation maps a provider relationship name to a graph relation
// and whether its ends must be swapped so the relation reads from caller to
// callee, from whole to part, or from software to where it runs.
func normalizeRelation(kind string) (string, bool) {
	k := strings.ToLower(strings.NewReplacer("_", "", ".", "", "-", "").Replace(kind))
	switch {
	case strings.Contains(k, "calledby"):
		return rca.RelationCalls, true
	case strings.Contains(k, "call"):
		return rca.RelationCalls, false
	case strings.HasPrefix(k, "runson"):
		return rca.RelationRunsOn, false
	case k == "runs" || strings.HasPrefix(k, "hosts"):
		return rca.RelationRunsOn, true
	case strings.HasPrefix(k, "isinstanceof"), strings.HasPrefix(k, "instanceof"), strings.HasPrefix(k, "belongsto"),
		strings.HasPrefix(k, "ispartof"), strings.HasPrefix(k, "partof"), strings.HasPrefix(k, "isprocessof"), strings.HasPrefix(k, "ischildof"):
		return rca.RelationContains, true
	case strings.HasPrefix(k, "contains"), strings.HasPrefix(k, "has"), strings.HasPrefix(k, "isparentof"):
		return rca.RelationContains, false
	}
	return "", false
}

// archiveGraphTopology turns the archived monitor jobs' topology into graph
// sources, entities and provider relations.
func archiveGraphTopology(manifest *RunArchiveManifest) ([]rca.GraphSource, []rca.GraphEntity, []rca.GraphRelation) {
	var sources []rca.GraphSource
	var entities []rca.GraphEntity
	var relations []rca.GraphRelation
	relate := func(provider, source, kind, from, to, fromType, toType string) {
		relation, reversed := normalizeRelation(kind)
		if relation == "" || from == "" || to == "" {
			return
		}
		// A classic relationship end names only an ID, whose prefix is its type.
		fromType, toType = firstNonEmpty(fromType, dynatraceIDType(from)), firstNonEmpty(toType, dynatraceIDType(to))
		if reversed {
			from, to, fromType, toType = to, from, toType, fromType
		}
		relations = append(relations, rca.GraphRelation{Provider: provider, SourceID: source, Kind: relation, From: from, To: to, FromType: fromType, ToType: toType,
			FromInfrastructure: infrastructureEntity(fromType), ToInfrastructure: infrastructureEntity(toType)})
	}
	for _, coverage := range manifest.MonitorCoverage {
		provider := coverageProvider(coverage)
		if coverage.SourceID != "" {
			sources = append(sources, rca.GraphSource{ID: coverage.SourceID, Provider: provider})
		}
		if provider == "" {
			continue
		}
		for _, e := range coverage.Topology {
			switch provider {
			case monitorprovider.ProviderDynatrace:
				if e.Type == "relationship" {
					relate(provider, coverage.SourceID, e.Name, e.EntityIDs["source_id"], e.EntityIDs["target_id"], e.EntityIDs["source_type"], e.EntityIDs["target_type"])
					continue
				}
				if e.ID == "" {
					continue
				}
				entity := rca.GraphEntity{Provider: provider, SourceID: coverage.SourceID, ID: e.ID, Name: e.Name, Type: e.Type, Infrastructure: infrastructureEntity(e.Type)}
				// Smartscape entities keep their classic ID, which points and
				// classic relationships use.
				if classic := e.EntityIDs["classic_id"]; classic != "" && classic != e.ID {
					entity.Aliases = []string{classic}
				}
				entities = append(entities, entity)
				// Classic entities carry their relationships as from.<kind>.<n>
				// and to.<kind>.<n> entity IDs.
				for _, key := range slices.Sorted(maps.Keys(e.EntityIDs)) {
					parts := strings.SplitN(key, ".", 3)
					if len(parts) != 3 {
						continue
					}
					switch parts[0] {
					case "from":
						relate(provider, coverage.SourceID, parts[1], e.ID, e.EntityIDs[key], e.Type, "")
					case "to":
						relate(provider, coverage.SourceID, parts[1], e.EntityIDs[key], e.ID, "", e.Type)
					}
				}
			case monitorprovider.ProviderAppDynamics:
				if e.ID == "" || e.Type == "" {
					continue
				}
				// AppDynamics IDs are unique per entity type.
				parent := ""
				switch e.Type {
				case "node", "business_transaction":
					if tier := firstNonEmpty(e.EntityIDs["tier"], e.ParentID); tier != "" {
						parent = "tier:" + tier
					}
				case "tier":
					if application := firstNonEmpty(e.EntityIDs["application"], e.ParentID); application != "" {
						parent = "application:" + application
					}
				}
				entities = append(entities, rca.GraphEntity{Provider: provider, SourceID: coverage.SourceID, ID: e.Type + ":" + e.ID, Name: e.Name, Type: e.Type, ParentID: parent, Infrastructure: infrastructureEntity(e.Type)})
			}
		}
	}
	return sources, entities, relations
}

// archiveGraphInput gathers a run's frozen graph evidence.
func archiveGraphInput(manifest *RunArchiveManifest, calls *graphCallCollector, observations *graphObservationCollector, mappings []rca.GraphMapping) rca.GraphInput {
	input := rca.GraphInput{Calls: calls.list(), Observations: observations.list(), Mappings: mappings}
	input.Sources, input.Entities, input.Relations = archiveGraphTopology(manifest)
	if calls.truncated || observations.truncated {
		input.Limitations = append(input.Limitations, rca.Limitation{Code: limitationGraphEvidenceCut, Detail: "the run's distinct calls or a source's observed entities exceeded their bounds"})
	}
	return input
}
