package qa_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/rca"
)

// The evaluation corpus manifest must always validate, and the README must
// state the release bars the harness enforces.
func TestRCAEvaluationCorpusIsWellFormed(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("rca-eval", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest rca.EvalManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, trial := range manifest.Trials {
		if _, err := os.Stat(filepath.Join("rca-eval", filepath.Clean(trial.Assessment))); err != nil {
			t.Errorf("trial %s: %v", trial.ID, err)
		}
	}
	readme, err := os.ReadFile(filepath.Join("rca-eval", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bar := range []string{"≥ 80%", "≥ 90%", "≤ 5%", "at least 100", "at least 20 trials"} {
		if !strings.Contains(string(readme), bar) {
			t.Errorf("README does not state %q", bar)
		}
	}
}
