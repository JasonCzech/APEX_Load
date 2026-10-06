package api

import (
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const BrowserResourcePageVersion = 1
const MaxBrowserResourcePageBytes = 2 << 20
const MaxBrowserResourcePageItems = 100

// Population counts retained entry observations only. Nil counts explicitly
// preserve an unavailable capture projection; a completed explicit empty source
// can retain observed zero without claiming zero issued requests.
type BrowserResourcePagePopulation struct {
	Availability                string `json:"availability"`
	TransactionRecords          int64  `json:"transaction_records"`
	RetainedEntryObservations   *int64 `json:"retained_entry_observations"`
	MatchedRetainedObservations *int64 `json:"matched_retained_observations"`
}
type BrowserResourcePageCoverage struct {
	Availability string `json:"availability"`
	Reason       string `json:"reason"`
}
type BrowserResourcePageLocator struct {
	TransactionOrdinal int64  `json:"transaction_ordinal"`
	ResourceOrdinal    int64  `json:"resource_ordinal"`
	Branch             string `json:"branch"`
}
type BrowserResourcePageItem struct {
	Locator            BrowserResourcePageLocator        `json:"locator"`
	Timestamp          *time.Time                        `json:"timestamp,omitempty"`
	TimestampPrecision string                            `json:"timestamp_precision"`
	ActionName         string                            `json:"action_name,omitempty"`
	Group              string                            `json:"group,omitempty"`
	Region             string                            `json:"region,omitempty"`
	PageURL            string                            `json:"page_url,omitempty"`
	URL                string                            `json:"url,omitempty"`
	Method             string                            `json:"method,omitempty"`
	StatusCode         *int                              `json:"status_code,omitempty"`
	ResourceType       string                            `json:"resource_type,omitempty"`
	FailureKind        string                            `json:"failure_kind,omitempty"`
	DurationMs         *float64                          `json:"duration_ms,omitempty"`
	BytesReceived      *int64                            `json:"bytes_received,omitempty"`
	DeviceProfile      string                            `json:"device_profile,omitempty"`
	DevicePlatform     string                            `json:"device_platform,omitempty"`
	BrowserEngine      string                            `json:"browser_engine,omitempty"`
	EvidenceState      string                            `json:"evidence_state"`
	BrowserEvidence    *protocol.BrowserResourceEvidence `json:"browser_evidence,omitempty"`
}
type BrowserResourcePage struct {
	Version                  int                           `json:"version"`
	TestID                   string                        `json:"test_id"`
	ProjectID                string                        `json:"project_id"`
	SourceFormat             string                        `json:"source_format"`
	SourceRevision           string                        `json:"source_revision"`
	SourceIntegrity          string                        `json:"source_integrity"`
	SourceTimestampPrecision string                        `json:"source_timestamp_precision"`
	Population               BrowserResourcePagePopulation `json:"population"`
	CaptureCoverage          BrowserResourcePageCoverage   `json:"capture_coverage"`
	Items                    []BrowserResourcePageItem     `json:"items"`
	NextCursor               string                        `json:"next_cursor,omitempty"`
}

func browserResourceReadText(s string, limit int) bool {
	return len(s) <= limit && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}
func browserResourceReadPrecision(s string) bool {
	return s == "rfc3339_nanosecond" || s == "unix_millisecond" || s == "unavailable" || s == "mixed"
}
func browserResourceReadSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// ValidateBrowserResourcePage validates the closed owned page projection. Raw
// JSON callers must also enforce exact field presence/types before using it.
func ValidateBrowserResourcePage(page *BrowserResourcePage) bool {
	if page == nil || page.Version != BrowserResourcePageVersion || page.TestID == "" || !browserResourceReadText(page.TestID, 128) || page.ProjectID == "" || !browserResourceReadText(page.ProjectID, 128) || page.SourceFormat != "jsonl" && page.SourceFormat != "parquet" || !browserResourceReadSHA(page.SourceRevision) || page.SourceIntegrity != "verified_manifest_and_artifact" && page.SourceIntegrity != "legacy_scan_fingerprint" || !browserResourceReadPrecision(page.SourceTimestampPrecision) || page.Items == nil || len(page.Items) > MaxBrowserResourcePageItems || page.NextCursor != "" && !browserResourceCursorShape(page.NextCursor) || page.Population.TransactionRecords < 0 || page.Population.TransactionRecords > maxArchiveQueryRecords {
		return false
	}
	if page.CaptureCoverage.Availability != "unavailable" || page.CaptureCoverage.Reason != "retained_observations_are_not_capture_coverage" {
		return false
	}
	p := page.Population
	if p.Availability == "unavailable" {
		if p.RetainedEntryObservations != nil || p.MatchedRetainedObservations != nil || len(page.Items) != 0 || page.NextCursor != "" {
			return false
		}
	} else if p.Availability == "retained_observations" {
		if p.RetainedEntryObservations == nil || p.MatchedRetainedObservations == nil || *p.RetainedEntryObservations < 0 || *p.RetainedEntryObservations > maxBrowserResourceReadEntries || *p.MatchedRetainedObservations < 0 || *p.MatchedRetainedObservations > *p.RetainedEntryObservations || int64(len(page.Items)) > *p.MatchedRetainedObservations {
			return false
		}
	} else {
		return false
	}
	seen := map[BrowserResourcePageLocator]bool{}
	for _, item := range page.Items {
		if !browserResourcePageItemValid(item) || seen[item.Locator] || item.Locator.TransactionOrdinal > p.TransactionRecords {
			return false
		}
		seen[item.Locator] = true
	}
	return true
}
func browserResourcePageItemValid(item BrowserResourcePageItem) bool {
	if item.Locator.TransactionOrdinal < 1 || item.Locator.ResourceOrdinal < 1 || item.Locator.ResourceOrdinal > maxBrowserResourceRecordEntries || item.Locator.Branch != "network_entries" && item.Locator.Branch != "snapshot_network_entries" || !browserResourceReadPrecision(item.TimestampPrecision) || item.TimestampPrecision == "mixed" {
		return false
	}
	if item.Timestamp == nil {
		if item.TimestampPrecision != "unavailable" {
			return false
		}
	} else if item.Timestamp.IsZero() || item.Timestamp.Location() != time.UTC || item.TimestampPrecision == "unavailable" {
		return false
	}
	for _, field := range []struct {
		s string
		n int
	}{{item.ActionName, 256}, {item.Group, 256}, {item.Region, 256}, {item.PageURL, 2048}, {item.URL, 2048}, {item.Method, 32}, {item.ResourceType, 64}, {item.DeviceProfile, 128}, {item.DevicePlatform, 32}, {item.BrowserEngine, 32}} {
		if !browserResourceReadText(field.s, field.n) {
			return false
		}
	}
	if item.StatusCode != nil && (*item.StatusCode < 0 || *item.StatusCode > 999) || item.DurationMs != nil && (math.IsNaN(*item.DurationMs) || math.IsInf(*item.DurationMs, 0) || *item.DurationMs < 0 || *item.DurationMs > 86400000) || item.BytesReceived != nil && (*item.BytesReceived < 0 || *item.BytesReceived > 1<<50) {
		return false
	}
	switch item.FailureKind {
	case "", "http", "transport", "timeout", "canceled", "body_limit":
	default:
		return false
	}
	switch item.EvidenceState {
	case "available":
		return item.BrowserEvidence != nil && item.BrowserEvidence.Clone() != nil
	case "not_captured", "malformed", "unsupported_version":
		return item.BrowserEvidence == nil
	default:
		return false
	}
}
