package api

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/rca"
)

func TestRCABrowserOriginalSourceQualification(t *testing.T) {
	original := protocol.BrowserAuthoredDefinition{Version: 1, ScriptSHA256: strings.Repeat("a", 64), StructureSHA256: strings.Repeat("b", 64)}
	for _, tc := range []struct {
		name    string
		mutate  func(*RunArchiveManifest, *RunArchiveManifest, *rca.WorkloadKey)
		changed bool
	}{
		{name: "same original and executable"},
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
			if a.Provenance.BrowserAuthoredSources != nil && b.Provenance.BrowserAuthoredSources != nil && got["other"] {
				t.Fatal("unrelated group was excluded")
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
			read.currentManifest.Provenance.BrowserAuthoredSources = map[string]protocol.BrowserAuthoredDefinition{"checkout": original}
			read.manifest.Provenance.BrowserAuthoredSources = map[string]protocol.BrowserAuthoredDefinition{"checkout": original}
		}
		_, evidence := server.rcaBaselineEvidence(context.Background(), read, rca.NewBudgeter(rca.DefaultBudget, nil, nil), rca.DefaultBudget)
		if evidence == nil || evidence.ScriptChangedGroups["checkout"] == present {
			t.Fatalf("present %v, evidence %+v", present, evidence)
		}
	}
}
