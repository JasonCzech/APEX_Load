package archivecontract

import (
	"encoding/json"
	"testing"
)

func TestSourceCoveragePreservesAbsentAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		value RunArchiveSourceCoverage
		want  string
	}{
		{RunArchiveSourceCoverage{Mode: "local"}, `{"mode":"local","sources":null}`},
		{RunArchiveSourceCoverage{Mode: "local", Sources: []RunArchiveSource{}}, `{"mode":"local","sources":[]}`},
		{RunArchiveSourceCoverage{Mode: "distributed", Sources: []RunArchiveSource{{Target: "generator", Collected: false, Groups: []string{"group"}}}}, `{"mode":"distributed","sources":[{"target":"generator","collected":false,"groups":["group"]}]}`},
	} {
		raw, err := json.Marshal(tc.value)
		if err != nil || string(raw) != tc.want {
			t.Fatalf("wire=%s err=%v want=%s", raw, err, tc.want)
		}
	}
}

func TestIntegrityVerdictPreservesZeroCounters(t *testing.T) {
	raw, err := json.Marshal(RunArchiveIntegrityReport{TestID: "run", Status: "unverifiable"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"total", "matched", "mismatched", "missing", "unverifiable"} {
		if string(fields[name]) != "0" {
			t.Errorf("%s=%s", name, fields[name])
		}
	}
	if string(fields["verified"]) != "false" {
		t.Fatal("unverified became absent")
	}
	if _, ok := fields["artifacts"]; ok {
		t.Fatal("empty optional artifacts emitted")
	}
}

func TestSourceLabel(t *testing.T) {
	for _, tc := range []struct {
		source RunArchiveSource
		want   string
	}{
		{RunArchiveSource{}, ""},
		{RunArchiveSource{Target: " address "}, "address"},
		{RunArchiveSource{Target: "address", GeneratorID: " generator ", Region: " west ", Groups: []string{"cart"}}, "generator (address; west; group cart)"},
		{RunArchiveSource{Target: "address", GeneratorID: "address", Groups: []string{"cart", "store"}}, "address (groups cart, store)"},
	} {
		if got := tc.source.Label(); got != tc.want {
			t.Fatalf("label %q, want %q", got, tc.want)
		}
	}
}
