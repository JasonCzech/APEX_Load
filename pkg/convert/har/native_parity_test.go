package har

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/paritytest"
)

// TestNativeHARParityCorpus converts every recording.har under
// testdata/native_parity with the same pipeline the browser recorder uses for
// its HTTP script, replays it once against a recording fixture server, and
// compares the wire traffic with the case's expected.json.
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
			result, err := Convert(raw, ConvertOptions{})
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			for _, problem := range paritytest.CheckOutcome(context.Background(), server, exp, result.Script, result.Warnings) {
				t.Error(problem)
			}
		})
	}
}
