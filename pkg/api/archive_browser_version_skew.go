package api

import (
	"sort"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// A browser session whose browser's major version differs from the one the
// Playwright driver was tested with, or from the one its script was recorded
// with, reports it in its first action's evidence
// (protocol_evidence.browser_version_skew). The run report lists each
// distinct mismatch and how many sessions reported it.

// maxBrowserVersionSkews bounds the distinct mismatches a run reports.
const maxBrowserVersionSkews = 16

// BrowserVersionSkew is one browser version mismatch the run's sessions
// reported: the launched browser's version and the one expected, from the
// driver's tested build (source "driver") or the recording (source
// "recording").
type BrowserVersionSkew struct {
	Source   string `json:"source"`
	Actual   string `json:"actual"`
	Expected string `json:"expected"`
	Sessions int64  `json:"sessions"`
}

// SourceLabel names what the launched browser was compared with.
func (s BrowserVersionSkew) SourceLabel() string {
	if s.Source == browserprotocol.BrowserVersionSkewRecording {
		return "The recording's browser"
	}
	return "The Playwright driver's tested browser"
}

type archiveBrowserVersionSkewAccumulator struct {
	rows map[BrowserVersionSkew]int64
}

func newArchiveBrowserVersionSkewAccumulator() *archiveBrowserVersionSkewAccumulator {
	return &archiveBrowserVersionSkewAccumulator{rows: map[BrowserVersionSkew]int64{}}
}

func (a *archiveBrowserVersionSkewAccumulator) merge(tx TransactionEvent) {
	skews, _ := tx.ProtocolEvidence[browserprotocol.BrowserVersionSkewEvidenceKey].([]any)
	for _, raw := range skews {
		record, _ := raw.(map[string]any)
		source, _ := record["source"].(string)
		actual, _ := record["actual"].(string)
		expected, _ := record["expected"].(string)
		if source != browserprotocol.BrowserVersionSkewDriver && source != browserprotocol.BrowserVersionSkewRecording {
			continue
		}
		if !browserprotocol.ValidBrowserVersion(actual) || !browserprotocol.ValidBrowserVersion(expected) {
			continue
		}
		key := BrowserVersionSkew{Source: source, Actual: actual, Expected: expected}
		if _, seen := a.rows[key]; !seen && len(a.rows) >= maxBrowserVersionSkews {
			continue
		}
		a.rows[key]++
	}
}

// result is the mismatches, the driver's first, then by sessions.
func (a *archiveBrowserVersionSkewAccumulator) result() []BrowserVersionSkew {
	out := make([]BrowserVersionSkew, 0, len(a.rows))
	for key, sessions := range a.rows {
		key.Sessions = sessions
		out = append(out, key)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source == browserprotocol.BrowserVersionSkewDriver
		}
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		if out[i].Actual != out[j].Actual {
			return out[i].Actual < out[j].Actual
		}
		return out[i].Expected < out[j].Expected
	})
	if len(out) == 0 {
		return nil
	}
	return out
}
