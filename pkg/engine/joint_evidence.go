package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Joint evidence v4 (B5). A run chooses, before Start, up to three grouping
// dimensions of one population (actions or business transactions); every
// sample is then counted under the exact tuple of its values for those
// dimensions, so an intersection is measured rather than inferred from
// independent marginals. A publication keeps at most JointMaxKeys tuples:
// the rest are folded into one explicit "other" bucket and the publication
// is marked incomplete, never silently truncated.

const (
	JointEvidenceVersion = 4
	JointMaxDimensions   = 3
	JointMaxKeys         = 512
	jointMaxLabelBytes   = 256
	jointMaxKeyBytes     = 1024
	// jointOmittedTracking bounds the memory spent counting distinct
	// omitted tuples; past it the count is a lower bound.
	jointOmittedTracking = 4096
	jointPropertyPrefix  = "property:"
	jointPropertyMaxName = 128
)

// JointPopulations are the populations a projection can select.
var JointPopulations = []string{"action", "transaction"}

// JointDimensions are the projectable dimensions besides property:<name>.
var JointDimensions = []string{"name", "protocol", "group", "region", "dependency", "page", "hierarchy", "phase", "network_profile", "browser_profile", "browser_engine", "browser_platform"}

// JointProjection is a run's frozen choice of joint evidence.
type JointProjection struct {
	Population string   `json:"population"`
	Dimensions []string `json:"dimensions"`
}

// Validate checks a projection: one population, 1 to 3 distinct
// dimensions, at most one promoted property category.
func (p JointProjection) Validate() error {
	if p.Population != "action" && p.Population != "transaction" {
		return errors.New("joint evidence population must be action or transaction")
	}
	if len(p.Dimensions) == 0 || len(p.Dimensions) > JointMaxDimensions {
		return fmt.Errorf("joint evidence needs 1 to %d dimensions", JointMaxDimensions)
	}
	seen, properties := map[string]bool{}, 0
	for _, dimension := range p.Dimensions {
		if seen[dimension] {
			return fmt.Errorf("joint evidence dimension %q is repeated", dimension)
		}
		seen[dimension] = true
		if name, ok := strings.CutPrefix(dimension, jointPropertyPrefix); ok {
			properties++
			if name == "" || len(name) > jointPropertyMaxName || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\n") {
				return errors.New("joint evidence property category is invalid")
			}
			continue
		}
		if !slices.Contains(JointDimensions, dimension) {
			return fmt.Errorf("joint evidence dimension %q is not supported", dimension)
		}
	}
	if properties > 1 {
		return errors.New("joint evidence projects at most one property category")
	}
	return nil
}

// JointPropertyCategory reports whether a projected dimension is a promoted
// property category, and its name.
func JointPropertyCategory(dimension string) (string, bool) {
	return strings.CutPrefix(dimension, jointPropertyPrefix)
}

// Clone copies a projection.
func (p *JointProjection) Clone() *JointProjection {
	if p == nil {
		return nil
	}
	out := *p
	out.Dimensions = append([]string(nil), p.Dimensions...)
	return &out
}

// tuple returns a result's values for the projection, or false when the
// result is outside the projected population. A dimension the result does
// not carry is the empty label: the sample is still counted, under the
// tuple that says so.
func (p *JointProjection) tuple(r *protocol.Result, groupProtocols map[string]string) ([]string, bool) {
	if p == nil || r == nil || r.BusinessTransaction != (p.Population == "transaction") {
		return nil, false
	}
	values := make([]string, len(p.Dimensions))
	for i, dimension := range p.Dimensions {
		var value string
		switch dimension {
		case "name":
			value = r.ActionName
		case "protocol":
			value = groupProtocols[r.Group]
		case "group":
			value = r.Group
		case "region":
			value = r.Region
		case "dependency":
			if strings.TrimSpace(r.DependencyTarget) != "" {
				value = strings.TrimSpace(r.DependencyType) + "/" + strings.TrimSpace(r.DependencyTarget)
			}
		case "page":
			value = r.PageName
		case "hierarchy":
			value = r.Hierarchy
		case "phase":
			value = r.Phase
		case "network_profile":
			value = r.NetworkProfile
		case "browser_profile", "browser_engine", "browser_platform":
			if r.ClientMetrics != nil {
				value = map[string]string{"browser_profile": r.ClientMetrics.DeviceProfile, "browser_engine": r.ClientMetrics.BrowserEngine, "browser_platform": r.ClientMetrics.DevicePlatform}[dimension]
			}
		default:
			if name, ok := strings.CutPrefix(dimension, jointPropertyPrefix); ok {
				value = r.PropertyDimensions[name]
			}
		}
		values[i] = strings.TrimSpace(value)
	}
	return values, true
}

// JointTupleKey encodes a tuple collision-free (labels may hold any
// delimiter); false when a label is invalid or too long.
func JointTupleKey(values []string) (string, bool) {
	for _, value := range values {
		if len(value) > jointMaxLabelBytes || !utf8.ValidString(value) {
			return "", false
		}
	}
	data, _ := json.Marshal(values)
	if len(data) > jointMaxKeyBytes {
		return "", false
	}
	return string(data), true
}

// JointTupleValues decodes a tuple key for a projection of n dimensions.
func JointTupleValues(key string, n int) ([]string, bool) {
	var values []string
	if len(key) > jointMaxKeyBytes || json.Unmarshal([]byte(key), &values) != nil || len(values) != n {
		return nil, false
	}
	for _, value := range values {
		if len(value) > jointMaxLabelBytes || !utf8.ValidString(value) {
			return nil, false
		}
	}
	return values, true
}

// JointMetricsSnapshot is one window's joint evidence.
type JointMetricsSnapshot struct {
	Version    int                       `json:"version"`
	Population string                    `json:"population"`
	Dimensions []string                  `json:"dimensions"`
	Tuples     map[string]*ActionMetrics `json:"tuples"`
	// Other holds the samples of tuples past the key bound (or with
	// unrepresentable labels); OmittedTuples counts those distinct tuples
	// (a lower bound past the tracking limit).
	Other         *ActionMetrics `json:"other,omitempty"`
	OmittedTuples int64          `json:"omitted_tuples,omitempty"`
	Incomplete    bool           `json:"incomplete,omitempty"`
}

// jointAccumulator builds one window's joint snapshot.
type jointAccumulator struct {
	snapshot  *JointMetricsSnapshot
	digests   map[string]*durationDistribution
	durations map[string]time.Duration
	omitted   map[string]bool
	otherSum  time.Duration
	otherTD   *durationDistribution
}

func newJointAccumulator(p *JointProjection) *jointAccumulator {
	if p == nil {
		return nil
	}
	return &jointAccumulator{
		snapshot:  &JointMetricsSnapshot{Version: JointEvidenceVersion, Population: p.Population, Dimensions: append([]string(nil), p.Dimensions...), Tuples: map[string]*ActionMetrics{}},
		digests:   map[string]*durationDistribution{},
		durations: map[string]time.Duration{},
		omitted:   map[string]bool{},
	}
}

func (a *jointAccumulator) add(p *JointProjection, r *protocol.Result, groupProtocols map[string]string) {
	if a == nil {
		return
	}
	values, in := p.tuple(r, groupProtocols)
	if !in {
		return
	}
	key, valid := JointTupleKey(values)
	snapshot := a.snapshot
	if metrics := snapshot.Tuples[key]; valid && (metrics != nil || len(snapshot.Tuples) < JointMaxKeys) {
		if metrics == nil {
			metrics = &ActionMetrics{Name: key, MinDuration: r.Duration, MaxDuration: r.Duration}
			snapshot.Tuples[key] = metrics
		}
		mergeActionResult(metrics, r)
		recordDurationSample(a.digests, a.durations, key, r.Duration)
		return
	}
	snapshot.Incomplete = true
	if !valid {
		key = "\x00invalid"
	}
	if !a.omitted[key] && len(a.omitted) < jointOmittedTracking {
		a.omitted[key] = true
	}
	snapshot.OmittedTuples = int64(len(a.omitted))
	if snapshot.Other == nil {
		snapshot.Other = &ActionMetrics{Name: "other", MinDuration: r.Duration, MaxDuration: r.Duration}
		a.otherTD = newDurationDistribution()
	}
	mergeActionResult(snapshot.Other, r)
	a.otherSum += r.Duration
	a.otherTD.Add(float64(r.Duration))
}

func (a *jointAccumulator) finish() *JointMetricsSnapshot {
	if a == nil {
		return nil
	}
	for key, metrics := range a.snapshot.Tuples {
		finalizeActionMetrics(metrics, a.durations[key], a.digests[key])
	}
	if a.snapshot.Other != nil {
		finalizeActionMetrics(a.snapshot.Other, a.otherSum, a.otherTD)
	}
	return a.snapshot
}

// SortedJointKeys orders tuples by sample count (descending), then key, so
// a bound applied after merging keeps the same tuples on every peer.
func SortedJointKeys(counts map[string]int64) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}
