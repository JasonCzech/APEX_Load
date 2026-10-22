package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// BrowserPanelInputs binds a saved browser comparison to exact archive
// publications and query semantics. These are assertions, verified against
// authorized archive reads, not client-supplied evidence of compatibility.
type BrowserPanelInputs struct {
	Version     int                   `json:"version"`
	QuerySHA256 string                `json:"query_sha256"`
	Current     BrowserPanelRunInput  `json:"current"`
	Baseline    *BrowserPanelRunInput `json:"baseline,omitempty"`
}

type BrowserPanelRunInput struct {
	RunID          string   `json:"run_id"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	TupleIDs       []string `json:"tuple_ids"`
}

// BrowserPanelQueryDigest uses the same default that saved definitions apply.
// It excludes only the assertions being authenticated by this digest.
func BrowserPanelQueryDigest(q AnalyticsQuery) string {
	q.BrowserInputs = nil
	if q.MaxSeries == 0 {
		q.MaxSeries = 20
	}
	data, err := json.Marshal(q)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func browserPanelDigestValid(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateBrowserPanelRunInput(input BrowserPanelRunInput) bool {
	if input.RunID == "" || strings.TrimSpace(input.RunID) != input.RunID || len(input.RunID) > 128 || !browserPanelDigestValid(input.ManifestSHA256) || input.TupleIDs == nil || len(input.TupleIDs) > 20 {
		return false
	}
	for i, id := range input.TupleIDs {
		if !browserPanelDigestValid(id) || i > 0 && input.TupleIDs[i-1] >= id {
			return false
		}
	}
	return true
}

func validateBrowserPanelInputs(q AnalyticsQuery) error {
	if q.QueryVersion != 4 {
		if q.BrowserInputs != nil {
			return errors.New("browser input pins require query version 4")
		}
		return nil
	}
	if q.Population != analysis.PopulationBrowserDocument || q.BrowserTupleMode != "separate" || q.EvidenceMode != "" || q.JointEvidence {
		return errors.New("query version 4 requires separate archived browser tuples")
	}
	// Preview requests obtain pins from evaluated archives. Saved definitions
	// require the returned pins; all old versions retain their previous meaning.
	p := q.BrowserInputs
	if p == nil {
		return nil
	}
	if p.Version != 1 || !validateBrowserPanelRunInput(p.Current) || !browserPanelDigestValid(p.QuerySHA256) || p.QuerySHA256 != BrowserPanelQueryDigest(q) {
		return errors.New("browser input pins do not match the saved query")
	}
	if (q.ComparisonRunID == "") != (p.Baseline == nil) || p.Baseline != nil && (!validateBrowserPanelRunInput(*p.Baseline) || p.Baseline.RunID != q.ComparisonRunID) {
		return errors.New("browser baseline input does not match the selected run")
	}
	return nil
}
