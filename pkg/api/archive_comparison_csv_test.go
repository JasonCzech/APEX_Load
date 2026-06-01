package api

import (
	"bytes"
	"encoding/csv"
	"testing"
)

func TestWriteArchiveComparisonCSVIncludesThroughputGapEvidence(t *testing.T) {
	var out bytes.Buffer
	err := writeArchiveComparisonCSV(&out, archiveComparisonBreakdownOverall,
		archiveComparisonRun{
			ID:   "baseline",
			Name: "Baseline",
			Report: &RunArchiveReport{Overview: RunArchiveOverview{
				Transactions:              100,
				PeakTPS:                   45,
				PeakTargetTPS:             50,
				MaxThroughputGapPct:       10,
				ThroughputMissedIntervals: 2,
				PeakActiveVUsers:          20,
			}},
		},
		archiveComparisonRun{
			ID:   "candidate",
			Name: "Candidate",
			Report: &RunArchiveReport{Overview: RunArchiveOverview{
				Transactions:              120,
				PeakTPS:                   50,
				PeakTargetTPS:             50,
				MaxThroughputGapPct:       0,
				ThroughputMissedIntervals: 0,
				PeakActiveVUsers:          20,
			}},
		},
	)
	if err != nil {
		t.Fatalf("writeArchiveComparisonCSV returned error: %v", err)
	}
	rows, err := csv.NewReader(bytes.NewReader(out.Bytes())).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows len = %d, want header plus two runs: %#v", len(rows), rows)
	}
	index := csvHeaderIndex(rows[0])
	for _, header := range []string{"peak_target_tps", "max_throughput_gap_pct", "throughput_missed_intervals", "throughput_target_missed"} {
		if _, ok := index[header]; !ok {
			t.Fatalf("missing header %q in %#v", header, rows[0])
		}
	}
	if rows[1][index["peak_target_tps"]] != "50" || rows[1][index["max_throughput_gap_pct"]] != "10" {
		t.Fatalf("baseline throughput evidence row = %#v", rows[1])
	}
	if rows[1][index["throughput_missed_intervals"]] != "2" || rows[1][index["throughput_target_missed"]] != "true" {
		t.Fatalf("baseline miss columns = %#v", rows[1])
	}
	if rows[2][index["throughput_target_missed"]] != "false" {
		t.Fatalf("candidate miss column = %q, want false", rows[2][index["throughput_target_missed"]])
	}
}

func csvHeaderIndex(headers []string) map[string]int {
	index := make(map[string]int, len(headers))
	for i, header := range headers {
		index[header] = i
	}
	return index
}
