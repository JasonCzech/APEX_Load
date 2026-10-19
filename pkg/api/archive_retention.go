package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type ArchiveRetentionPlanResponse struct {
	GeneratedAt   time.Time                    `json:"generated_at"`
	DryRun        bool                         `json:"dry_run"`
	Candidates    []ArchivePruneCandidate      `json:"candidates"`
	Results       []ArchiveRetentionResult     `json:"results,omitempty"`
	HARCandidates []HARArtifactPruneCandidate  `json:"har_candidates,omitempty"`
	HARResults    []HARArtifactRetentionResult `json:"har_results,omitempty"`
	Summary       ArchiveRetentionRunSummary   `json:"summary"`
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
	CandidateCount    int `json:"candidate_count"`
	PrunedCount       int `json:"pruned_count"`
	FailedCount       int `json:"failed_count"`
	HARCandidateCount int `json:"har_candidate_count,omitempty"`
	HARPrunedCount    int `json:"har_pruned_count,omitempty"`
	HARFailedCount    int `json:"har_failed_count,omitempty"`
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
		retention, ok := checkedDuration(policy.RetentionDays, 24*time.Hour)
		if !ok || retention <= 0 {
			// Invalid legacy settings must fail closed: never turn integer wrap into
			// an immediate destructive prune.
			continue
		}
		expiredAt := completedAt.Add(retention)
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
	settings, tests := s.archiveDomainService().RetentionInputs()
	candidates := planArchiveRetentionPrune(now, settings, tests)
	harCandidates := planHARArtifactRetention(now, settings)
	writeJSON(w, http.StatusOK, ArchiveRetentionPlanResponse{
		GeneratedAt:   now,
		DryRun:        true,
		Candidates:    candidates,
		HARCandidates: harCandidates,
		Summary: ArchiveRetentionRunSummary{
			CandidateCount:    len(candidates),
			HARCandidateCount: len(harCandidates),
		},
	})
}

func (s *Server) handleApplyArchiveRetention(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	now := time.Now().UTC()
	archives := s.archiveDomainService()
	settings, tests := archives.RetentionInputs()
	candidates := planArchiveRetentionPrune(now, settings, tests)
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
		record, ok, err := archives.GetRetentionArchive(candidate.TestID)
		if err != nil {
			result.Status = "failed"
			result.Error = "load durable archive record: " + err.Error()
			summary.FailedCount++
			results = append(results, result)
			continue
		}
		if !ok && candidate.ArchivePath != "" {
			record = &storage.RunArchiveRecord{TestID: candidate.TestID, ArchivePath: candidate.ArchivePath}
		}
		if err := s.pruneRunArchive(r.Context(), record); err != nil {
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

	harCandidates := planHARArtifactRetention(now, settings)
	harResults, harPruned, harFailed := applyHARArtifactRetention(harCandidates)
	summary.HARCandidateCount = len(harCandidates)
	summary.HARPrunedCount = harPruned
	summary.HARFailedCount = harFailed
	for i, harResult := range harResults {
		s.recordAuditEvent(r, "archive.retention.har_prune", harResult.Status, "har_artifact", harCandidates[i].Path, "", map[string]any{
			"path":           harCandidates[i].Path,
			"sensitivity":    harCandidates[i].Sensitivity,
			"retention_days": harCandidates[i].RetentionDays,
			"expired_at":     harCandidates[i].ExpiredAt,
			"status":         harResult.Status,
		})
	}

	writeJSON(w, http.StatusOK, ArchiveRetentionPlanResponse{
		GeneratedAt:   now,
		DryRun:        false,
		Candidates:    candidates,
		Results:       results,
		HARCandidates: harCandidates,
		HARResults:    harResults,
		Summary:       summary,
	})
}

type conditionalRunArchiveMetadataDeleter interface {
	DeleteRunArchiveIfUnchanged(expected *storage.RunArchiveRecord) (bool, error)
}

var errArchivePublicationChanged = errors.New("archive publication changed while retention was pending")

// pruneRunArchive deletes durable object data first, then the local staging
// directory, and finally the database pointer. That ordering prevents a
// metadata-only prune from being undone by ensureArchiveMaterialized.
func (s *Server) pruneRunArchive(ctx context.Context, record *storage.RunArchiveRecord) error {
	if record == nil || strings.TrimSpace(record.TestID) == "" {
		return fmt.Errorf("run archive record is required")
	}
	if s.runner != nil {
		if !s.runner.isExecutionLeader() {
			return errNotExecutionLeader
		}
		unlock := s.runner.lockArchiveCommitFence(record.TestID)
		defer unlock()
		operationCtx, cancel := s.runner.archiveOperationContext(ctx)
		defer cancel()
		if err := s.pruneRunArchiveUnderFence(operationCtx, record); err != nil {
			return err
		}
		return s.completeArchiveCleanupIntent(record.TestID)
	}
	if err := s.pruneRunArchiveUnderFence(ctx, record); err != nil {
		return err
	}
	return s.completeArchiveCleanupIntent(record.TestID)
}

func (s *Server) pruneRunArchiveUnderFence(ctx context.Context, record *storage.RunArchiveRecord) error {
	if record == nil || strings.TrimSpace(record.TestID) == "" {
		return fmt.Errorf("run archive record is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.ensureArchiveCleanupIntent(record); err != nil {
		return err
	}
	deleter, supportsConditionalDelete := s.store.(conditionalRunArchiveMetadataDeleter)
	if s.store != nil && !supportsConditionalDelete {
		return fmt.Errorf("conditional archive metadata deletion is not supported by the configured store")
	}
	if s.runner != nil {
		if !s.runner.isExecutionLeader() {
			return errNotExecutionLeader
		}
		// Retention/cleanup is a durable cancellation of any publication that
		// still owns this local source. Delete its intent while holding the same
		// fence before removing objects or the catalog pointer.
		if outbox := s.runner.archiveFinalizationOutbox(); outbox != nil {
			if err := outbox.DeleteArchiveFinalizationJob(record.TestID); err != nil {
				return fmt.Errorf("cancel archive finalization before prune: %w", err)
			}
		}
		if outbox := s.runner.archiveRefreshOutbox(); outbox != nil {
			if err := outbox.DeleteArchiveRefreshJob(record.TestID); err != nil {
				return fmt.Errorf("cancel archive refresh before prune: %w", err)
			}
		}
	}
	if err := deleteArchiveBackendObjects(ctx, record); err != nil {
		return err
	}
	if err := removeLocalArchiveDirectory(record.ArchivePath); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.runner != nil && !s.runner.isExecutionLeader() {
		return errNotExecutionLeader
	}
	if supportsConditionalDelete {
		deleted, err := deleter.DeleteRunArchiveIfUnchanged(record)
		if err != nil {
			return fmt.Errorf("delete archive metadata: %w", err)
		}
		if !deleted {
			current, loadErr := s.store.GetRunArchive(record.TestID)
			if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
				return fmt.Errorf("confirm archive metadata deletion: %w", loadErr)
			}
			if current != nil || loadErr == nil {
				return errArchivePublicationChanged
			}
		}
	}
	s.archiveReadCaches().forgetRunArchiveCache(record.TestID)
	if s.runner != nil {
		s.runner.mu.Lock()
		delete(s.runner.archives, record.TestID)
		if test := s.runner.tests[record.TestID]; test != nil {
			test.Archive = nil
		}
		s.runner.mu.Unlock()
	}
	return nil
}

func (s *Server) ensureArchiveCleanupIntent(record *storage.RunArchiveRecord) error {
	outbox := s.archiveCleanupOutbox()
	if outbox == nil {
		return fmt.Errorf("archive cleanup durability is not supported by the configured store")
	}
	if existing, err := outbox.GetArchiveCleanupJob(record.TestID); err == nil {
		if !sameRunArchiveRecordVersion(&existing.ArchiveRecord, record, s.store) {
			return errArchivePublicationChanged
		}
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load archive cleanup intent: %w", err)
	}
	projectID := storage.DefaultProjectID
	if s.runner == nil || s.runner.testStore == nil {
		return fmt.Errorf("cannot create archive cleanup intent without durable test storage")
	}
	test, err := s.runner.testStore.GetTest(record.TestID)
	if err != nil {
		return fmt.Errorf("load test for archive cleanup intent: %w", err)
	}
	if test != nil {
		projectID = normalizeProjectID(test.ProjectID)
	}
	conditionalOutbox, ok := s.store.(conditionalArchiveCleanupOutbox)
	if !ok {
		return fmt.Errorf("conditional archive cleanup intent is not supported by the configured store")
	}
	copy := *record
	created, err := conditionalOutbox.SaveArchiveCleanupJobIfArchiveUnchanged(&storage.ArchiveCleanupJob{
		TestID: record.TestID, ProjectID: projectID, ArchiveRecord: copy,
	})
	if err != nil {
		return fmt.Errorf("save archive cleanup intent: %w", err)
	}
	if !created {
		return errArchivePublicationChanged
	}
	return nil
}

// A cached record keeps the run's nanosecond clock while PostgreSQL stores
// microseconds, so the publication times compare at metadataStore's precision.
func sameRunArchiveRecordVersion(left, right *storage.RunArchiveRecord, metadataStore any) bool {
	if left == nil || right == nil {
		return false
	}
	return left.TestID == right.TestID &&
		left.RefKey == right.RefKey &&
		left.SchemaVersion == right.SchemaVersion &&
		left.ArchiveBackend == right.ArchiveBackend &&
		left.ArchivePath == right.ArchivePath &&
		left.ManifestPath == right.ManifestPath &&
		left.ManifestSHA256 == right.ManifestSHA256 &&
		left.ArchiveURI == right.ArchiveURI &&
		left.ManifestURI == right.ManifestURI &&
		left.TransactionCount == right.TransactionCount &&
		left.ResourceSampleCount == right.ResourceSampleCount &&
		left.IntervalSummaryCount == right.IntervalSummaryCount &&
		archiveRunTimestampMatches(left.CreatedAt, right.CreatedAt, metadataStore) &&
		archiveRunTimestampMatches(left.CompletedAt, right.CompletedAt, metadataStore)
}

func (s *Server) completeArchiveCleanupIntent(testID string) error {
	outbox := s.archiveCleanupOutbox()
	if outbox == nil {
		return nil
	}
	if err := outbox.DeleteArchiveCleanupJob(testID); err != nil {
		return fmt.Errorf("complete archive cleanup intent: %w", err)
	}
	return nil
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
