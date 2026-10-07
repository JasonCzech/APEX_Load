package analysis

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func manifestFixture(t *testing.T) (*NormalizedPartitionManifest, ParquetMapping) {
	t.Helper()
	at := time.Date(2026, 1, 1, 0, 0, 0, 123456000, time.UTC)
	name := "latency"
	mapping := ParquetMapping{Version: 2, Timestamp: ParquetColumnPath{"ts"}, TimestampEncoding: "timestamp_offset", Timezone: "UTC", Measures: []ParquetMeasure{{Name: name, Value: ParquetColumnPath{"value"}, Kind: KindObservation, Unit: "ms"}}}
	stats := NormalizedPartitionStats{Version: 1, SchemaVersion: SchemaVersion, IdentityVersion: 2, Kind: KindObservation, Unit: "ms", Rows: 1, Valid: 1, MinTime: &at, MaxTime: &at, Metric: &name, Identity: &NormalizedPartitionIdentity{Source: "imported", Host: "host-a", Service: "service-a", Dimensions: map[string]string{"region": "east", "present": ""}}}
	manifest, err := NewNormalizedPartitionManifest("project", "dataset", mapping, []NormalizedPartition{{ObjectKey: "part-a.parquet", SHA256: strings.Repeat("a", 64), Bytes: 120, Stats: stats}})
	if err != nil {
		t.Fatal(err)
	}
	return manifest, mapping
}
func TestPartitionManifestConservativeSelection(t *testing.T) {
	m, _ := manifestFixture(t)
	at := *m.Partitions[0].Stats.MinTime
	base := PartitionSelection{Metric: "latency", IdentityVersion: 2, From: at, To: at.Add(time.Second)}
	for _, tc := range []struct {
		name   string
		change func(*PartitionSelection)
		want   int
	}{
		{"included", func(q *PartitionSelection) {}, 1},
		{"metric", func(q *PartitionSelection) { q.Metric = "cpu" }, 0},
		{"after-inclusive-max", func(q *PartitionSelection) { q.From = at.Add(time.Microsecond) }, 0},
		{"exact-exclusive-to", func(q *PartitionSelection) { q.From = at.Add(-time.Second); q.To = at }, 0},
		{"host", func(q *PartitionSelection) { q.Host = "other" }, 0},
		{"service", func(q *PartitionSelection) { q.Service = "other" }, 0},
		{"source", func(q *PartitionSelection) { q.Source = "other" }, 0},
		{"missing-dimension", func(q *PartitionSelection) { q.Dimensions = map[string]string{"absent": ""} }, 0},
		{"present-empty", func(q *PartitionSelection) { q.Dimensions = map[string]string{"present": ""} }, 1},
		{"unknown-filter-no-match", func(q *PartitionSelection) { q.Dimensions = map[string]string{"region": "west"} }, 0},
		{"submicrosecond-from-must-retain", func(q *PartitionSelection) { q.From = at.Add(time.Nanosecond) }, 1},
		{"submicrosecond-to-must-retain", func(q *PartitionSelection) { q.From = at.Add(-time.Second); q.To = at.Add(-time.Nanosecond) }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			tc.change(&q)
			got, err := m.Select(q)
			if err != nil || len(got) != tc.want {
				t.Fatalf("selection=%v err=%v want=%d", got, err, tc.want)
			}
		})
	}
	unknown := m.Clone()
	unknown.Partitions[0].Stats.Metric = nil
	unknown.Partitions[0].Stats.Identity = nil
	unknown.Digest, _ = unknown.canonicalDigest()
	q := base
	q.Metric = "different"
	q.Host = "different"
	if got, err := unknown.Select(q); err != nil || len(got) != 1 {
		t.Fatal("unknown summary falsely excluded population", got, err)
	}
	missing := m.Clone()
	missing.Partitions[0].Stats.Valid = 0
	missing.Partitions[0].Stats.Missing = 1
	missing.Digest, _ = missing.canonicalDigest()
	if got, err := missing.Select(base); err != nil || len(got) != 1 {
		t.Fatal("all-missing population was discarded", got, err)
	}
	// A histogram partition that ends before From can hold the window that
	// crosses into the range, so only histograms escape lower-bound pruning.
	histogram := m.Clone()
	histogram.Partitions[0].Stats.Kind, histogram.Partitions[0].Stats.SchemaVersion, histogram.Partitions[0].Stats.SourceRows = KindHistogram, DistributionSchemaVersion, 1
	histogram.Digest, _ = histogram.canonicalDigest()
	later := base
	later.From, later.To = at.Add(time.Minute), at.Add(2*time.Minute)
	if got, err := histogram.Select(later); err != nil || len(got) != 1 {
		t.Fatal("an earlier histogram partition was pruned", got, err)
	}
	if got, err := m.Select(later); err != nil || len(got) != 0 {
		t.Fatal("an earlier scalar partition was kept", got, err)
	}
	// Recording its latest window end prunes it again once no window
	// reaches From, and keeps it while one crosses into the range.
	ends := func(end time.Time) *NormalizedPartitionManifest {
		bounded := histogram.Clone()
		bounded.Partitions[0].Stats.MaxWindowEnd = &end
		bounded.Digest, _ = bounded.canonicalDigest()
		if bounded.Validate() != nil {
			t.Fatal("a histogram partition with its window end was refused")
		}
		return bounded
	}
	if got, err := ends(at.Add(30 * time.Second)).Select(later); err != nil || len(got) != 0 {
		t.Fatal("a histogram partition whose windows end before From was kept", got, err)
	}
	if got, err := ends(at.Add(90 * time.Second)).Select(later); err != nil || len(got) != 1 {
		t.Fatal("a histogram partition whose window crosses From was pruned", got, err)
	}
	// A window ending exactly at From does not reach into the range.
	if got, err := ends(later.From).Select(later); err != nil || len(got) != 0 {
		t.Fatal("a histogram partition whose windows end at From was kept", got, err)
	}
	// The window end survives the strict decode and still prunes.
	body, err := json.Marshal(ends(at.Add(30 * time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeNormalizedPartitionManifest(body)
	if err != nil || decoded.Partitions[0].Stats.MaxWindowEnd == nil || !decoded.Partitions[0].Stats.MaxWindowEnd.Equal(at.Add(30*time.Second)) {
		t.Fatal("the window end did not round-trip", err)
	}
	if got, err := decoded.Select(later); err != nil || len(got) != 0 {
		t.Fatal("a decoded window end did not prune", got, err)
	}
	for name, change := range map[string]func(*NormalizedPartitionStats){
		"at-max-time":     func(s *NormalizedPartitionStats) { end := *s.MaxTime; s.MaxWindowEnd = &end },
		"before-max-time": func(s *NormalizedPartitionStats) { end := s.MaxTime.Add(-time.Second); s.MaxWindowEnd = &end },
		"non-utc": func(s *NormalizedPartitionStats) {
			end := s.MaxTime.Add(time.Minute).In(time.FixedZone("x", 3600))
			s.MaxWindowEnd = &end
		},
		"sub-microsecond": func(s *NormalizedPartitionStats) {
			end := s.MaxTime.Add(time.Minute + time.Nanosecond)
			s.MaxWindowEnd = &end
		},
		"no-rows-with-end": func(s *NormalizedPartitionStats) {
			end := s.MaxTime.Add(time.Minute)
			*s = NormalizedPartitionStats{Version: s.Version, SchemaVersion: s.SchemaVersion, IdentityVersion: s.IdentityVersion, Kind: s.Kind, Unit: s.Unit, MaxWindowEnd: &end}
		},
	} {
		stats := histogram.Partitions[0].Stats.Clone()
		change(&stats)
		if stats.Validate() == nil {
			t.Fatalf("%s: an invalid window end was accepted", name)
		}
	}
	invalid := m.Clone()
	end := at.Add(time.Minute)
	invalid.Partitions[0].Stats.MaxWindowEnd = &end
	invalid.Digest, _ = invalid.canonicalDigest()
	if invalid.Validate() == nil {
		t.Fatal("a scalar partition carried a window end")
	}
	before := base
	before.From, before.To = at.Add(-2*time.Minute), at.Add(-time.Minute)
	if got, err := histogram.Select(before); err != nil || len(got) != 0 {
		t.Fatal("a histogram partition after the range was kept", got, err)
	}
	empty := m.Clone()
	empty.Partitions[0].Stats = NormalizedPartitionStats{Version: 1, SchemaVersion: SchemaVersion, IdentityVersion: 2, Kind: KindObservation, Unit: "ms"}
	empty.Digest, _ = empty.canonicalDigest()
	if got, err := empty.Select(base); err != nil || len(got) != 0 {
		t.Fatal("empty object requires no measurement IO", got, err)
	}
}
func TestPartitionManifestExactArtifactBindingAndCloning(t *testing.T) {
	m, mapping := manifestFixture(t)
	mappingHash, err := PartitionMappingDigest(mapping)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MatchesArtifacts("project", "dataset", mappingHash, 2, []string{"part-a.parquet"}, map[string]string{"part-a.parquet": strings.Repeat("a", 64)}, 1); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*NormalizedPartitionManifest)
	}{
		{"hash", func(m *NormalizedPartitionManifest) { m.Partitions[0].SHA256 = strings.Repeat("b", 64) }},
		{"rows", func(m *NormalizedPartitionManifest) { m.Partitions[0].Stats.Rows = 2 }},
		{"time", func(m *NormalizedPartitionManifest) {
			at := m.Partitions[0].Stats.MinTime.Add(time.Nanosecond)
			m.Partitions[0].Stats.MinTime = &at
		}},
		{"identity", func(m *NormalizedPartitionManifest) { m.Partitions[0].Stats.Identity.Dimensions["region"] = "west" }},
		{"digest", func(m *NormalizedPartitionManifest) { m.Digest = strings.Repeat("b", 64) }},
		{"duplicate-object", func(m *NormalizedPartitionManifest) { m.Partitions = append(m.Partitions, m.Partitions[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := m.Clone()
			tc.mutate(other)
			if other.Validate() == nil {
				t.Fatal("corrupt proof accepted")
			}
		})
	}
	clone := m.Clone()
	clone.Partitions[0].Stats.Identity.Dimensions["region"] = "clone"
	if m.Partitions[0].Stats.Identity.Dimensions["region"] != "east" {
		t.Fatal("clone retained identity aliases")
	}
	if err := m.MatchesArtifacts("project", "dataset", mappingHash, 2, []string{"part-a.parquet", "part-a.parquet"}, map[string]string{"part-a.parquet": strings.Repeat("a", 64)}, 2); err == nil {
		t.Fatal("duplicate population accepted")
	}
	if err := m.MatchesArtifacts("project", "dataset", mappingHash, 2, []string{"part-a.parquet"}, map[string]string{"part-a.parquet": strings.Repeat("a", 64)}, 2); err == nil {
		t.Fatal("input rows confused with accepted measurement count")
	}
}
func TestPartitionManifestStrictBoundedDecode(t *testing.T) {
	m, _ := manifestFixture(t)
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeNormalizedPartitionManifest(body)
	if err != nil || decoded.Digest != m.Digest {
		t.Fatal(decoded, err)
	}
	for _, bad := range []string{
		strings.Replace(string(body), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(body), `"rows":1`, `"rows":null`, 1),
		strings.Replace(string(body), `"missing":0,`, "", 1),
		strings.Replace(string(body), `"rows":1`, `"Rows":1`, 1),
		strings.Replace(string(body), `"region":"east"`, `"region":"east","region":"west"`, 1),
		strings.Replace(string(body), "123456Z", "123456+00:00", 1),
		strings.Replace(string(body), `"producer":`, `"unknown":1,"producer":`, 1),
		string(body) + "{}",
	} {
		if _, err := DecodeNormalizedPartitionManifest([]byte(bad)); err == nil {
			t.Fatalf("noncanonical/ambiguous metadata accepted: %s", bad)
		}
	}
	if _, err := DecodeNormalizedPartitionManifest(make([]byte, MaxPartitionManifestBytes+1)); err == nil {
		t.Fatal("oversized JSON allocated typed population")
	}
}
func TestPartitionManifestTotalEscapedBudget(t *testing.T) {
	m, _ := manifestFixture(t)
	// Scalar labels are each within their own limits; their escaped aggregate is
	// nevertheless independently bounded before Marshal/clone.
	parts := make([]NormalizedPartition, MaxNormalizedPartitions)
	for i := range parts {
		parts[i] = m.Partitions[0]
		parts[i].ObjectKey = "part-" + strings.Repeat("x", 250) + string(rune('a'+i%26))
		parts[i].Stats = m.Partitions[0].Stats.Clone()
		parts[i].Stats.Identity.Dimensions = make(map[string]string)
		for d := 0; d < 16; d++ {
			parts[i].Stats.Identity.Dimensions[string(rune('a'+d))] = strings.Repeat("\n", 128)
		}
	}
	if _, err := MarshalBoundedAnalysisJSON(parts, MaxPartitionManifestBytes); err == nil {
		t.Fatal("individually bounded labels bypassed total budget")
	}
	// An omitted optional field costs nothing, so adding one cannot push a
	// manifest that fit before over its limit.
	if body, err := MarshalBoundedAnalysisJSON(struct {
		End  *time.Time `json:"end,omitempty"`
		Rows int64      `json:"rows,omitempty"`
	}{}, 2); err != nil || string(body) != "{}" {
		t.Fatal("omitted optional fields were charged", string(body), err)
	}
	long := strings.Repeat("v", 64)
	// A written optional field is still charged before marshalling: each
	// value below needs more than the budget, so the estimate refuses it
	// even though nothing is marshalled.
	for name, value := range map[string]any{
		"map": struct {
			M map[string]string `json:"m,omitempty"`
		}{M: map[string]string{"k": long}},
		"slice": struct {
			S []string `json:"s,omitempty"`
		}{S: []string{long}},
		"string": struct {
			S string `json:"s,omitempty"`
		}{S: long},
		"pointer": struct {
			P *string `json:"p,omitempty"`
		}{P: &long},
	} {
		left := int64(32)
		if analysisJSONCharge(reflect.ValueOf(value), 0, &left) == nil {
			t.Fatalf("%s: a written optional field was not charged", name)
		}
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if _, err := MarshalBoundedAnalysisJSON(cycle, 1024); err == nil {
		t.Fatal("cyclic metadata bypassed bounded traversal")
	}
}
