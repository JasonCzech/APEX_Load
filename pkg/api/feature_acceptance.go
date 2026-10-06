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
// claims: which commit and binaries ran which suite on which profile,
// provider and platform, with hashes of the sanitized evidence. Records are
// append-only; a claim is withdrawn by recording a newer revoked result.

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
	if len(req.Notes) > 2048 {
		return errors.New("notes exceed 2048 bytes")
	}
	return nil
}

// FeatureSupportStatus is the latest result for one acceptance subject.
type FeatureSupportStatus struct {
	RequirementID string                           `json:"requirement_id"`
	Feature       string                           `json:"feature"`
	Profile       string                           `json:"profile,omitempty"`
	Provider      string                           `json:"provider,omitempty"`
	Platform      string                           `json:"platform,omitempty"`
	Supported     bool                             `json:"supported"`
	Latest        *storage.FeatureAcceptanceRecord `json:"latest"`
}

func (s *Server) handleFeatureAcceptanceRecords(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(storage.FeatureAcceptanceStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "feature acceptance records unavailable")
		return
	}
	if r.Method == http.MethodGet {
		limit, offset := parseLimitOffsetQuery(r, 100)
		records, err := store.ListFeatureAcceptanceRecords(r.URL.Query().Get("requirement"), r.URL.Query().Get("feature"), min(limit, 500), offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cannot list feature acceptance records")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": records})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "acceptance record exceeds its byte limit")
		return
	}
	var req FeatureAcceptanceRequest
	if err := decodeAnalysisDefinitionJSON(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

// handleFeatureAcceptanceStatus reports, per subject, whether the latest
// record is a pass. A subject without records is simply absent.
func (s *Server) handleFeatureAcceptanceStatus(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(storage.FeatureAcceptanceStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "feature acceptance records unavailable")
		return
	}
	latest := map[string]*storage.FeatureAcceptanceRecord{}
	for offset := 0; ; offset += 500 {
		page, err := store.ListFeatureAcceptanceRecords(r.URL.Query().Get("requirement"), r.URL.Query().Get("feature"), 500, offset)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cannot read feature acceptance records")
			return
		}
		for _, record := range page {
			key := strings.Join([]string{record.RequirementID, record.Feature, record.Profile, record.Provider, record.Platform}, "\x00")
			if _, seen := latest[key]; !seen {
				latest[key] = record
			}
		}
		if len(page) < 500 || offset >= 20_000 {
			break
		}
	}
	statuses := make([]FeatureSupportStatus, 0, len(latest))
	for _, record := range latest {
		statuses = append(statuses, FeatureSupportStatus{
			RequirementID: record.RequirementID, Feature: record.Feature, Profile: record.Profile, Provider: record.Provider,
			Platform: record.Platform, Supported: record.Result == storage.AcceptancePassed, Latest: record,
		})
	}
	sort.Slice(statuses, func(i, j int) bool {
		a, b := statuses[i], statuses[j]
		return strings.Join([]string{a.RequirementID, a.Feature, a.Profile, a.Provider, a.Platform}, "\x00") < strings.Join([]string{b.RequirementID, b.Feature, b.Profile, b.Provider, b.Platform}, "\x00")
	})
	writeJSON(w, http.StatusOK, map[string]any{"statuses": statuses})
}
