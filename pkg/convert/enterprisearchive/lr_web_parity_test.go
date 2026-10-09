package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/internal/paritytest"
)

// TestLRWebParityCorpus imports every archive under testdata/lr_web_parity,
// replays it once against a recording fixture server, and compares the wire
// traffic with the case's expected.json (or its LoadRunner replay.har when one
// is present). Gated cases must instead carry their execution gate and be
// refused. See pkg/internal/paritytest for the case format.
func TestLRWebParityCorpus(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "lr_web_parity", "*"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirs)
	ran := 0
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		ran++
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			exp, err := paritytest.LoadExpectation(dir)
			if err != nil {
				t.Fatal(err)
			}
			server := paritytest.NewServer(exp.Responses)
			defer server.Close()

			archive := zipParityArchive(t, dir, server)
			result, err := ConvertZip(archive, ConvertOptions{PreserveTransactions: true})
			if err != nil {
				t.Fatalf("ConvertZip: %v", err)
			}
			scripts := result.effectiveScripts()
			if len(scripts) == 0 && exp.Outcome == paritytest.OutcomeGated {
				// A review-only import generates no script at all; its review
				// artifact must still name the reason.
				checkParityReviewArtifact(t, result, exp)
				if recorded := server.Recorded(); len(recorded) > 0 {
					t.Errorf("a review-only import sent %d request(s)", len(recorded))
				}
				return
			}
			if len(scripts) != 1 {
				t.Fatalf("converted %d scripts, want 1", len(scripts))
			}
			for _, problem := range paritytest.CheckOutcome(context.Background(), server, exp, scripts[0], result.Warnings) {
				t.Error(problem)
			}
		})
	}
	if ran == 0 {
		t.Fatal("no parity corpus cases found")
	}
}

// checkParityReviewArtifact verifies that a review-only import's artifact
// carries every expected gate reason code.
func checkParityReviewArtifact(t *testing.T, result *ConvertResult, exp *paritytest.Expectation) {
	t.Helper()
	if result.ReviewArtifact == nil {
		t.Fatal("no script and no review artifact were produced")
	}
	raw, err := json.Marshal(result.ReviewArtifact)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range exp.GateReasonCodes {
		if !strings.Contains(string(raw), `"`+code+`"`) {
			t.Errorf("review artifact does not name reason code %q: %s", code, raw)
		}
	}
	joined := strings.Join(result.Warnings, "\n")
	for _, fragment := range exp.WarningsContain {
		if !strings.Contains(joined, fragment) {
			t.Errorf("warnings do not mention %q; got:\n%s", fragment, joined)
		}
	}
}

// zipParityArchive zips a corpus case's source files, pointing every
// placeholder origin at the fixture server.
func zipParityArchive(t *testing.T, dir string, server *paritytest.Server) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		switch rel {
		case "expected.json", "replay.har", "README.md":
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if utf8.Valid(content) {
			content = []byte(strings.ReplaceAll(string(content), paritytest.Origin, server.URL))
		}
		file, err := writer.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = file.Write(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
