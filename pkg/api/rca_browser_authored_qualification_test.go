package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/rca"
)

func TestRCABrowserOriginalSourceQualification(t *testing.T) {
	original := protocol.BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}
	for _, tc := range []struct {
		name         string
		mutate       func(*RunArchiveManifest, *RunArchiveManifest, *rca.WorkloadKey)
		changed      bool
		otherChanged bool
	}{
		{name: "same original and executable"},
		{name: "initial identity survives missing final", mutate: func(a, b *RunArchiveManifest, _ *rca.WorkloadKey) {
			pa, pb := a.Provenance, b.Provenance
			a.InitialProvenance = &pa
			b.InitialProvenance = &pb
			a.Provenance.BrowserAuthoredSources = nil
			b.Provenance.BrowserAuthoredSources = nil
		}},
		{name: "initial mismatch wins over equal final", changed: true, mutate: func(a, b *RunArchiveManifest, _ *rca.WorkloadKey) {
			pa, pb := a.Provenance, b.Provenance
			a.InitialProvenance = &pa
			b.InitialProvenance = &pb
			d := original
			d.ScriptSHA256 = strings.Repeat("c", 64)
			b.InitialProvenance.BrowserAuthoredSources = map[string]protocol.BrowserAuthoredDefinition{"browser": d, "other": original}
		}},
		{name: "empty initial does not fall back to final", changed: true, otherChanged: true, mutate: func(a, b *RunArchiveManifest, _ *rca.WorkloadKey) {
			a.InitialProvenance = &RunArchiveProvenance{}
			b.InitialProvenance = &RunArchiveProvenance{}
		}},
		{name: "different original content", changed: true, mutate: func(_, b *RunArchiveManifest, _ *rca.WorkloadKey) {
			d := b.Provenance.BrowserAuthoredSources["browser"]
			d.ScriptSHA256 = strings.Repeat("c", 64)
			b.Provenance.BrowserAuthoredSources["browser"] = d
		}},
		{name: "different original structure", changed: true, mutate: func(_, b *RunArchiveManifest, _ *rca.WorkloadKey) {
			d := b.Provenance.BrowserAuthoredSources["browser"]
			d.StructureSHA256 = strings.Repeat("c", 64)
			b.Provenance.BrowserAuthoredSources["browser"] = d
		}},
		{name: "current missing", changed: true, mutate: func(a, _ *RunArchiveManifest, _ *rca.WorkloadKey) { a.Provenance.BrowserAuthoredSources = nil }},
		{name: "baseline missing", changed: true, mutate: func(_, b *RunArchiveManifest, _ *rca.WorkloadKey) { b.Provenance.BrowserAuthoredSources = nil }},
		{name: "both missing", changed: true, mutate: func(a, b *RunArchiveManifest, _ *rca.WorkloadKey) {
			a.Provenance.BrowserAuthoredSources = nil
			b.Provenance.BrowserAuthoredSources = nil
		}},
		{name: "invalid descriptor", changed: true, mutate: func(_, b *RunArchiveManifest, _ *rca.WorkloadKey) {
			d := b.Provenance.BrowserAuthoredSources["browser"]
			d.Version = 0
			b.Provenance.BrowserAuthoredSources["browser"] = d
		}},
		{name: "equal malformed descriptors", changed: true, mutate: func(a, b *RunArchiveManifest, _ *rca.WorkloadKey) {
			d := a.Provenance.BrowserAuthoredSources["browser"]
			d.ScriptSHA256 = "unknown"
			a.Provenance.BrowserAuthoredSources["browser"] = d
			b.Provenance.BrowserAuthoredSources["browser"] = d
		}},
		{name: "same original different runtime", changed: true, mutate: func(_, _ *RunArchiveManifest, b *rca.WorkloadKey) {
			b.Groups["browser"] = rca.WorkloadGroup{ScriptDigest: "different"}
		}},
		{name: "same original unrecorded runtime", changed: true, mutate: func(_, _ *RunArchiveManifest, b *rca.WorkloadKey) { b.Groups["browser"] = rca.WorkloadGroup{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &RunArchiveManifest{Provenance: RunArchiveProvenance{BrowserAuthoredSources: map[string]protocol.BrowserAuthoredDefinition{"browser": original, "other": original}}}
			b := &RunArchiveManifest{Provenance: RunArchiveProvenance{BrowserAuthoredSources: map[string]protocol.BrowserAuthoredDefinition{"browser": original, "other": original}}}
			current := rca.WorkloadKey{Groups: map[string]rca.WorkloadGroup{"browser": {ScriptDigest: "runtime"}, "other": {ScriptDigest: "other"}}}
			baseline := rca.WorkloadKey{Groups: map[string]rca.WorkloadGroup{"browser": {ScriptDigest: "runtime"}, "other": {ScriptDigest: "other"}}}
			if tc.mutate != nil {
				tc.mutate(a, b, &baseline)
			}
			got := rcaBrowserScriptChangedGroups(current, baseline, a, b)
			if got["browser"] != tc.changed {
				t.Fatalf("changed groups = %v", got)
			}
			if a.Provenance.BrowserAuthoredSources != nil && b.Provenance.BrowserAuthoredSources != nil && got["other"] != tc.otherChanged {
				t.Fatalf("unrelated group exclusion = %v, want %v", got["other"], tc.otherChanged)
			}
			got["browser"] = false
			if again := rcaBrowserScriptChangedGroups(current, baseline, a, b); again["browser"] != tc.changed {
				t.Fatal("returned map mutated input state")
			}
		})
	}
	key := rca.WorkloadKey{Groups: map[string]rca.WorkloadGroup{"browser": {ScriptDigest: "runtime"}}}
	if !rcaBrowserScriptChangedGroups(key, key, nil, nil)["browser"] {
		t.Fatal("nil manifests qualified")
	}
	if len(rcaBrowserScriptChangedGroups(rca.WorkloadKey{}, rca.WorkloadKey{}, nil, nil)) != 0 {
		t.Fatal("empty workloads invented a group")
	}
}

func TestRCABaselineEvidenceRequiresOriginalSourceAlongsideExecution(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 200, 150)
	manifest, err := loadRunArchiveManifestFromRecord(server.runner.archives[current.ID])
	if err != nil {
		t.Fatal(err)
	}
	digest, err := rcaArchiveDigest(server.runner.archives[baseline.ID])
	if err != nil {
		t.Fatal(err)
	}
	read := server.readRCABaselineArchive(context.Background(), manifest, baseline.ID, digest)
	defer read.release()
	if read.currentManifest != manifest {
		t.Fatal("current source provenance was not retained")
	}
	// Isolate source qualification from the fixture's unrecorded executable keys.
	read.currentKey = rca.WorkloadKey{Groups: map[string]rca.WorkloadGroup{"checkout": {ScriptDigest: "same-runtime"}}}
	read.key = read.currentKey
	original := protocol.BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}
	for _, present := range []bool{false, true} {
		if present {
			read.currentManifest.InitialProvenance.BrowserAuthoredSources = map[string]protocol.BrowserAuthoredDefinition{"checkout": original}
			read.manifest.InitialProvenance.BrowserAuthoredSources = map[string]protocol.BrowserAuthoredDefinition{"checkout": original}
		}
		_, evidence := server.rcaBaselineEvidence(context.Background(), read, rca.NewBudgeter(rca.DefaultBudget, nil, nil), rca.DefaultBudget)
		if evidence == nil || evidence.ScriptChangedGroups["checkout"] == present {
			t.Fatalf("present %v, evidence %+v", present, evidence)
		}
	}
}

// Valid archived observations may have blank/foreign groups. Exercise their real
// archive projection and detector output, not only the exclusion-map helper.
func TestRCABrowserArchivedUnassignedGroupsCannotQualify(t *testing.T) {
	server, store := baselineTestServer(t)
	original := protocol.BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}
	key := rca.WorkloadKey{Groups: map[string]rca.WorkloadGroup{"browser": {ScriptDigest: "runtime"}}}
	for _, group := range []string{"", "foreign", "browser"} {
		t.Run("group="+group, func(t *testing.T) {
			var series [2][]rca.Series
			var manifests [2]*RunArchiveManifest
			for index, id := range []string{"current", "baseline"} {
				record := exemplarArchiveFixture(t, server, store, id, nil)
				var documents []d2Document
				for i := 0; i < 36; i++ {
					value := float64(1000 + (i%2)*40)
					if index == 0 {
						value += 400
					}
					documents = append(documents, d2Document{id: i + 1, metric: "lcp", group: group, cohort: d2Cohort("141.0"), at: exemplarFixtureStart.Add(time.Duration(i*10+1) * time.Second), value: value, final: true})
				}
				attachD2Documents(t, record, documents...)
				manifest, err := loadRunArchiveManifestFromRecord(record)
				if err != nil {
					t.Fatal(err)
				}
				// Even a claimed source descriptor cannot qualify a foreign workload group.
				manifest.Provenance.BrowserAuthoredSources = map[string]protocol.BrowserAuthoredDefinition{"browser": original, "foreign": original, "": original}
				manifests[index] = manifest
				summaries, err := readAllArchiveSummariesContext(context.Background(), record)
				if err != nil {
					t.Fatal(err)
				}
				var limits []rca.Limitation
				series[index], limits, err = rcaBrowserDocumentSeries(context.Background(), record, manifest, summaries, -1, 0)
				if err != nil || len(limits) != 0 || len(series[index]) != 1 {
					t.Fatalf("archive series: %v %v %v", series[index], limits, err)
				}
			}
			level := rca.LoadLevel{Start: exemplarFixtureStart, End: exemplarFixtureStart.Add(6 * time.Minute), VUsers: 5}
			input := rca.DetectionInput{Levels: []rca.LoadLevel{level}, Series: series[0], Baseline: &rca.DetectionBaseline{TestID: "baseline", Qualification: rca.BaselineQualified, Levels: []rca.LoadLevel{level}, Series: series[1], ScriptChangedGroups: rcaBrowserScriptChangedGroups(key, key, manifests[0], manifests[1], series[1]...)}}
			got := rca.DetectBrowserBaseline(input, rca.DefaultBrowserBaselineParams)
			if got == nil || len(got.Results) != 1 {
				t.Fatalf("detector: %+v", got)
			}
			result := got.Results[0]
			if group == "browser" {
				if result.State != rca.BrowserBaselineAnomaly {
					t.Fatalf("valid control: %+v", result)
				}
			} else if result.State != rca.BrowserBaselineUnscored || result.Reason != rca.BrowserBaselineCohortChanged || result.Z != 0 || result.ExpectedHigh != 0 {
				t.Fatalf("unassigned cohort scored: %+v", result)
			}
		})
	}
}
