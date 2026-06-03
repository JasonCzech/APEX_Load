package api

import (
	"net/http"
	"os"
	"strings"
	"time"
)

type ArchiveRetentionPlanResponse struct {
	GeneratedAt time.Time                  `json:"generated_at"`
	DryRun      bool                       `json:"dry_run"`
	Candidates  []ArchivePruneCandidate    `json:"candidates"`
	Results     []ArchiveRetentionResult   `json:"results,omitempty"`
	Summary     ArchiveRetentionRunSummary `json:"summary"`
}

type ArchiveRetentionResult struct {
	TestID          string `json:"test_id"`
	ArchivePath     string `json:"archive_path"`
	Status          string `json:"status"`
	ComplianceClass string `json:"compliance_class,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Error           string `json:"error,omitempty"`
}

type ArchiveRetentionRunSummary struct {
	CandidateCount int `json:"candidate_count"`
	PrunedCount    int `json:"pruned_count"`
	FailedCount    int `json:"failed_count"`
}

type ArchivePruneCandidate struct {
	TestID          string    `json:"test_id"`
	ProjectID       string    `json:"project_id,omitempty"`
	ReleaseID       string    `json:"release_id,omitempty"`
	RunStatus       string    `json:"run_status,omitempty"`
	ArchivePath     string    `json:"archive_path"`
	PolicyName      string    `json:"policy_name,omitempty"`
	ArtifactType    string    `json:"artifact_type"`
	ComplianceClass string    `json:"compliance_class,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	RetentionDays   int       `json:"retention_days"`
	CompletedAt     time.Time `json:"completed_at"`
	ExpiredAt       time.Time `json:"expired_at"`
}

func planArchiveRetentionPrune(now time.Time, settings ArchiveRetentionSettings, tests []*ManagedTest) []ArchivePruneCandidate {
	if !settings.Enabled || len(tests) == 0 {
		return nil
	}
	settings.Policies = normalizeArchiveRetentionPolicies(settings.Policies)
	if settings.DefaultRetentionDays <= 0 {
		settings.DefaultRetentionDays = defaultSystemSettings(0).ArchiveRetention.DefaultRetentionDays
	}
	candidates := make([]ArchivePruneCandidate, 0)
	for _, test := range tests {
		if test == nil || test.Archive == nil || strings.TrimSpace(test.Archive.ArchivePath) == "" {
			continue
		}
		completedAt := archiveRetentionCompletedAt(test)
		if completedAt.IsZero() {
			continue
		}
		policy := matchingArchiveRetentionPolicy(settings, test)
		expiredAt := completedAt.Add(time.Duration(policy.RetentionDays) * 24 * time.Hour)
		if now.Before(expiredAt) {
			continue
		}
		candidates = append(candidates, ArchivePruneCandidate{
			TestID:          test.ID,
			ProjectID:       test.ProjectID,
			ReleaseID:       test.ReleaseID,
			RunStatus:       test.Status,
			ArchivePath:     test.Archive.ArchivePath,
			PolicyName:      policy.Name,
			ArtifactType:    policy.ArtifactType,
			ComplianceClass: policy.ComplianceClass,
			Reason:          policy.Reason,
			RetentionDays:   policy.RetentionDays,
			CompletedAt:     completedAt,
			ExpiredAt:       expiredAt,
		})
	}
	return candidates
}

func archiveRetentionCompletedAt(test *ManagedTest) time.Time {
	if test == nil {
		return time.Time{}
	}
	if test.Archive != nil && !test.Archive.CompletedAt.IsZero() {
		return test.Archive.CompletedAt
	}
	if test.EndedAt != nil {
		return *test.EndedAt
	}
	return time.Time{}
}

func matchingArchiveRetentionPolicy(settings ArchiveRetentionSettings, test *ManagedTest) ArchiveRetentionPolicy {
	for _, policy := range settings.Policies {
		if archiveRetentionPolicyMatches(policy, test) {
			return policy
		}
	}
	return ArchiveRetentionPolicy{
		Name:            "default",
		ArtifactType:    "bundle",
		ComplianceClass: "standard",
		Reason:          "expired_by_policy",
		RetentionDays:   settings.DefaultRetentionDays,
	}
}

func archiveRetentionPolicyMatches(policy ArchiveRetentionPolicy, test *ManagedTest) bool {
	if test == nil {
		return false
	}
	if policy.ProjectID != "" && policy.ProjectID != test.ProjectID {
		return false
	}
	if policy.ReleaseID != "" && policy.ReleaseID != test.ReleaseID {
		return false
	}
	if policy.RunStatus != "" && !strings.EqualFold(policy.RunStatus, test.Status) {
		return false
	}
	if policy.RunType != "" && policy.RunType != test.ScenarioMode {
		return false
	}
	return true
}

func (s *Server) handlePlanArchiveRetention(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	candidates := planArchiveRetentionPrune(now, s.runner.GetSystemSettings().ArchiveRetention, s.runner.ListTests())
	writeJSON(w, http.StatusOK, ArchiveRetentionPlanResponse{
		GeneratedAt: now,
		DryRun:      true,
		Candidates:  candidates,
		Summary: ArchiveRetentionRunSummary{
			CandidateCount: len(candidates),
		},
	})
}

func (s *Server) handleApplyArchiveRetention(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	candidates := planArchiveRetentionPrune(now, s.runner.GetSystemSettings().ArchiveRetention, s.runner.ListTests())
	results := make([]ArchiveRetentionResult, 0, len(candidates))
	summary := ArchiveRetentionRunSummary{CandidateCount: len(candidates)}
	for _, candidate := range candidates {
		result := ArchiveRetentionResult{
			TestID:          candidate.TestID,
			ArchivePath:     candidate.ArchivePath,
			Status:          "pruned",
			ComplianceClass: candidate.ComplianceClass,
			Reason:          candidate.Reason,
		}
		if err := os.RemoveAll(candidate.ArchivePath); err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			summary.FailedCount++
			s.recordAuditEvent(r, "archive.retention.prune", "failure", "test", candidate.TestID, candidate.ProjectID, archiveRetentionAuditDetails(candidate, result))
		} else {
			summary.PrunedCount++
			s.recordAuditEvent(r, "archive.retention.prune", "success", "test", candidate.TestID, candidate.ProjectID, archiveRetentionAuditDetails(candidate, result))
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, ArchiveRetentionPlanResponse{
		GeneratedAt: now,
		DryRun:      false,
		Candidates:  candidates,
		Results:     results,
		Summary:     summary,
	})
}

func archiveRetentionAuditDetails(candidate ArchivePruneCandidate, result ArchiveRetentionResult) map[string]any {
	details := map[string]any{
		"archive_path":     candidate.ArchivePath,
		"project_id":       candidate.ProjectID,
		"release_id":       candidate.ReleaseID,
		"run_status":       candidate.RunStatus,
		"policy_name":      candidate.PolicyName,
		"artifact_type":    candidate.ArtifactType,
		"compliance_class": candidate.ComplianceClass,
		"reason":           candidate.Reason,
		"retention_days":   candidate.RetentionDays,
		"completed_at":     candidate.CompletedAt,
		"expired_at":       candidate.ExpiredAt,
		"retention_status": result.Status,
	}
	if result.Error != "" {
		details["error"] = result.Error
	}
	return details
}
