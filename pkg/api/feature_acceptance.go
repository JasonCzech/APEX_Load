package api

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Feature acceptance records hold the qualification evidence behind support
// claims: which commit and binary ran which suite on which profile, provider
// and platform, with hashes of the sanitized evidence. Records are
// append-only; a newer failed, pending or revoked record of the subject, or
// of a broader subject that covers it, withdraws its claim.

var (
	acceptanceRequirementPattern = regexp.MustCompile(`^[A-Z]{1,6}-[0-9A-Za-z]{1,8}$`)
	acceptanceSlugPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}$`)
	acceptanceCommitPattern      = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	acceptanceSHA256Pattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// FeatureAcceptanceRequest records one qualification result.
type FeatureAcceptanceRequest struct {
	RequirementID string                              `json:"requirement_id"`
	Feature       string                              `json:"feature"`
	Profile       string                              `json:"profile,omitempty"`
	Provider      string                              `json:"provider,omitempty"`
	Platform      string                              `json:"platform,omitempty"`
	CommitSHA     string                              `json:"commit_sha"`
	BinarySHA256  string                              `json:"binary_sha256,omitempty"`
	Suite         string                              `json:"suite"`
	Result        string                              `json:"result"`
	Artifacts     []storage.FeatureAcceptanceArtifact `json:"artifacts,omitempty"`
	Notes         string                              `json:"notes,omitempty"`
}

func (req FeatureAcceptanceRequest) validate() error {
	if !acceptanceRequirementPattern.MatchString(req.RequirementID) {
		return errors.New("requirement_id must look like BE-4 or TEL-4")
	}
	if !acceptanceSlugPattern.MatchString(req.Feature) {
		return errors.New("feature must be a lowercase slug")
	}
	for _, optional := range []string{req.Profile, req.Provider, req.Platform} {
		if optional != "" && !acceptanceSlugPattern.MatchString(optional) {
			return errors.New("profile, provider and platform must be lowercase slugs")
		}
	}
	if !acceptanceCommitPattern.MatchString(req.CommitSHA) {
		return errors.New("commit_sha must be a lowercase hex commit")
	}
	if req.BinarySHA256 != "" && !acceptanceSHA256Pattern.MatchString(req.BinarySHA256) {
		return errors.New("binary_sha256 must be a lowercase SHA-256")
	}
	if strings.TrimSpace(req.Suite) == "" || len(req.Suite) > 256 {
		return errors.New("suite is required and at most 256 bytes")
	}
	switch req.Result {
	case storage.AcceptancePassed, storage.AcceptanceFailed, storage.AcceptancePending, storage.AcceptanceRevoked:
	default:
		return errors.New("result must be passed, failed, pending or revoked")
	}
	if req.Result == storage.AcceptancePassed && len(req.Artifacts) == 0 {
		// A passing claim must name the evidence it rests on.
		return errors.New("a passed result requires at least one artifact hash")
	}
	if len(req.Artifacts) > 64 {
		return errors.New("at most 64 artifacts")
	}
	for _, artifact := range req.Artifacts {
		if strings.TrimSpace(artifact.Name) == "" || len(artifact.Name) > 256 || !acceptanceSHA256Pattern.MatchString(artifact.SHA256) {
			return errors.New("each artifact needs a name and a lowercase SHA-256")
		}
	}
	if len(req.Notes) > 256 {
		return errors.New("notes exceed 256 bytes")
	}
	return nil
}

// FeatureSupportStatus is the latest result for one acceptance subject.
// Supported needs the subject's latest record to be a pass, not withdrawn by
// a newer failed, pending or revoked record of a broader subject (one whose
// unset profile, provider or platform covers this one), and, when the query
// names a commit or binary, recorded for exactly that commit or binary.
type FeatureSupportStatus struct {
	RequirementID string                           `json:"requirement_id"`
	Feature       string                           `json:"feature"`
	Profile       string                           `json:"profile,omitempty"`
	Provider      string                           `json:"provider,omitempty"`
	Platform      string                           `json:"platform,omitempty"`
	Supported     bool                             `json:"supported"`
	Reason        string                           `json:"reason,omitempty"`
	Latest        *storage.FeatureAcceptanceRecord `json:"latest"`
	WithdrawnBy   *storage.FeatureAcceptanceRecord `json:"withdrawn_by,omitempty"`
}

// Reasons a subject is not supported.
const (
	acceptanceReasonLatestNotPassed = "latest_not_passed"
	acceptanceReasonWithdrawn       = "withdrawn_by_broader_record"
	acceptanceReasonCommitMismatch  = "commit_mismatch"
	acceptanceReasonBinaryMismatch  = "binary_mismatch"
)

// maxAcceptanceSubjects bounds one status response.
var maxAcceptanceSubjects = 5000

// FeatureAcceptanceListResponse pages acceptance records, newest first.
type FeatureAcceptanceListResponse struct {
	Records []*storage.FeatureAcceptanceRecord `json:"records"`
}

// FeatureSupportStatusResponse is the support status of every matching
// subject; truncated means more subjects exist than one response holds.
type FeatureSupportStatusResponse struct {
	Statuses  []FeatureSupportStatus `json:"statuses"`
	Truncated bool                   `json:"truncated"`
}

func (s *Server) handleListFeatureAcceptanceRecords(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(storage.FeatureAcceptanceStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "feature acceptance records unavailable")
		return
	}
	limit, offset := parseLimitOffsetQuery(r, 100)
	records, err := store.ListFeatureAcceptanceRecords(r.URL.Query().Get("requirement"), r.URL.Query().Get("feature"), min(limit, 500), offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot list feature acceptance records")
		return
	}
	writeJSON(w, http.StatusOK, FeatureAcceptanceListResponse{Records: records})
}

func (s *Server) handleCreateFeatureAcceptanceRecord(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(storage.FeatureAcceptanceStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "feature acceptance records unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "acceptance record exceeds its byte limit")
			return
		}
		writeError(w, http.StatusBadRequest, "cannot read acceptance record")
		return
	}
	var req FeatureAcceptanceRequest
	if err := decodeAnalysisDefinitionJSON(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid acceptance record: strings are at most 256 bytes and unknown or repeated fields are refused")
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	actorType, actorSubject, _ := s.resolveAuditActor(r)
	record := &storage.FeatureAcceptanceRecord{
		ID: uuid.NewString(), RequirementID: req.RequirementID, Feature: req.Feature, Profile: req.Profile, Provider: req.Provider,
		Platform: req.Platform, CommitSHA: req.CommitSHA, BinarySHA256: req.BinarySHA256, Suite: req.Suite, Result: req.Result,
		Artifacts: req.Artifacts, Notes: req.Notes, RecordedAt: time.Now().UTC(), RecordedBy: strings.Trim(actorType+":"+actorSubject, ":"),
	}
	if err := store.AppendFeatureAcceptanceRecord(record); err != nil {
		writeError(w, http.StatusServiceUnavailable, "cannot record feature acceptance")
		return
	}
	s.recordAuditEvent(r, "feature_acceptance.record", "success", "feature_acceptance", record.ID, "", map[string]any{"requirement_id": record.RequirementID, "feature": record.Feature, "result": record.Result})
	writeJSON(w, http.StatusCreated, record)
}

// handleFeatureAcceptanceStatus reports, per subject, whether it is
// supported (see FeatureSupportStatus). A subject without records is absent.
// Optional profile, provider and platform filters narrow the subjects after
// broader records are applied; commit_sha or binary_sha256 ask whether that
// exact build is supported.
func (s *Server) handleFeatureAcceptanceStatus(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(storage.FeatureAcceptanceStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "feature acceptance records unavailable")
		return
	}
	query := r.URL.Query()
	commit, binary := strings.ToLower(query.Get("commit_sha")), strings.ToLower(query.Get("binary_sha256"))
	if commit != "" && !acceptanceCommitPattern.MatchString(commit) || binary != "" && !acceptanceSHA256Pattern.MatchString(binary) {
		writeError(w, http.StatusBadRequest, "commit_sha must be a hex commit and binary_sha256 a hex SHA-256")
		return
	}
	latest, truncated, err := store.LatestFeatureAcceptanceRecords(query.Get("requirement"), query.Get("feature"), maxAcceptanceSubjects)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read feature acceptance records")
		return
	}
	withdrawals, withdrawalsTruncated, err := store.LatestFeatureAcceptanceWithdrawals(query.Get("requirement"), query.Get("feature"), maxAcceptanceSubjects)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read feature acceptance records")
		return
	}
	truncated = truncated || withdrawalsTruncated
	statuses := resolveFeatureSupport(latest, withdrawals, commit, binary)
	filtered := statuses[:0]
	for _, status := range statuses {
		if dimensionMatches(query.Get("profile"), status.Profile) && dimensionMatches(query.Get("provider"), status.Provider) && dimensionMatches(query.Get("platform"), status.Platform) {
			filtered = append(filtered, status)
		}
	}
	writeJSON(w, http.StatusOK, FeatureSupportStatusResponse{Statuses: filtered, Truncated: truncated})
}

func dimensionMatches(filter, value string) bool {
	return filter == "" || filter == value
}

// acceptanceRecordNewer orders records by their database sequence, falling
// back to time for records written before the sequence existed.
func acceptanceRecordNewer(a, b *storage.FeatureAcceptanceRecord) bool {
	if a.Seq != b.Seq {
		return a.Seq > b.Seq
	}
	if !a.RecordedAt.Equal(b.RecordedAt) {
		return a.RecordedAt.After(b.RecordedAt)
	}
	return a.ID > b.ID
}

// acceptanceSubjectCovers reports whether broad's set dimensions all equal
// narrow's; an unset dimension covers every value.
func acceptanceSubjectCovers(broad, narrow *storage.FeatureAcceptanceRecord) bool {
	return broad.RequirementID == narrow.RequirementID && broad.Feature == narrow.Feature &&
		(broad.Profile == "" || broad.Profile == narrow.Profile) &&
		(broad.Provider == "" || broad.Provider == narrow.Provider) &&
		(broad.Platform == "" || broad.Platform == narrow.Platform)
}

func commitMatches(recorded, queried string) bool {
	return strings.HasPrefix(recorded, queried) || strings.HasPrefix(queried, recorded)
}

func sameAcceptanceSubject(a, b *storage.FeatureAcceptanceRecord) bool {
	return a.RequirementID == b.RequirementID && a.Feature == b.Feature && a.Profile == b.Profile && a.Provider == b.Provider && a.Platform == b.Platform
}

// resolveFeatureSupport judges each subject by its latest record. A passed
// subject is withdrawn by any broader subject's newest failed, pending or
// revoked record that came after the pass; a later broad pass does not
// restore it, because only a new pass of the subject itself re-qualifies it.
func resolveFeatureSupport(latest, withdrawals []*storage.FeatureAcceptanceRecord, commit, binary string) []FeatureSupportStatus {
	statuses := make([]FeatureSupportStatus, 0, len(latest))
	for _, record := range latest {
		status := FeatureSupportStatus{
			RequirementID: record.RequirementID, Feature: record.Feature, Profile: record.Profile, Provider: record.Provider,
			Platform: record.Platform, Supported: record.Result == storage.AcceptancePassed, Latest: record,
		}
		if !status.Supported {
			status.Reason = acceptanceReasonLatestNotPassed
		}
		for _, broader := range withdrawals {
			if !status.Supported {
				break
			}
			if broader.Result == storage.AcceptancePassed || sameAcceptanceSubject(broader, record) || !acceptanceSubjectCovers(broader, record) || !acceptanceRecordNewer(broader, record) {
				continue
			}
			status.Supported, status.Reason, status.WithdrawnBy = false, acceptanceReasonWithdrawn, broader
		}
		if status.Supported && commit != "" && !commitMatches(record.CommitSHA, commit) {
			status.Supported, status.Reason = false, acceptanceReasonCommitMismatch
		}
		if status.Supported && binary != "" && record.BinarySHA256 != binary {
			status.Supported, status.Reason = false, acceptanceReasonBinaryMismatch
		}
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(i, j int) bool {
		a, b := statuses[i], statuses[j]
		return strings.Join([]string{a.RequirementID, a.Feature, a.Profile, a.Provider, a.Platform}, "\x00") < strings.Join([]string{b.RequirementID, b.Feature, b.Profile, b.Provider, b.Platform}, "\x00")
	})
	return statuses
}
