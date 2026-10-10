package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Notification events root-cause analysis adds.
const (
	// notificationEventRCAAnalysisCompleted announces a completed analysis
	// with its outcome and leading hypotheses.
	notificationEventRCAAnalysisCompleted = "rca_analysis_completed"
	// notificationEventRunValidity announces a run whose load generators or
	// telemetry could not carry the load it reports: suspect or invalid.
	notificationEventRunValidity = "run_validity"
)

// rcaAutoQueueTimeout bounds queuing an automatic analysis: leasing the
// archive and freezing its evidence. The analysis itself runs later on the
// worker under its own budgets.
const rcaAutoQueueTimeout = 2 * time.Minute

// maxRCAEventHypotheses bounds the hypotheses an rca_analysis_completed event
// names.
const maxRCAEventHypotheses = 3

// autoQueueRCAAnalysis queues an automatic analysis when a run's archive is
// attached, for projects that analyze every archived run. It runs after the
// catalog commit, so every sidecar and the final manifest are durable. A
// refresh that left the evidence as it was (a new note, a recovered
// publication) finds the existing analysis of identical inputs and queues
// nothing; one that changed the evidence queues a new analysis. An archive
// still being refreshed is skipped: the refresh attaches it again.
func (s *Server) autoQueueRCAAnalysis(testID string) {
	if s == nil || s.runner == nil {
		return
	}
	if _, ok := s.store.(storage.RCAStore); !ok {
		return
	}
	test, ok := s.runner.GetTestSummary(testID)
	if !ok || test == nil {
		return
	}
	project := normalizeProjectID(test.ProjectID)
	if !projectAnalysisGates(s.store, project).RootCause.AutoAnalyze {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), rcaAutoQueueTimeout)
	defer cancel()
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, testID)
	if !ok || record == nil {
		return
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		slog.Warn("the run archive could not be leased to queue its root-cause analysis", "test_id", testID, "error", err)
		return
	}
	defer lease.Release()
	analysis, created, err := s.queueRCAAnalysis(ctx, project, testID, record, rcaAnalysisRequest{}, storage.RCAOriginAutomatic)
	switch {
	case errors.Is(err, errRCAFinalizing):
		// The refresh that holds the archive attaches it again when it ends.
		slog.Debug("an automatic root-cause analysis waits for the archive to settle", "test_id", testID)
		return
	case errors.Is(err, errRCADisabled):
		// Analyses were turned off since the gate above was read.
		slog.Debug("an automatic root-cause analysis was not queued: analyses are off for the project", "test_id", testID)
		return
	case err != nil:
		slog.Warn("an automatic root-cause analysis was not queued", "test_id", testID, "error", err)
		return
	}
	if created {
		slog.Info("queued an automatic root-cause analysis", "test_id", testID, "analysis_id", analysis.ID, "revision", analysis.Revision)
		s.wakeRCAWorker()
	}
}

// notificationWanted reports whether an enabled webhook subscribes to an
// event type, so an announcement that costs a report build is only made
// when someone receives it.
func notificationWanted(settings NotificationsSettings, eventType string) bool {
	for _, webhook := range settings.Webhooks {
		if webhook.Enabled && notificationWebhookMatchesEvent(webhook, eventType) {
			return true
		}
	}
	return false
}

// notifyRunValidity announces a run judged suspect or invalid, once per run
// and verdict: a later attach of the same archive finds the claim, while a
// refresh that changed the verdict announces the new one.
func (s *Server) notifyRunValidity(testID string) {
	if s == nil || s.runner == nil || s.runner.notifier == nil {
		return
	}
	settings := s.runner.currentNotificationSettings()
	if !notificationWanted(settings, notificationEventRunValidity) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), rcaAutoQueueTimeout)
	defer cancel()
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, testID)
	if !ok || record == nil {
		return
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		slog.Warn("the run archive could not be leased to announce its validity", "test_id", testID, "error", err)
		return
	}
	validity, err := cachedRunArchiveValidity(record)
	lease.Release()
	if err != nil || validity == nil || (validity.Verdict != rca.ValiditySuspect && validity.Verdict != rca.ValidityInvalid) {
		return
	}
	if claims, ok := s.store.(storage.RunNotificationClaimStore); ok {
		claimed, err := claims.ClaimRunNotification(testID, notificationEventRunValidity+":"+validity.Verdict, time.Now())
		if err != nil {
			slog.Warn("the run's validity announcement could not be claimed; it is not sent", "test_id", testID, "error", err)
			return
		}
		if !claimed {
			return
		}
	}
	test, _ := s.runner.GetTestSummary(testID)
	name, project := testID, ""
	if test != nil {
		name, project = firstNonEmpty(test.Name, testID), normalizeProjectID(test.ProjectID)
	}
	severity := "warning"
	if validity.Verdict == rca.ValidityInvalid {
		severity = "danger"
	}
	summary := "run validity " + validity.Verdict
	if len(validity.Reasons) > 0 {
		summary += ": " + validity.Reasons[0]
	}
	conditions := []string{"run:" + testID, "report:" + rcaReportLink(testID, "", "")}
	for _, reason := range validity.Reasons {
		if len(conditions) >= maxTelemetryAcceptanceEventConditions {
			break
		}
		conditions = append(conditions, "reason:"+reason)
	}
	s.runner.notifier.Dispatch(settings, NotificationEvent{Type: notificationEventRunValidity, TestID: testID, ProjectID: project, TestName: name,
		Status: validity.Verdict, Severity: severity, Summary: summary, Conditions: conditions, Timestamp: time.Now().UTC()})
}

// rcaReportLink is the dashboard route of a run's root cause, relative to
// the dashboard's origin: the Diagnostics section, and the analysis and
// hypothesis when they are known.
func rcaReportLink(testID, analysisID, hypothesisID string) string {
	link := "#/report/" + testID + "?section=diagnostics"
	if analysisID != "" {
		link += "&analysis=" + analysisID
	}
	if hypothesisID != "" {
		link += "&hypothesis=" + hypothesisID
	}
	return link
}

// notifyRCAAnalysisCompleted announces a completed analysis of a project that
// presents root cause, once per analysis.
func (s *Server) notifyRCAAnalysisCompleted(analysis *storage.RCAAnalysis, assessment *rca.Assessment) {
	if s == nil || s.runner == nil || s.runner.notifier == nil || analysis == nil || assessment == nil {
		return
	}
	settings := s.runner.currentNotificationSettings()
	if !notificationWanted(settings, notificationEventRCAAnalysisCompleted) || !rcaV2PresentEnabled(s.store, analysis.ProjectID) {
		return
	}
	if claims, ok := s.store.(storage.RunNotificationClaimStore); ok {
		claimed, err := claims.ClaimRunNotification(analysis.TestID, notificationEventRCAAnalysisCompleted+":"+analysis.ID, time.Now())
		if err != nil {
			slog.Warn("the root-cause announcement could not be claimed; it is not sent", "test_id", analysis.TestID, "analysis_id", analysis.ID, "error", err)
			return
		}
		if !claimed {
			return
		}
	}
	event := rcaAnalysisCompletedEvent(analysis, assessment)
	if test, ok := s.runner.GetTestSummary(analysis.TestID); ok && test != nil {
		event.TestName = firstNonEmpty(test.Name, analysis.TestID)
	}
	s.runner.notifier.Dispatch(settings, event)
}

// rcaAnalysisCompletedEvent is the rca_analysis_completed event for a
// completed analysis: its outcome as the status, a severity that follows the
// run's validity and whether a cause is supported, the same one-line summary
// the reports state, and the leading hypotheses with the report's route.
func rcaAnalysisCompletedEvent(analysis *storage.RCAAnalysis, assessment *rca.Assessment) NotificationEvent {
	severity := "info"
	switch {
	case assessment.Validity.Verdict == rca.ValidityInvalid:
		severity = "danger"
	case assessment.Validity.Verdict == rca.ValiditySuspect, assessment.Outcome == rca.OutcomeSupported:
		severity = "warning"
	}
	top := ""
	if len(assessment.Hypotheses) > 0 {
		top = assessment.Hypotheses[0].ID
	}
	conditions := []string{"run:" + analysis.TestID, fmt.Sprintf("analysis:%s@%d", analysis.ID, analysis.Revision), "origin:" + firstNonEmpty(analysis.Origin, storage.RCAOriginRequested),
		"validity:" + firstNonEmpty(assessment.Validity.Verdict, rca.ValidityUnknown), "report:" + rcaReportLink(analysis.TestID, analysis.ID, top)}
	for i, hypothesis := range assessment.Hypotheses {
		if i >= maxRCAEventHypotheses {
			break
		}
		conditions = append(conditions, fmt.Sprintf("hypothesis:#%d %s (%s confidence)", hypothesis.Rank, rcaSignatureTitle(hypothesis.Signature), hypothesis.Confidence))
	}
	stamp := time.Now().UTC()
	if analysis.CompletedAt != nil {
		stamp = analysis.CompletedAt.UTC()
	}
	return NotificationEvent{Type: notificationEventRCAAnalysisCompleted, TestID: analysis.TestID, ProjectID: analysis.ProjectID, TestName: analysis.TestID,
		Status: assessment.Outcome, Severity: severity, Summary: strings.TrimSpace(reportRootCauseFromV2(assessment, analysis.CompletedAt).Summary), Conditions: conditions, Timestamp: stamp}
}
