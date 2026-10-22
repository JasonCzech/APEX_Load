package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Admission uses one inventory snapshot, the existing bounded/integrity-checked
// archive readers and materialization quotas, and a total wall-time deadline.
// It never scans transaction rows or substitutes partial document populations.
func (s *Server) selectRCABrowserBaseline(ctx context.Context, project, testID string, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, policy *rca.BrowserBaselinePolicy) (selection *rca.BrowserBaselineSelection, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.browserSelectionArchiveStable(ctx, testID); err != nil {
		return nil, err
	}
	// Even a refusal receipt must describe settled current evidence. Recheck
	// after inspection, before the cancellation defer releases this deadline.
	defer func() {
		if err == nil {
			if publicationErr := s.browserSelectionArchiveStable(ctx, testID); publicationErr != nil {
				selection, err = nil, publicationErr
			}
		}
	}()
	store, ok := s.store.(interface {
		storage.BrowserBaselineCandidateStore
		GetTestContext(context.Context, string) (*storage.TestRecord, error)
		GetRunArchiveContext(context.Context, string) (*storage.RunArchiveRecord, error)
	})
	if !ok {
		return nil, errors.New("browser baseline inventory unavailable")
	}
	current, err := store.GetTestContext(ctx, testID)
	if err != nil {
		return nil, err
	}
	if current == nil || current.ID != testID || normalizeProjectID(current.ProjectID) != project || !browserSelectionArchiveIdentity(current, record, manifest, s.store) {
		return &rca.BrowserBaselineSelection{Version: rca.BrowserSelectionVersion, Policy: policy.Ref, Status: "current_identity_unavailable"}, nil
	}
	page, more, err := store.ListBrowserBaselineCandidates(ctx, storage.BrowserBaselineCandidateQuery{ProjectID: project, ApplicationID: current.ApplicationID, ScenarioID: current.ScenarioID, CurrentTestID: testID, Limit: rca.MaxBrowserSelectionCandidates})
	if err != nil {
		return nil, err
	}
	candidates := make([]rca.BrowserSelectionCandidate, len(page))
	for i, c := range page {
		candidates[i] = rca.BrowserSelectionCandidate{TestID: c.TestID, CompletedAt: c.CompletedAt}
	}
	input := rca.DetectionInput{BrowserPolicy: policy}
	if !more {
		input, err = browserSelectionDocumentInput(ctx, record, manifest)
		if err != nil {
			return nil, err
		}
		input.BrowserPolicy = policy
	}
	currentKey := comparisonWorkloadKey(&baselineComparisonRun{manifest: manifest}, nil, "")
	return rca.SelectBrowserBaseline(ctx, testID, archiveRunStarted(manifest), input, candidates, more, func(ctx context.Context, c rca.BrowserSelectionCandidate) (rca.BrowserCandidateInspection, error) {
		reject := func(reason string) (rca.BrowserCandidateInspection, error) {
			return rca.BrowserCandidateInspection{Reasons: []string{reason}}, nil
		}
		row, err := store.GetTestContext(ctx, c.TestID)
		if errors.Is(err, sql.ErrNoRows) {
			return reject("candidate_identity_unavailable")
		}
		if err != nil {
			return rca.BrowserCandidateInspection{}, err
		}
		if row == nil || row.ID != c.TestID || row.ID == testID || normalizeProjectID(row.ProjectID) != project || row.ApplicationID != current.ApplicationID || row.ScenarioID != current.ScenarioID || row.Status != "COMPLETE" || row.EndTime == nil || !row.EndTime.Equal(c.CompletedAt) {
			return reject("candidate_identity_changed")
		}
		if err := s.browserSelectionArchiveStable(ctx, c.TestID); err != nil {
			return rca.BrowserCandidateInspection{}, err
		}
		candidateRecord, err := store.GetRunArchiveContext(ctx, c.TestID)
		if errors.Is(err, sql.ErrNoRows) {
			return reject("candidate_archive_missing")
		}
		if err != nil {
			return rca.BrowserCandidateInspection{}, err
		}
		if candidateRecord == nil {
			return reject("candidate_archive_missing")
		}
		lease, err := acquireArchiveMaterialized(ctx, candidateRecord)
		if err != nil {
			return rca.BrowserCandidateInspection{}, err
		}
		defer lease.Release()
		candidateManifest, err := loadRunArchiveManifestFromRecord(candidateRecord)
		if err != nil {
			return rca.BrowserCandidateInspection{}, err
		}
		if !browserSelectionArchiveIdentity(row, candidateRecord, candidateManifest, s.store) {
			return reject("candidate_archive_identity_changed")
		}
		digest, err := rcaEvidenceDigest(candidateManifest)
		if err != nil {
			return rca.BrowserCandidateInspection{}, err
		}
		result := rca.BrowserCandidateInspection{Digest: digest, CompletedAt: candidateManifest.CompletedAt}
		if !candidateManifest.CompletedAt.Before(archiveRunStarted(manifest)) {
			result.Reasons = []string{"completion_not_before_start"}
			return result, nil
		}
		if !reflect.DeepEqual(browserSelectionPolicyPins(manifest), browserSelectionPolicyPins(candidateManifest)) {
			result.Reasons = []string{"browser_policy_changed"}
			return result, nil
		}
		baselineInput, err := browserSelectionDocumentInput(ctx, candidateRecord, candidateManifest)
		if err != nil {
			return result, err
		}
		key := comparisonWorkloadKey(&baselineComparisonRun{manifest: candidateManifest}, nil, "")
		qualification := rca.QualifyBaseline(currentKey, key)
		result.Baseline = &rca.DetectionBaseline{TestID: c.TestID, Qualification: qualification.Status, QualificationReasons: qualification.Reasons, Levels: baselineInput.Levels, Series: baselineInput.Series, DocumentsIncomplete: baselineInput.DocumentsIncomplete || baselineInput.DocumentsUnreadable, ScriptChangedGroups: rcaBrowserScriptChangedGroups(currentKey, key, manifest, candidateManifest, input.Series...)}
		// Finalization or replacement during inspection makes admission uncertain.
		latest, err := store.GetTestContext(ctx, c.TestID)
		if err != nil {
			return result, err
		}
		if latest == nil || !browserSelectionArchiveIdentity(latest, candidateRecord, candidateManifest, s.store) {
			return result, errRCAFinalizing
		}
		if err := s.browserSelectionArchiveStable(ctx, c.TestID); err != nil {
			return result, err
		}
		return result, nil
	})
}

func browserSelectionPolicyPins(manifest *RunArchiveManifest) []RunPolicyPin {
	var out []RunPolicyPin
	for _, pin := range manifest.Provenance.PolicyPins {
		if pin.Kind == storage.AnalysisDefinitionKindBrowserPolicy {
			out = append(out, pin)
		}
	}
	return out
}

func browserSelectionDocumentInput(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) (rca.DetectionInput, error) {
	summaries, err := readAllArchiveSummariesContext(ctx, record)
	if err != nil {
		return rca.DetectionInput{}, err
	}
	key := comparisonWorkloadKey(&baselineComparisonRun{manifest: manifest}, nil, "")
	input := rca.DetectionInput{Levels: archiveLoadLevels(summaries, archiveRunStarted(manifest), archiveArrivalModel(&key))}
	series, limits, err := rcaBrowserDocumentSeries(ctx, record, manifest, summaries, 64, 4096)
	if err != nil {
		return input, err
	}
	input.Series = series
	input.DocumentsIncomplete, input.DocumentsUnreadable = rcaDocumentEvidenceLoss(limits)
	return input, nil
}

func browserSelectionArchiveIdentity(row *storage.TestRecord, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, store any) bool {
	if row == nil || record == nil || manifest == nil || row.ID != record.TestID || row.ID != manifest.TestID || row.EndTime == nil || !isTerminalTestStatus(row.Status) || manifest.Status != row.Status || normalizeProjectID(manifest.Provenance.ProjectID) != normalizeProjectID(row.ProjectID) || manifest.Provenance.ApplicationID != row.ApplicationID || manifest.Provenance.ScenarioID != row.ScenarioID {
		return false
	}
	if initial := manifest.InitialProvenance; initial != nil && (normalizeProjectID(initial.ProjectID) != normalizeProjectID(row.ProjectID) || initial.ApplicationID != row.ApplicationID || initial.ScenarioID != row.ScenarioID) {
		return false
	}
	incarnation, err := archiveManifestRunIncarnation(manifest)
	if err != nil || incarnation == "" {
		return false
	}
	durable, err := storage.TestRunIncarnation(row.InitialProvenance)
	if err != nil || durable != incarnation {
		return false
	}
	return manifest.Provenance.ExecutionGeneration == row.ExecutionGeneration && archiveRunTimestampMatches(archiveManifestRequestedAt(manifest), row.StartTime, store) && archiveRunTimestampMatches(manifest.CompletedAt, *row.EndTime, store) && archiveRunTimestampMatches(record.CompletedAt, *row.EndTime, store)
}

// Historical rows without a receipt retain their previous interpretation.
func validateRCABrowserSelection(payload rcaAnalysisPayload) error {
	if payload.BrowserSelection == nil && payload.Inputs.BrowserSelection == "" {
		return nil
	}
	body, err := json.Marshal(payload.BrowserSelection)
	if err != nil || payload.BrowserSelection == nil || sha256BytesHex(body) != payload.Inputs.BrowserSelection {
		return errors.New("the frozen browser selection receipt could not be verified")
	}
	receipt := payload.BrowserSelection
	if receipt.SelectedTestID != payload.Request.BaselineTestID || receipt.SelectedDigest != payload.Inputs.Baseline || (receipt.Status == "selected") != (receipt.SelectedTestID != "") {
		return errors.New("the frozen browser selection does not match the baseline inputs")
	}
	return nil
}

// An outstanding failed job is still mutable: it can be retried. Unknown state
// must abort admission rather than making an older archive the latest match.
func (s *Server) browserSelectionArchiveStable(ctx context.Context, testID string) error {
	if s.runner == nil {
		return errors.New("browser archive publication state unavailable")
	}
	return browserSelectionArchivePublication(ctx, s.runner.archiveStore, testID)
}

func browserSelectionArchivePublication(ctx context.Context, archiveStore any, testID string) error {
	store, ok := archiveStore.(interface {
		GetArchiveFinalizationJobContext(context.Context, string) (*storage.ArchiveFinalizationJob, error)
		GetArchiveRefreshJobContext(context.Context, string) (*storage.ArchiveRefreshJob, error)
	})
	if !ok {
		return errors.New("browser archive publication state unavailable")
	}
	job, err := store.GetArchiveFinalizationJobContext(ctx, testID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if job != nil {
		return errRCAFinalizing
	}
	refresh, err := store.GetArchiveRefreshJobContext(ctx, testID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if refresh != nil {
		return errRCAFinalizing
	}
	return ctx.Err()
}
