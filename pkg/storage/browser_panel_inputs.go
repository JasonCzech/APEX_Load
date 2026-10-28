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

// SavedPanelSourcePin freezes one evaluated run archive or committed dataset
// input and its measurement identity for a general saved analysis panel (v2).
type SavedPanelSourcePin struct {
	RunID                 string                  `json:"run_id,omitempty"`
	ManifestSHA256        string                  `json:"manifest_sha256,omitempty"`
	DatasetID             string                  `json:"dataset_id,omitempty"`
	DatasetArtifactSHA256 string                  `json:"dataset_artifact_sha256,omitempty"`
	SourceID              string                  `json:"source_id,omitempty"`
	Protocol              string                  `json:"protocol,omitempty"`
	PolicyPins            []BrowserPanelPolicyPin `json:"policy_pins,omitempty"`
}

// SavedPanelDrilldownBounds freezes bounded drill-down and window parameters,
// including UI defaults resolved at save time so replay never consults later
// project defaults.
type SavedPanelDrilldownBounds struct {
	StartSeconds  int `json:"start_seconds"`
	EndSeconds    int `json:"end_seconds"`
	BucketSeconds int `json:"bucket_seconds"`
	MaxSeries     int `json:"max_series"`
	MaxLagSeconds int `json:"max_lag_seconds,omitempty"`
}

// SavedPanelInputs freezes a general saved analysis or comparison panel (v2)
// to immutable input artifact SHA-256 hashes, policy/catalog revisions,
// UTC vs. elapsed alignment, source/measurement selection, coverage policy,
// and bounded drill-down parameters.
type SavedPanelInputs struct {
	Version            int                       `json:"version"`
	CatalogVersion     string                    `json:"catalog_version"`
	Alignment          string                    `json:"alignment"`
	CoveragePolicy     string                    `json:"coverage_policy"`
	Population         string                    `json:"population,omitempty"`
	Metric             string                    `json:"metric"`
	Aggregation        string                    `json:"aggregation"`
	QuerySHA256        string                    `json:"query_sha256"`
	ComparisonID       string                    `json:"comparison_id,omitempty"`
	ComparisonRevision int64                     `json:"comparison_revision,omitempty"`
	ComparisonSHA256   string                    `json:"comparison_sha256,omitempty"`
	PolicyPins         []BrowserPanelPolicyPin   `json:"policy_pins,omitempty"`
	Drilldown          SavedPanelDrilldownBounds `json:"drilldown"`
	Current            SavedPanelSourcePin       `json:"current"`
	Baseline           *SavedPanelSourcePin      `json:"baseline,omitempty"`
	Overlay            *SavedPanelSourcePin      `json:"overlay,omitempty"`
}

// NormalizeSavedPanelQueryDefaults freezes UI and query defaults on q so saved
// panel definitions and replays never consult later project defaults.
func NormalizeSavedPanelQueryDefaults(q *AnalyticsQuery) {
	if q == nil {
		return
	}
	if q.QueryVersion == 0 {
		q.QueryVersion = AnalyticsQueryVersion
	}
	if q.CatalogVersion == "" {
		if q.PanelInputs != nil && q.PanelInputs.CatalogVersion != "" {
			q.CatalogVersion = q.PanelInputs.CatalogVersion
		} else {
			q.CatalogVersion = analysis.CatalogVersion
		}
	}
	if q.Alignment == "" {
		if q.PanelInputs != nil && q.PanelInputs.Alignment != "" {
			q.Alignment = q.PanelInputs.Alignment
		} else if q.EvidenceMode != "" || q.OverlaySelection != nil {
			q.Alignment = "utc"
		} else {
			q.Alignment = "elapsed"
		}
	}
	if q.CoveragePolicy == "" {
		if q.PanelInputs != nil && q.PanelInputs.CoveragePolicy != "" {
			q.CoveragePolicy = q.PanelInputs.CoveragePolicy
		} else {
			q.CoveragePolicy = "complete_bucket"
		}
	}
	if q.BucketSeconds == 0 {
		if q.PanelInputs != nil && q.PanelInputs.Drilldown.BucketSeconds > 0 {
			q.BucketSeconds = q.PanelInputs.Drilldown.BucketSeconds
		} else {
			q.BucketSeconds = 10
		}
	}
	if q.EndSeconds == 0 && (q.Population != "" || q.PanelInputs != nil) && q.EvidenceMode == "" {
		if q.PanelInputs != nil && q.PanelInputs.Drilldown.EndSeconds > 0 {
			q.EndSeconds = q.PanelInputs.Drilldown.EndSeconds
		} else if q.Population != "" {
			q.EndSeconds = 60
		}
	}
	if q.MaxSeries == 0 {
		if q.PanelInputs != nil && q.PanelInputs.Drilldown.MaxSeries > 0 {
			q.MaxSeries = q.PanelInputs.Drilldown.MaxSeries
		} else if q.Population != "" && q.EvidenceMode == "" && q.BrowserTupleMode != "separate" {
			q.MaxSeries = 1
		} else {
			q.MaxSeries = 20
		}
	}
	if q.OverlaySelection != nil && q.OverlaySelection.DatasetID != "" && q.OverlaySelection.DatasetArtifactSHA256 == "" && q.PanelInputs != nil && q.PanelInputs.Overlay != nil {
		q.OverlaySelection.DatasetArtifactSHA256 = q.PanelInputs.Overlay.DatasetArtifactSHA256
	}
}

// SavedPanelQueryDigest computes the canonical SHA-256 digest of a general
// saved panel query after freezing defaults and excluding input receipts.
func SavedPanelQueryDigest(q AnalyticsQuery) string {
	NormalizeSavedPanelQueryDefaults(&q)
	q.BrowserInputs = nil
	q.PanelInputs = nil
	data, err := json.Marshal(q)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validateSavedPanelSourcePin(pin SavedPanelSourcePin) bool {
	hasRun := pin.RunID != "" || pin.ManifestSHA256 != ""
	hasDataset := pin.DatasetID != "" || pin.DatasetArtifactSHA256 != ""
	if hasRun == hasDataset {
		return false
	}
	if hasRun {
		if pin.RunID == "" || strings.TrimSpace(pin.RunID) != pin.RunID || len(pin.RunID) > 128 || !browserPanelDigestValid(pin.ManifestSHA256) {
			return false
		}
	}
	if hasDataset {
		if pin.DatasetID == "" || strings.TrimSpace(pin.DatasetID) != pin.DatasetID || len(pin.DatasetID) > 128 || !browserPanelDigestValid(pin.DatasetArtifactSHA256) {
			return false
		}
	}
	if len(pin.SourceID) > 256 || strings.TrimSpace(pin.SourceID) != pin.SourceID || len(pin.Protocol) > 64 || strings.TrimSpace(pin.Protocol) != pin.Protocol {
		return false
	}
	return validateBrowserPanelPolicyPins(pin.PolicyPins)
}

// ValidateSavedPanelInputs verifies that a general saved analysis panel (v2)
// freezes input artifact digests, catalog/policy revisions, alignment,
// coverage policy, source/measurement selection, and drill-down bounds.
func ValidateSavedPanelInputs(q AnalyticsQuery) error {
	if q.PanelInputs == nil {
		return nil
	}
	if q.BrowserInputs != nil {
		return errors.New("panel cannot set both browser_inputs and panel_inputs")
	}
	if q.QueryVersion != 0 && q.QueryVersion != AnalyticsQueryVersion && q.QueryVersion != 5 {
		return errors.New("saved panel input pins require query version 3 or 5")
	}
	norm := q
	NormalizeSavedPanelQueryDefaults(&norm)
	p := q.PanelInputs
	if p.Version != 2 || p.CatalogVersion != analysis.CatalogVersion || norm.CatalogVersion != p.CatalogVersion {
		return errors.New("saved panel input pins require version 2 and current catalog_version")
	}
	if (p.Alignment != "elapsed" && p.Alignment != "utc") || norm.Alignment != p.Alignment {
		return errors.New("saved panel input pins require matching elapsed or utc alignment")
	}
	if (p.CoveragePolicy != "complete_bucket" && p.CoveragePolicy != "required") || norm.CoveragePolicy != p.CoveragePolicy {
		return errors.New("saved panel input pins require matching complete_bucket or required coverage_policy")
	}
	if p.Metric == "" || p.Metric != norm.Metric || p.Aggregation == "" || p.Aggregation != norm.Aggregation || p.Population != norm.Population {
		return errors.New("saved panel input pins do not match query measurement selection")
	}
	if p.Drilldown.StartSeconds != norm.StartSeconds || p.Drilldown.EndSeconds != norm.EndSeconds || p.Drilldown.BucketSeconds != norm.BucketSeconds || p.Drilldown.MaxSeries != norm.MaxSeries || p.Drilldown.MaxLagSeconds < 0 || p.Drilldown.MaxLagSeconds > 3600 {
		return errors.New("saved panel input pins do not match frozen drilldown bounds")
	}
	if !browserPanelDigestValid(p.QuerySHA256) || p.QuerySHA256 != SavedPanelQueryDigest(norm) {
		return errors.New("saved panel input pins do not match the saved query")
	}
	if !validateBrowserPanelPolicyPins(p.PolicyPins) || !validateSavedPanelSourcePin(p.Current) {
		return errors.New("saved panel current input pin is invalid")
	}
	if len(p.PolicyPins) > 0 && !slices.Equal(p.PolicyPins, p.Current.PolicyPins) {
		return errors.New("saved panel policy pins do not match current source policy pins")
	}
	if p.Baseline != nil {
		if !validateSavedPanelSourcePin(*p.Baseline) {
			return errors.New("saved panel baseline input pin is invalid")
		}
		if norm.ComparisonRunID != "" && p.Baseline.RunID != norm.ComparisonRunID {
			return errors.New("saved panel baseline input does not match comparison_run_id")
		}
	} else if norm.ComparisonRunID != "" {
		return errors.New("saved panel baseline input pin is required when comparison_run_id is set")
	}
	if norm.OverlaySelection != nil && norm.OverlaySelection.DatasetID != "" {
		if p.Overlay == nil || !validateSavedPanelSourcePin(*p.Overlay) || p.Overlay.DatasetID != norm.OverlaySelection.DatasetID || (norm.OverlaySelection.DatasetArtifactSHA256 != "" && p.Overlay.DatasetArtifactSHA256 != norm.OverlaySelection.DatasetArtifactSHA256) {
			return errors.New("saved panel overlay input pin does not match overlay_selection")
		}
	} else if p.Overlay != nil && !validateSavedPanelSourcePin(*p.Overlay) {
		return errors.New("saved panel overlay input pin is invalid")
	}
	if p.ComparisonID != "" || p.ComparisonRevision != 0 || p.ComparisonSHA256 != "" {
		if p.ComparisonID == "" || strings.TrimSpace(p.ComparisonID) != p.ComparisonID || len(p.ComparisonID) > 128 || p.ComparisonRevision < 1 || !browserPanelDigestValid(p.ComparisonSHA256) {
			return errors.New("saved panel comparison pin is invalid")
		}
	}
	return nil
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
	if q.PanelInputs != nil {
		return ValidateSavedPanelInputs(q)
	}
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
