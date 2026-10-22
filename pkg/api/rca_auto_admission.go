package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type rcaAutoAdmissionStore interface {
	AppendRCAEvidenceForAutoAdmission(context.Context, storage.RCAAutoAdmission, []byte, time.Time, func(*storage.RCAEvidenceRevision) bool) (*storage.RCAEvidenceRevision, error)
	CreateRCAAnalysisForAutoAdmission(context.Context, storage.RCAAutoAdmission, string, []byte, time.Time) (*storage.RCAAnalysis, bool, error)
	EnsureRCAAutoAdmission(context.Context, string) (*storage.RCAAutoAdmission, error)
	ListRCAAutoAdmissions(context.Context, string, int) ([]storage.RCAAutoAdmission, string, error)
	RCAAutoAdmissionCurrent(context.Context, storage.RCAAutoAdmission) (bool, error)
	FinishRCAAutoAdmission(context.Context, storage.RCAAutoAdmission) error
}

// The attachment hook persists admission before freezing inputs. This recovers
// baseline-finalization waits across restart; the pre-existing crash gap between
// archive commit and invocation of this hook is not an atomic outbox guarantee.
func (s *Server) autoQueueRCAAnalysis(testID string) {
	if s == nil || s.runner == nil {
		return
	}
	test, ok := s.runner.GetTestSummary(testID)
	if !ok || test == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), rcaAutoQueueTimeout)
	defer cancel()
	store, ok := s.store.(rcaAutoAdmissionStore)
	if !ok {
		s.autoQueueRCAAnalysisOnce(ctx, test.ID)
		return
	}
	intent, err := store.EnsureRCAAutoAdmission(ctx, test.ID)
	if err != nil {
		slog.Warn("automatic RCA admission intent could not be persisted", "test_id", test.ID)
		return
	}
	s.processRCAAutoAdmission(ctx, store, *intent)
}
func (s *Server) processRCAAutoAdmission(ctx context.Context, store rcaAutoAdmissionStore, in storage.RCAAutoAdmission) {
	current, err := store.RCAAutoAdmissionCurrent(ctx, in)
	if err != nil {
		return
	}
	if !current || s.autoQueueRCAAnalysisOnce(ctx, in.TestID, &in) {
		if err := store.FinishRCAAutoAdmission(ctx, in); err != nil && ctx.Err() == nil {
			slog.Warn("automatic RCA admission acknowledgement remains retryable", "test_id", in.TestID)
		}
	}
}
func (s *Server) reconcileRCAAutoAdmissions(ctx context.Context, after string) string {
	store, ok := s.store.(rcaAutoAdmissionStore)
	if !ok {
		return ""
	}
	intents, next, err := store.ListRCAAutoAdmissions(ctx, after, 4)
	if err != nil {
		return after
	}
	cursor := after
	for _, in := range intents {
		if ctx.Err() != nil {
			return cursor
		}
		work, cancel := context.WithTimeout(ctx, rcaAutoQueueTimeout)
		s.processRCAAutoAdmission(work, store, in)
		cancel()
		cursor = in.Key
	}
	return next
}
func (s *Server) startRCAAutoAdmissionWorker() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if _, ok := s.store.(rcaAutoAdmissionStore); !ok {
		return cancel
	}
	if !s.launchBackground(func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		cursor := ""
		for {
			if ctx.Err() != nil {
				return
			}
			if s.isExecutionLeader() {
				work, done := context.WithTimeout(ctx, 4*rcaAutoQueueTimeout)
				cursor = s.reconcileRCAAutoAdmissions(work, cursor)
				done()
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}) {
		cancel()
	}
	return cancel
}

// A failed project-settings read is not an operator decision to disable RCA.
func rcaAutoAnalysisGates(store any, project string) (AnalysisGates, error) {
	getter, ok := store.(projectGetter)
	if !ok {
		return analysisGatesFromEnv(project), nil
	}
	stored, err := getter.GetProject(project)
	if err != nil {
		return AnalysisGates{}, err
	}
	if stored == nil {
		return AnalysisGates{}, errors.New("RCA project settings unavailable")
	}
	return combineAnalysisGates(analysisGatesFromEnv(project), analysisGatesFromSettings(stored.Settings.Analysis)), nil
}
