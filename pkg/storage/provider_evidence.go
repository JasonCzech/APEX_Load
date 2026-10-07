package storage

import (
	"errors"
	"strings"
	"time"
)

const evidenceTimestampLayout = "2006-01-02T15:04:05.000000000Z"

// EvidenceHeadFilter operates on bounded metadata stored with the canonical
// head. Filtering before LIMIT keeps pagination truthful for sparse matches.
type EvidenceHeadFilter struct {
	Kind     string
	Service  string
	Severity string
	TraceID  string
	From     *time.Time
	Until    *time.Time
}

func (f EvidenceHeadFilter) Validate() error {
	for _, value := range []string{f.Kind, f.Service, f.Severity, f.TraceID} {
		if len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("invalid evidence filter")
		}
	}
	for _, value := range []*time.Time{f.From, f.Until} {
		if value != nil && (value.IsZero() || value.Location() != time.UTC || value.Year() < 1 || value.Year() > 9999) {
			return errors.New("invalid evidence filter time")
		}
	}
	if f.From != nil && f.Until != nil && !f.Until.After(*f.From) {
		return errors.New("evidence until must be after from")
	}
	return nil
}

func (s *SQLiteStore) ListProviderEvidenceHeads(project, run, source, after string, filter EvidenceHeadFilter, limit int) ([]ObservationHead, error) {
	if limit < 1 || limit > 1000 || filter.Validate() != nil || len(after) > 64 {
		return nil, errors.New("invalid evidence head page")
	}
	from, until := "", ""
	if filter.From != nil {
		from = filter.From.Format(evidenceTimestampLayout)
	}
	if filter.Until != nil {
		until = filter.Until.Format(evidenceTimestampLayout)
	}
	rows, err := s.db.Query(`SELECT h.job_id,h.record_key,h.sequence,h.record_index,h.record_kind,h.content_digest FROM run_monitor_observation_heads h JOIN run_monitor_jobs j ON j.id=h.job_id WHERE j.project_id=? AND j.test_id=? AND (?='' OR j.id=?) AND h.record_kind='diagnostic' AND h.record_key>? AND (?='' OR h.diagnostic_kind=?) AND (?='' OR h.evidence_service=?) AND (?='' OR h.evidence_severity=?) AND (?='' OR h.evidence_trace_id=?) AND (?='' OR h.evidence_time>=?) AND (?='' OR h.evidence_time<?) ORDER BY h.record_key,h.job_id LIMIT ?`, project, run, source, run+":"+source, after, filter.Kind, filter.Kind, filter.Service, filter.Service, filter.Severity, filter.Severity, filter.TraceID, filter.TraceID, from, from, until, until, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ObservationHead{}
	for rows.Next() {
		var h ObservationHead
		if err = rows.Scan(&h.JobID, &h.Key, &h.Sequence, &h.Index, &h.Kind, &h.ContentDigest); err != nil {
			return nil, err
		}
		result = append(result, h)
	}
	return result, rows.Err()
}
