package har

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/clienttimestamps"
	"github.com/jasonczech/apexload/pkg/internal/paritytest"
)

// TestNativeHARParityCorpus converts every recording.har under
// testdata/native_parity with the same pipeline the browser recorder uses for
// its HTTP script (with the case's options.json, when present), replays it
// once against a recording fixture server, and compares the wire traffic with
// the case's expected.json.
func TestNativeHARParityCorpus(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "native_parity", "*"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirs)
	if len(dirs) == 0 {
		t.Fatal("no native HAR parity corpus cases found")
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			exp, err := paritytest.LoadExpectation(dir)
			if err != nil {
				t.Fatal(err)
			}
			server := paritytest.NewServer(exp.Responses)
			defer server.Close()
			raw, err := os.ReadFile(filepath.Join(dir, "recording.har"))
			if err != nil {
				t.Fatal(err)
			}
			raw = []byte(server.Expand(string(raw)))
			var opts ConvertOptions
			if optionsJSON, err := os.ReadFile(filepath.Join(dir, "options.json")); err == nil {
				if err := json.Unmarshal(optionsJSON, &opts); err != nil {
					t.Fatalf("options.json: %v", err)
				}
			}
			result, err := Convert(raw, opts)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			// The import API also replaces client timestamps after conversion.
			if evidence, err := clienttimestamps.FromHAR(raw); err == nil {
				if warning := clienttimestamps.Apply(result.Script, evidence).Warning(); warning != "" {
					result.Warnings = append(result.Warnings, warning)
				}
			}
			for _, problem := range paritytest.CheckOutcome(context.Background(), server, exp, result.Script, result.Warnings) {
				t.Error(problem)
			}
		})
	}
}
