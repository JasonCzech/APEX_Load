package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// BrowserPanelPolicyPin binds an immutable browser or journey policy revision
// to a saved browser panel input receipt (BrowserPanelInputs version 2).
type BrowserPanelPolicyPin struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	SHA256   string `json:"sha256"`
}

// BrowserPanelInputs binds a saved browser comparison to exact archive
// publications and query semantics. These are assertions, verified against
// authorized archive reads, not client-supplied evidence of compatibility.
type BrowserPanelInputs struct {
	Version                int                     `json:"version"`
	QuerySHA256            string                  `json:"query_sha256"`
	PolicyPins             []BrowserPanelPolicyPin `json:"policy_pins,omitempty"`
	SelectionReceiptSHA256 string                  `json:"selection_receipt_sha256,omitempty"`
	Current                BrowserPanelRunInput    `json:"current"`
	Baseline               *BrowserPanelRunInput   `json:"baseline,omitempty"`
}

type BrowserPanelRunInput struct {
	RunID          string                  `json:"run_id"`
	ManifestSHA256 string                  `json:"manifest_sha256"`
	TupleIDs       []string                `json:"tuple_ids"`
	PolicyPins     []BrowserPanelPolicyPin `json:"policy_pins,omitempty"`
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

func validateBrowserPanelPolicyPins(pins []BrowserPanelPolicyPin) bool {
	if len(pins) > 2 {
		return false
	}
	for i, pin := range pins {
		if (pin.Kind != AnalysisDefinitionKindBrowserPolicy && pin.Kind != AnalysisDefinitionKindJourneyPolicy) ||
			pin.ID == "" || strings.TrimSpace(pin.ID) != pin.ID || len(pin.ID) > 128 ||
			pin.Revision < 1 || !browserPanelDigestValid(pin.SHA256) {
			return false
		}
		if i > 0 && pins[i-1].Kind >= pin.Kind {
			return false
		}
	}
	return true
}

func validateBrowserPanelRunInput(input BrowserPanelRunInput, version int) bool {
	if input.RunID == "" || strings.TrimSpace(input.RunID) != input.RunID || len(input.RunID) > 128 || !browserPanelDigestValid(input.ManifestSHA256) || input.TupleIDs == nil || len(input.TupleIDs) > 20 {
		return false
	}
	for i, id := range input.TupleIDs {
		if !browserPanelDigestValid(id) || i > 0 && input.TupleIDs[i-1] >= id {
			return false
		}
	}
	if version == 1 {
		return len(input.PolicyPins) == 0
	}
	return validateBrowserPanelPolicyPins(input.PolicyPins)
}

func validateBrowserPanelInputs(q AnalyticsQuery) error {
	if q.QueryVersion != 4 && q.QueryVersion != 5 {
		if q.BrowserInputs != nil {
			return errors.New("browser input pins require query version 4 or 5")
		}
		return nil
	}
	if q.Population != analysis.PopulationBrowserDocument || q.BrowserTupleMode != "separate" || q.EvidenceMode != "" || q.JointEvidence {
		return errors.New("query version 4 or 5 requires separate archived browser tuples")
	}
	// Preview requests obtain pins from evaluated archives. Saved definitions
	// require the returned pins; all old versions retain their previous meaning.
	p := q.BrowserInputs
	if p == nil {
		return nil
	}
	expectedVersion := 1
	if q.QueryVersion == 5 {
		expectedVersion = 2
	}
	if p.Version != expectedVersion || !validateBrowserPanelRunInput(p.Current, expectedVersion) || !browserPanelDigestValid(p.QuerySHA256) || p.QuerySHA256 != BrowserPanelQueryDigest(q) {
		return errors.New("browser input pins do not match the saved query")
	}
	if expectedVersion == 1 {
		if len(p.PolicyPins) != 0 || p.SelectionReceiptSHA256 != "" {
			return errors.New("browser input pins do not match the saved query")
		}
	} else {
		if !validateBrowserPanelPolicyPins(p.PolicyPins) || !slices.Equal(p.PolicyPins, p.Current.PolicyPins) {
			return errors.New("browser input pins do not match the saved query")
		}
		if p.SelectionReceiptSHA256 != "" && !browserPanelDigestValid(p.SelectionReceiptSHA256) {
			return errors.New("browser input pins do not match the saved query")
		}
	}
	if (q.ComparisonRunID == "") != (p.Baseline == nil) || p.Baseline != nil && (!validateBrowserPanelRunInput(*p.Baseline, expectedVersion) || p.Baseline.RunID != q.ComparisonRunID) {
		return errors.New("browser baseline input does not match the selected run")
	}
	return nil
}
