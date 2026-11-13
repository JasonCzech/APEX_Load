package api

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// A remote generator's authored_request_retained marker is honoured only when
// the controller's own copy of that group's script leaves
// config.log.redact_run_evidence off. A marker on an event from a script with
// the setting on (or from an unknown group) is cleared before sanitizing, so
// the request side keeps the full name/shape scrub.
func TestMergeRemoteArchiveTransactionsHonoursAuthoredMarkerOnlyForSettingOff(t *testing.T) {
	const literal = "remote-authored-literal-9101"
	for _, tc := range []struct {
		name    string
		redact  *bool
		group   string
		visible bool
	}{
		{name: "setting-on", redact: dsl.BoolPtr(true), group: "buyers", visible: false},
		{name: "setting-off", redact: dsl.BoolPtr(false), group: "buyers", visible: true},
		{name: "setting-unset", redact: nil, group: "buyers", visible: true},
		{name: "unknown-group", redact: dsl.BoolPtr(false), group: "forged", visible: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			event := TransactionEvent{
				Timestamp: time.Unix(123, 0).UTC(), TestID: "distributed-authored", Group: tc.group, ActionName: "Login",
				Snapshot: &protocol.ErrorSnapshot{
					AuthoredRequestRetained: true,
					RequestURL:              "https://api.example.test/login",
					RequestHeaders:          map[string]string{"Authorization": "Bearer " + literal},
					NetworkEntries: []protocol.NetworkEntry{{
						AuthoredRequestRetained: true, URL: "https://api.example.test/login", Method: "POST",
						RequestHeaders: map[string]string{"Authorization": "Bearer " + literal},
					}},
				},
				NetworkEntries: []protocol.NetworkEntry{{
					AuthoredRequestRetained: true, URL: "https://api.example.test/login", Method: "POST",
					RequestHeaders: map[string]string{"Authorization": "Bearer " + literal},
				}},
				ProtocolEvidence: map[string]any{
					archivedEvidenceAuthoredMarker: true,
					"metadata":                     map[string]any{"headers": map[string]any{"Authorization": "Bearer " + literal}},
				},
			}
			source := filepath.Join(root, "a.jsonl")
			content, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, append(content, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			script := &dsl.Script{Name: "buyers"}
			script.Config.Log.RedactRunEvidence = tc.redact
			test := &ManagedTest{Groups: []TestGroup{{Name: "buyers", script: script}}}
			manifest := RunArchiveManifest{authoredRequestGroups: authoredRequestRetentionForTest(test)}
			destination := filepath.Join(root, "merged.jsonl")
			if err := mergeRemoteArchiveTransactions(destination, &manifest, []remoteArchiveBundle{{target: "a", transactionsPath: source, manifest: RunArchiveManifest{TransactionCount: 1}}}, nil); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(destination)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 1<<20), 1<<20)
			if !scanner.Scan() {
				t.Fatal("no merged event")
			}
			merged := scanner.Text()
			if got := strings.Count(merged, literal); tc.visible && got < 4 || !tc.visible && got != 0 {
				t.Fatalf("authored literal occurrences = %d (visible=%v): %s", got, tc.visible, merged)
			}
		})
	}
}
