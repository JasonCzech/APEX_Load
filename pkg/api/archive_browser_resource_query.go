package api

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const maxBrowserResourceReadEntries = 2_000_000
const maxBrowserResourceRecordEntries = 4096
const maxBrowserResourceCursorBytes = 4096

// BrowserResourcePageQuery is an approved exact-filter read operation, not an
// expression or SQL interface. Cursor is transport state, excluded from the
// normalized query fingerprint; the limit remains part of that fingerprint.
type BrowserResourcePageQuery struct {
	Limit               int    `json:"limit"`
	SortBy              string `json:"sort_by"`
	SortDir             string `json:"sort_dir"`
	ActionName          string `json:"action_name,omitempty"`
	Group               string `json:"group,omitempty"`
	Region              string `json:"region,omitempty"`
	ResourceType        string `json:"resource_type,omitempty"`
	FailureKind         string `json:"failure_kind,omitempty"`
	State               string `json:"state,omitempty"`
	SourceGeneration    string `json:"source_generation,omitempty"`
	RequestID           string `json:"request_id,omitempty"`
	RedirectChainID     string `json:"redirect_chain_id,omitempty"`
	DocumentID          string `json:"document_id,omitempty"`
	InitiatingActionID  string `json:"initiating_action_id,omitempty"`
	ObservationActionID string `json:"observation_action_id,omitempty"`
	Origin              string `json:"origin,omitempty"`
	BrowserEngine       string `json:"browser_engine,omitempty"`
	DeviceProfile       string `json:"device_profile,omitempty"`
	Cursor              string `json:"-"`
}

var errBrowserResourceQuery = errors.New("invalid browser resource query")

// ParseBrowserResourcePageQuery refuses unsupported, repeated or empty filter
// fields rather than silently broadening a query. project_id is owned by the
// route's project authorization and never takes the place of durable scope.
func ParseBrowserResourcePageQuery(values url.Values) (BrowserResourcePageQuery, error) {
	q := BrowserResourcePageQuery{Limit: 50, SortBy: "capture_order", SortDir: "asc"}
	invalid := errBrowserResourceQuery
	allowed := map[string]bool{"project_id": true, "limit": true, "sort_by": true, "sort_dir": true, "cursor": true, "action_name": true, "group": true, "region": true, "resource_type": true, "failure_kind": true, "state": true, "source_generation": true, "request_id": true, "redirect_chain_id": true, "document_id": true, "initiating_action_id": true, "observation_action_id": true, "origin": true, "browser_engine": true, "device_profile": true}
	for key, value := range values {
		if !allowed[key] || len(value) != 1 || value[0] == "" {
			return q, invalid
		}
	}
	if project := values.Get("project_id"); !browserResourceReadText(project, 128) {
		return q, invalid
	}
	if raw := values.Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > MaxBrowserResourcePageItems {
			return q, invalid
		}
		q.Limit = v
	}
	if raw := values.Get("sort_by"); raw != "" {
		q.SortBy = raw
	}
	if raw := values.Get("sort_dir"); raw != "" {
		q.SortDir = raw
	}
	switch q.SortBy {
	case "capture_order", "duration_ms", "native_start_ms", "response_encoded_body":
	default:
		return q, invalid
	}
	if q.SortDir != "asc" && q.SortDir != "desc" {
		return q, invalid
	}
	fields := []struct {
		key    string
		target *string
		max    int
	}{{"action_name", &q.ActionName, 256}, {"group", &q.Group, 256}, {"region", &q.Region, 256}, {"resource_type", &q.ResourceType, 64}, {"failure_kind", &q.FailureKind, 32}, {"state", &q.State, 16}, {"source_generation", &q.SourceGeneration, 32}, {"request_id", &q.RequestID, 128}, {"redirect_chain_id", &q.RedirectChainID, 128}, {"document_id", &q.DocumentID, 128}, {"initiating_action_id", &q.InitiatingActionID, 128}, {"observation_action_id", &q.ObservationActionID, 128}, {"origin", &q.Origin, 512}, {"browser_engine", &q.BrowserEngine, 32}, {"device_profile", &q.DeviceProfile, 128}, {"cursor", &q.Cursor, maxBrowserResourceCursorBytes}}
	for _, field := range fields {
		*field.target = values.Get(field.key)
		if !browserResourceReadText(*field.target, field.max) {
			return q, invalid
		}
	}
	switch q.FailureKind {
	case "", "http", "transport", "timeout", "canceled", "body_limit":
	default:
		return q, invalid
	}
	switch q.State {
	case "", "finished", "failed", "censored":
	default:
		return q, invalid
	}
	if q.SourceGeneration != "" && !protocol.BrowserEvidenceGeneration(q.SourceGeneration) {
		return q, invalid
	}
	generation := q.SourceGeneration
	for _, id := range []string{q.RequestID, q.RedirectChainID, q.DocumentID, q.InitiatingActionID, q.ObservationActionID} {
		if id == "" {
			continue
		}
		parts := strings.SplitN(id, "-", 2)
		if len(parts) != 2 || !protocol.BrowserEvidenceGeneration(parts[0]) {
			return q, invalid
		}
		if generation != "" && generation != parts[0] {
			return q, invalid
		}
		generation = parts[0]
		for _, r := range parts[1] {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '/' || r == '_' || r == '.') {
				return q, invalid
			}
		}
	}
	if q.Cursor != "" && !browserResourceCursorShape(q.Cursor) {
		return q, invalid
	}
	if q.Origin != "" {
		parsed, err := url.Parse(q.Origin)
		if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" || q.Origin != parsed.Scheme+"://"+parsed.Host {
			return q, invalid
		}
	}
	return q, nil
}

func browserResourceMatches(item BrowserResourcePageItem, q BrowserResourcePageQuery) bool {
	for _, pair := range [][2]string{{q.ActionName, item.ActionName}, {q.Group, item.Group}, {q.Region, item.Region}, {q.ResourceType, item.ResourceType}, {q.FailureKind, item.FailureKind}, {q.BrowserEngine, item.BrowserEngine}, {q.DeviceProfile, item.DeviceProfile}} {
		if pair[0] != "" && pair[0] != pair[1] {
			return false
		}
	}
	e := item.BrowserEvidence
	if e == nil {
		return q.State == "" && q.SourceGeneration == "" && q.RequestID == "" && q.RedirectChainID == "" && q.DocumentID == "" && q.InitiatingActionID == "" && q.ObservationActionID == "" && q.Origin == ""
	}
	for _, pair := range [][2]string{{q.State, e.State}, {q.SourceGeneration, e.SourceGeneration}, {q.RequestID, e.RequestID}, {q.RedirectChainID, e.RedirectChainID}, {q.DocumentID, e.DocumentID}, {q.InitiatingActionID, e.InitiatingActionID}, {q.ObservationActionID, e.ObservationActionID}, {q.Origin, e.Origin}} {
		if pair[0] != "" && pair[0] != pair[1] {
			return false
		}
	}
	return true
}
