package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func messagingArchiveCoverageRecord(timestamp time.Time, sentA, sentB int, ackA, ackB float64) IntervalSummaryRecord {
	var summary *protocol.MessagingSummary
	protocol.MergeMessagingTelemetry(&summary, &protocol.ServerTelemetry{MessagesSent: sentA, PartitionID: "0", AcknowledgementMs: &ackA}, "events", "kafka_topic", "orders")
	protocol.MergeMessagingTelemetry(&summary, &protocol.ServerTelemetry{MessagesSent: sentB, PartitionID: "1", AcknowledgementMs: &ackB}, "events", "kafka_topic", "orders")
	return IntervalSummaryRecord{Timestamp: timestamp, TestID: "run", Messaging: map[string]*protocol.MessagingSummary{"publish": summary}}
}

func messagingArchiveCoverageBundle(t *testing.T, target string, records ...IntervalSummaryRecord) remoteArchiveBundle {
	t.Helper()
	root := t.TempDir()
	bundle := remoteArchiveBundle{
		target:           target,
		transactionsPath: filepath.Join(root, "transactions.jsonl"),
		resourcesPath:    filepath.Join(root, "resources.jsonl"),
		summariesPath:    filepath.Join(root, "summaries.jsonl"),
		manifest:         RunArchiveManifest{IntervalSummaryCount: int64(len(records))},
	}
	for _, path := range []string{bundle.transactionsPath, bundle.resourcesPath} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Create(bundle.summariesPath)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return bundle
}

type messagingArchiveCoverageExpectation struct {
	sent, operations, ackCount int64
	ackSum, ackMax             float64
	incomplete                 bool
}

func assertMessagingArchiveCoverage(t *testing.T, record IntervalSummaryRecord, want messagingArchiveCoverageExpectation) {
	t.Helper()
	summary := record.Messaging["publish"]
	if summary == nil || len(summary.Dimensions) != 2 {
		t.Fatalf("missing observed partition evidence: %+v", summary)
	}
	var sent, operations, ackCount int64
	var ackSum, ackMax float64
	for _, dimension := range summary.Dimensions {
		sent += dimension.Sent
		operations += dimension.Operations
		ackCount += dimension.Ack.Count
		ackSum += dimension.Ack.Sum
		ackMax = max(ackMax, dimension.Ack.Max)
	}
	if sent != want.sent || operations != want.operations || ackCount != want.ackCount || ackSum != want.ackSum || ackMax != want.ackMax {
		t.Fatalf("observations changed: sent=%d operations=%d ack=(%d, %.1f, %.1f); want %+v", sent, operations, ackCount, ackSum, ackMax, want)
	}
	if summary.Incomplete != want.incomplete {
		t.Fatalf("incomplete=%v; want %v", summary.Incomplete, want.incomplete)
	}
	if _, scored := summary.ObservedPartitionSkew(); scored == want.incomplete {
		t.Fatalf("partition skew scored=%v; incomplete=%v", scored, want.incomplete)
	}
}

func TestMessagingArchiveMergeRequiresEveryBundleInEachWindow(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		peerOffset time.Duration
		emptyPeer  bool
		want       []messagingArchiveCoverageExpectation
	}{
		{name: "aligned", peerOffset: 200 * time.Millisecond, want: []messagingArchiveCoverageExpectation{{sent: 20, operations: 4, ackCount: 4, ackSum: 20, ackMax: 8}}},
		{name: "skewed peer", peerOffset: 2 * time.Second, want: []messagingArchiveCoverageExpectation{{sent: 10, operations: 2, ackCount: 2, ackSum: 6, ackMax: 4, incomplete: true}, {sent: 10, operations: 2, ackCount: 2, ackSum: 14, ackMax: 8, incomplete: true}}},
		{name: "empty known peer", emptyPeer: true, want: []messagingArchiveCoverageExpectation{{sent: 10, operations: 2, ackCount: 2, ackSum: 6, ackMax: 4, incomplete: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundles := []remoteArchiveBundle{messagingArchiveCoverageBundle(t, "a", messagingArchiveCoverageRecord(base, 9, 1, 2, 4))}
			if tc.emptyPeer {
				bundles = append(bundles, messagingArchiveCoverageBundle(t, "b"))
			} else {
				bundles = append(bundles, messagingArchiveCoverageBundle(t, "b", messagingArchiveCoverageRecord(base.Add(tc.peerOffset), 3, 7, 6, 8)))
			}
			destination := filepath.Join(t.TempDir(), "merged.jsonl")
			count, err := mergeRemoteArchiveIntervalSummaries(destination, bundles)
			if err != nil {
				t.Fatal(err)
			}
			records := readMergedSummaries(t, destination)
			if count != int64(len(tc.want)) || len(records) != len(tc.want) {
				t.Fatalf("merged row count=%d/%d; want %d", count, len(records), len(tc.want))
			}
			for i, record := range records {
				assertMessagingArchiveCoverage(t, record, tc.want[i])
			}
		})
	}
}

func TestMessagingArchiveWriterPreservesMissingSourceCoverage(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		missingPeer bool
		want        messagingArchiveCoverageExpectation
	}{
		{name: "known uncollected peer", missingPeer: true, want: messagingArchiveCoverageExpectation{sent: 10, operations: 2, ackCount: 2, ackSum: 6, ackMax: 4, incomplete: true}},
		{name: "best effort collected all", want: messagingArchiveCoverageExpectation{sent: 20, operations: 4, ackCount: 4, ackSum: 20, ackMax: 8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundles := []remoteArchiveBundle{messagingArchiveCoverageBundle(t, "a", messagingArchiveCoverageRecord(base, 9, 1, 2, 4))}
			if !tc.missingPeer {
				bundles = append(bundles, messagingArchiveCoverageBundle(t, "b", messagingArchiveCoverageRecord(base.Add(200*time.Millisecond), 3, 7, 6, 8)))
			}
			root := t.TempDir()
			manifest := &RunArchiveManifest{
				TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "summaries.jsonl",
				SourceCoverage: &RunArchiveSourceCoverage{Mode: "best_effort", Sources: []RunArchiveSource{{Target: "a", Collected: true}, {Target: "b", Collected: !tc.missingPeer}}},
			}
			budget, err := newArchiveMergeBudget(root)
			if err != nil {
				t.Fatal(err)
			}
			defer budget.close()
			if err := writeMergedRemoteArchiveFilesWithBudget(root, manifest, bundles, nil, budget); err != nil {
				t.Fatal(err)
			}
			records := readMergedSummaries(t, filepath.Join(root, manifest.IntervalSummariesFile))
			if manifest.IntervalSummaryCount != 1 || len(records) != 1 {
				t.Fatalf("merged row count=%d/%d; want 1", manifest.IntervalSummaryCount, len(records))
			}
			assertMessagingArchiveCoverage(t, records[0], tc.want)
		})
	}
}

// A known generator without a row in a window is unknown Java work, not zero.
func TestJavaHTTPArchiveMergeRequiresEveryBundleInEachWindow(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	javaRecord := func(at time.Time) IntervalSummaryRecord {
		var summary *protocol.JavaHTTPSummary
		protocol.ObserveJavaHTTPAction(&summary, javaAPIDiagnostic())
		var actions *protocol.JavaHTTPActions
		protocol.AddJavaHTTPAction(&actions, "call", 1, summary)
		return IntervalSummaryRecord{Timestamp: at, TestID: "run", JavaHTTP: actions}
	}
	for _, tc := range []struct {
		name       string
		emptyPeer  bool
		incomplete bool
	}{{name: "aligned"}, {name: "empty known peer", emptyPeer: true, incomplete: true}} {
		t.Run(tc.name, func(t *testing.T) {
			peer := messagingArchiveCoverageBundle(t, "b", javaRecord(base.Add(200*time.Millisecond)))
			if tc.emptyPeer {
				peer = messagingArchiveCoverageBundle(t, "b")
			}
			destination := filepath.Join(t.TempDir(), "merged.jsonl")
			if _, err := mergeRemoteArchiveIntervalSummaries(destination, []remoteArchiveBundle{messagingArchiveCoverageBundle(t, "a", javaRecord(base)), peer}); err != nil {
				t.Fatal(err)
			}
			records := readMergedSummaries(t, destination)
			if len(records) != 1 || records[0].JavaHTTP == nil || records[0].JavaHTTP.Incomplete != tc.incomplete {
				t.Fatalf("merged Java envelope: %+v", records)
			}
		})
	}
}
