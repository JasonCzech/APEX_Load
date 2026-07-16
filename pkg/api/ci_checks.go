package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const ciGitHubCheckName = "APEX Load / Performance Gate"

func (s *Server) handleCIRunTerminal(testID string) {
	s.launchBackground(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var lastErr error
		for attempt := 0; attempt < 4; attempt++ {
			test, ok := s.runner.GetTest(testID)
			if !ok {
				return
			}
			metadata, err := decodeCIRunMetadata(test.MaterializationKey)
			if err != nil || !metadata.PublishCheck {
				return
			}
			if _, err = s.buildCIRunResponse(ctx, test); err == nil {
				return
			}
			lastErr = err
			timer := time.NewTimer(time.Duration(1<<attempt) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				attempt = 4
			case <-timer.C:
			}
		}
		if lastErr != nil {
			slog.Warn("failed to complete controller-owned GitHub Check after retries", "test_id", testID, "error", lastErr)
		}
	})
}

// startCIGitHubCheckReconciliation closes the disconnect/restart gaps in the
// controller-owned Check lifecycle. Durable pre-create intents and incomplete
// links are retried by the active execution leader.
func (s *Server) startCIGitHubCheckReconciliation() {
	if s == nil || s.runner == nil || s.ciChecksCancel != nil {
		return
	}
	if _, ok := s.githubEnterpriseStore(); !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.ciChecksCancel = cancel
	s.launchBackground(func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if s.isExecutionLeader() {
				s.reconcileIncompleteCIGitHubChecks(ctx)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

func (s *Server) reconcileIncompleteCIGitHubChecks(ctx context.Context) {
	store, ok := s.githubEnterpriseStore()
	if !ok {
		return
	}
	intents, err := store.ListPendingGitHubCheckIntents(200)
	if err != nil {
		slog.Warn("failed to list pending controller-owned GitHub Check intents", "error", err)
		return
	}
	for _, intent := range intents {
		if ctx.Err() != nil {
			return
		}
		if intent == nil || strings.TrimSpace(intent.TestID) == "" {
			continue
		}
		test, exists := s.runner.GetTest(intent.TestID)
		if !exists {
			// The intent is deliberately written before the test. A retry may
			// still materialize it, so an absent test is not an error.
			continue
		}
		metadata, metadataErr := decodeCIRunMetadata(test.MaterializationKey)
		if metadataErr != nil || !metadata.PublishCheck {
			slog.Warn("pending GitHub Check intent has invalid CI metadata", "test_id", intent.TestID, "error", metadataErr)
			continue
		}
		if err := validateCIGitHubCheckIntent(intent, test, metadata); err != nil {
			slog.Warn("pending GitHub Check intent does not match its CI run", "test_id", intent.TestID, "error", err)
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		client, clientErr := s.githubClientForCICheckIntent(attemptCtx, intent)
		if clientErr == nil {
			_, clientErr = s.ensureCIGitHubCheck(attemptCtx, client, test, metadata)
		}
		cancel()
		if clientErr != nil {
			slog.Warn("pending controller-owned GitHub Check reconciliation failed", "test_id", intent.TestID, "error", clientErr)
			continue
		}
		if ciRunTerminal(test.Status) {
			attemptCtx, cancel = context.WithTimeout(ctx, 15*time.Second)
			_, syncErr := s.buildCIRunResponse(attemptCtx, test)
			cancel()
			if syncErr != nil {
				slog.Warn("recovered controller-owned GitHub Check completion failed", "test_id", intent.TestID, "error", syncErr)
			}
		}
	}
	links, err := store.ListIncompleteGitHubCheckLinks(200)
	if err != nil {
		slog.Warn("failed to list incomplete controller-owned GitHub Checks", "error", err)
		return
	}
	for _, link := range links {
		if ctx.Err() != nil {
			return
		}
		if link == nil || strings.TrimSpace(link.TestID) == "" {
			continue
		}
		test, exists := s.runner.GetTest(link.TestID)
		if !exists || !ciRunTerminal(test.Status) {
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, syncErr := s.buildCIRunResponse(attemptCtx, test)
		cancel()
		if syncErr != nil {
			slog.Warn("controller-owned GitHub Check reconciliation failed", "test_id", link.TestID, "error", syncErr)
		}
	}
}

func (s *Server) saveCIGitHubCheckIntent(testID, projectID string, metadata ciRunMetadata) error {
	store, ok := s.githubEnterpriseStore()
	if !ok {
		return fmt.Errorf("GitHub Check intent storage is unavailable")
	}
	owner, repo, ok := splitOwnerRepo(metadata.Source.Repository)
	if !ok {
		return fmt.Errorf("invalid GitHub repository")
	}
	return store.SaveGitHubCheckIntent(&storage.GitHubCheckIntent{
		TestID: testID, ProjectID: projectID, InstallationID: metadata.CheckInstallationID,
		Owner: owner, Repo: repo, CommitSHA: metadata.Source.SHA,
	})
}

func (s *Server) deleteCIGitHubCheckIntent(testID string) {
	if store, ok := s.githubEnterpriseStore(); ok {
		if err := store.DeleteGitHubCheckIntent(testID); err != nil {
			slog.Warn("failed to remove completed GitHub Check intent", "test_id", testID, "error", err)
		}
	}
}

func validateCIGitHubCheckIntent(intent *storage.GitHubCheckIntent, test *ManagedTest, metadata ciRunMetadata) error {
	if intent == nil || test == nil {
		return fmt.Errorf("GitHub Check intent and CI run are required")
	}
	owner, repo, ok := splitOwnerRepo(metadata.Source.Repository)
	if !ok || intent.TestID != test.ID || normalizeProjectID(intent.ProjectID) != normalizeProjectID(test.ProjectID) ||
		intent.InstallationID != metadata.CheckInstallationID || !strings.EqualFold(intent.Owner, owner) ||
		!strings.EqualFold(intent.Repo, repo) || !strings.EqualFold(intent.CommitSHA, metadata.Source.SHA) {
		return fmt.Errorf("GitHub Check intent is bound to different CI metadata")
	}
	return nil
}

func (s *Server) githubClientForCICheckIntent(ctx context.Context, intent *storage.GitHubCheckIntent) (*githubClient, error) {
	if intent == nil {
		return nil, fmt.Errorf("GitHub Check intent is required")
	}
	settings := s.currentGitHubSettings()
	if !settings.Enabled || !settings.appConfigured() || intent.InstallationID <= 0 {
		return nil, fmt.Errorf("controller GitHub App is unavailable")
	}
	return s.githubInstallationClient(ctx, settings, intent.InstallationID, intent.Owner, intent.Repo)
}

func (s *Server) validateCICheckPublishing(ctx context.Context, policy storage.GitHubProjectPolicy, source ciRunSource) (*githubClient, error) {
	policy = normalizeGitHubProjectPolicy(policy)
	if !policy.Actions.ChecksEnabled {
		return nil, fmt.Errorf("publish_check is true but project GitHub Actions Check publication is disabled")
	}
	settings := s.currentGitHubSettings()
	if !settings.Enabled || !settings.ChecksEnabled {
		return nil, fmt.Errorf("GitHub Check publication is disabled in controller settings")
	}
	if !settings.appConfigured() {
		return nil, fmt.Errorf("GitHub Check publication requires a configured controller GitHub App")
	}
	if _, err := ciPublicBaseURL(); err != nil {
		return nil, fmt.Errorf("GitHub Check publication requires APEXLOAD_PUBLIC_URL: %w", err)
	}
	if policy.AppInstallationID <= 0 {
		return nil, fmt.Errorf("GitHub Check publication requires a project App installation id")
	}
	if !sameURLOrigin(settings.WebBaseURL, source.ServerURL) {
		return nil, fmt.Errorf("controller GitHub settings do not match the trusted GitHub server")
	}
	owner, repo, ok := splitOwnerRepo(source.Repository)
	if !ok {
		return nil, fmt.Errorf("source.repository must be owner/repo")
	}
	if store, ok := s.githubEnterpriseStore(); ok {
		if installation, err := store.GetGitHubAppInstallation(policy.AppInstallationID); err == nil && installation != nil && strings.TrimSpace(installation.PermissionsJSON) != "" {
			var permissions map[string]string
			if json.Unmarshal([]byte(installation.PermissionsJSON), &permissions) != nil || !strings.EqualFold(strings.TrimSpace(permissions["checks"]), "write") {
				return nil, fmt.Errorf("GitHub App installation does not grant Checks: write")
			}
		}
	}
	client, err := s.githubInstallationClient(ctx, settings, policy.AppInstallationID, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("validate GitHub App installation: %w", err)
	}
	run, err := client.GetWorkflowRun(ctx, owner, repo, source.RunID)
	if err != nil {
		return nil, fmt.Errorf("validate GitHub workflow run: %w", err)
	}
	if fmt.Sprintf("%d", run.Repository.ID) != strings.TrimSpace(source.RepositoryID) || !strings.EqualFold(strings.TrimSpace(run.Repository.FullName), strings.TrimSpace(source.Repository)) {
		return nil, fmt.Errorf("GitHub workflow run repository does not match CI source metadata")
	}
	expectedRunSHA := strings.TrimSpace(source.SHA)
	if strings.EqualFold(strings.TrimSpace(source.EventName), "pull_request") && strings.TrimSpace(source.HeadSHA) != "" {
		expectedRunSHA = strings.TrimSpace(source.HeadSHA)
	}
	if strings.TrimSpace(run.HeadSHA) != expectedRunSHA {
		return nil, fmt.Errorf("GitHub workflow run commit does not match CI source metadata")
	}
	if !strings.EqualFold(strings.TrimSpace(run.Event), strings.TrimSpace(source.EventName)) {
		return nil, fmt.Errorf("GitHub workflow run event does not match CI source metadata")
	}
	return client, nil
}

func ciPublicBaseURL() (string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("APEXLOAD_PUBLIC_URL")), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	return base, nil
}

func (s *Server) createCIGitHubCheck(ctx context.Context, client *githubClient, policy storage.GitHubProjectPolicy, test *ManagedTest, metadata ciRunMetadata) (*storage.GitHubCheckLink, error) {
	if client == nil || test == nil {
		return nil, fmt.Errorf("GitHub Check client and CI run are required")
	}
	if policy.AppInstallationID <= 0 || policy.AppInstallationID != metadata.CheckInstallationID {
		return nil, fmt.Errorf("GitHub Check installation does not match the accepted CI run")
	}
	return s.ensureCIGitHubCheck(ctx, client, test, metadata)
}

func (s *Server) ensureCIGitHubCheck(ctx context.Context, client *githubClient, test *ManagedTest, metadata ciRunMetadata) (*storage.GitHubCheckLink, error) {
	if client == nil || test == nil {
		return nil, fmt.Errorf("GitHub Check client and CI run are required")
	}
	owner, repo, ok := splitOwnerRepo(metadata.Source.Repository)
	if !ok {
		return nil, fmt.Errorf("invalid GitHub repository")
	}
	store, ok := s.githubEnterpriseStore()
	if !ok {
		return nil, fmt.Errorf("GitHub Check link storage is unavailable")
	}
	links, err := store.ListGitHubCheckLinks(test.ID, "", 10)
	if err != nil {
		return nil, err
	}
	if len(links) > 0 && links[0] != nil && links[0].CheckRunID > 0 {
		s.deleteCIGitHubCheckIntent(test.ID)
		return links[0], nil
	}
	check, err := client.FindCheckRunByExternalID(ctx, owner, repo, metadata.Source.SHA, ciGitHubCheckName, test.ID)
	if err != nil {
		return nil, fmt.Errorf("recover GitHub Check by external id: %w", err)
	}
	summary := ciGitHubCheckSummary(test, ciRunResponse{Status: test.Status, Outcome: "queued", ReportURL: ciPublicURL("/tests/" + test.ID)}, metadata)
	if check == nil {
		check, err = client.CreateCheckRunWithExternalID(ctx, owner, repo, ciGitHubCheckName, metadata.Source.SHA, "in_progress", "", summary, ciPublicURL("/tests/"+test.ID), test.ID)
		if err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	// CI execution scripts are ephemeral snapshots removed from the script
	// library as soon as the run has been materialized. Anchor the authoritative
	// Check lifecycle only to the durable test ID; otherwise script cleanup would
	// cascade-delete the Check link before the terminal update.
	link := &storage.GitHubCheckLink{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:"+test.ID)).String(), TestID: test.ID, Owner: owner, Repo: repo, CommitSHA: metadata.Source.SHA, CheckRunID: check.ID, CheckRunURL: check.HTMLURL, Status: firstNonEmpty(check.Status, "in_progress"), Conclusion: check.Conclusion, PublishedAt: now, UpdatedAt: now}
	if err := store.SaveGitHubCheckLink(link); err != nil {
		return nil, err
	}
	s.deleteCIGitHubCheckIntent(test.ID)
	return link, nil
}

func (s *Server) syncCIGitHubCheck(ctx context.Context, test *ManagedTest, metadata ciRunMetadata, response ciRunResponse) (string, error) {
	if !metadata.PublishCheck || test == nil {
		return "", nil
	}
	store, ok := s.githubEnterpriseStore()
	if !ok {
		return "", fmt.Errorf("GitHub Check link storage is unavailable")
	}
	links, err := store.ListGitHubCheckLinks(test.ID, "", 10)
	if err != nil {
		return "", err
	}
	if len(links) == 0 {
		return "", fmt.Errorf("GitHub Check link was not found")
	}
	link := links[0]
	if link == nil || link.CheckRunID <= 0 {
		return "", fmt.Errorf("GitHub Check link is invalid")
	}
	// A completed durable link is immutable. Stale polling snapshots and even a
	// conflicting terminal snapshot must never reopen or rewrite the Check.
	if strings.EqualFold(strings.TrimSpace(link.Status), "completed") && strings.TrimSpace(link.Conclusion) != "" {
		return link.CheckRunURL, nil
	}
	// The Check is created in progress. Avoiding subsequent nonterminal PATCHes
	// eliminates a race where an old RUNNING snapshot arrives after completion.
	if !ciRunTerminal(test.Status) {
		return link.CheckRunURL, nil
	}
	link, err = store.ClaimGitHubCheckConclusion(link.ID, ciGitHubCheckConclusion(response))
	if err != nil {
		return link.CheckRunURL, err
	}
	if strings.EqualFold(strings.TrimSpace(link.Status), "completed") && strings.TrimSpace(link.Conclusion) != "" {
		return link.CheckRunURL, nil
	}
	desiredConclusion := strings.TrimSpace(link.PendingConclusion)
	if desiredConclusion == "" {
		return link.CheckRunURL, fmt.Errorf("GitHub Check terminal conclusion was not durably claimed")
	}
	settings := s.currentGitHubSettings()
	if !settings.Enabled || !settings.appConfigured() || metadata.CheckInstallationID <= 0 {
		return link.CheckRunURL, fmt.Errorf("controller GitHub App is unavailable")
	}
	client, err := s.githubInstallationClient(ctx, settings, metadata.CheckInstallationID, link.Owner, link.Repo)
	if err != nil {
		return link.CheckRunURL, err
	}
	updated, err := client.UpdateCheckRun(ctx, link.Owner, link.Repo, link.CheckRunID, ciGitHubCheckName, "completed", desiredConclusion, ciGitHubCheckSummary(test, response, metadata), response.ReportURL)
	if err != nil {
		return link.CheckRunURL, err
	}
	link.Status = "completed"
	link.Conclusion = desiredConclusion
	link.PendingConclusion = ""
	link.CheckRunURL = firstNonEmpty(updated.HTMLURL, link.CheckRunURL)
	link.UpdatedAt = time.Now().UTC()
	if err := store.SaveGitHubCheckLink(link); err != nil {
		return link.CheckRunURL, err
	}
	return link.CheckRunURL, nil
}

func ciGitHubCheckConclusion(response ciRunResponse) string {
	switch response.Outcome {
	case "passed":
		return "success"
	case "cancelled":
		return "cancelled"
	default:
		return "failure"
	}
}

func ciGitHubCheckSummary(test *ManagedTest, response ciRunResponse, metadata ciRunMetadata) string {
	lines := []string{
		"APEX Load controller-backed performance gate.",
		"",
		"| Field | Value |",
		"|---|---:|",
		"| Status | `" + strings.ToUpper(strings.TrimSpace(response.Status)) + "` |",
		"| Outcome | `" + firstNonEmpty(response.Outcome, "running") + "` |",
		fmt.Sprintf("| Transactions | %d |", response.Transactions),
		fmt.Sprintf("| Errors | %d |", response.Errors),
		fmt.Sprintf("| TPS | %.2f |", response.TPS),
		fmt.Sprintf("| Duration | %d ms |", response.DurationMs),
		fmt.Sprintf("| SLA evaluated | %t |", response.SLAEvaluated),
		fmt.Sprintf("| SLA breached | %t |", response.SLABreached),
		"",
		"Source: `" + metadata.Source.Repository + "@" + metadata.Source.SHA + "`  ",
		"Workflow: `" + metadata.Source.WorkflowRef + "`  ",
		"GitHub run: `" + metadata.Source.RunID + "/" + metadata.Source.RunAttempt + "`",
	}
	if rows := ciGitHubCheckSLARows(test, response); len(rows) > 0 {
		lines = append(lines,
			"",
			"Configured SLA thresholds:",
			"",
			"| Metric | Threshold | Actual | Result |",
			"|---|---:|---:|:---|",
		)
		for _, row := range rows {
			lines = append(lines, fmt.Sprintf("| %s | %s | %s | `%s` |", row.metric, row.threshold, row.actual, row.result))
		}
	}
	if test != nil && strings.TrimSpace(test.Error) != "" {
		lines = append(lines, "", "Execution error: "+strings.TrimSpace(test.Error))
	}
	if len(response.SLADetails) > 0 {
		lines = append(lines, "", "SLA failures:")
		for _, detail := range response.SLADetails {
			lines = append(lines, "- "+detail)
		}
	}
	return strings.Join(lines, "\n")
}

type ciGitHubCheckSLARow struct {
	metric    string
	threshold string
	actual    string
	result    string
}

// ciGitHubCheckSLARows renders only configured CI SLA limits and keeps their
// order stable so retries and controller reconciliation produce the same Check
// summary. Server timing limits are evaluated against the worst transaction,
// matching the goal controller's any-transaction breach semantics.
func ciGitHubCheckSLARows(test *ManagedTest, response ciRunResponse) []ciGitHubCheckSLARow {
	if test == nil || test.GoalConfig == nil {
		return nil
	}
	config := test.GoalConfig
	live := test.LiveMetrics
	terminal := ciRunTerminal(response.Status)
	rows := make([]ciGitHubCheckSLARow, 0, 5)
	appendFloat := func(metric string, threshold *float64, unit string, actual float64, available bool) {
		if threshold == nil {
			return
		}
		actualText, result := ciGitHubCheckSLAResult(actual, available, *threshold, terminal, unit)
		rows = append(rows, ciGitHubCheckSLARow{
			metric:    metric,
			threshold: fmt.Sprintf("≤ %.2f%s", *threshold, unit),
			actual:    actualText,
			result:    result,
		})
	}

	if live != nil {
		appendFloat("P95 latency", config.P95LatencyMs, " ms", live.P95Ms, true)
		appendFloat("Error rate", config.MaxErrorPct, "%", live.ErrorPct, true)
	} else {
		appendFloat("P95 latency", config.P95LatencyMs, " ms", 0, false)
		appendFloat("Error rate", config.MaxErrorPct, "%", 0, false)
	}
	serverProcessing, hasServerProcessing := ciGitHubCheckMaxServerTelemetry(live, func(summary *ServerTelemetrySummary) float64 {
		return summary.AvgServerProcessingMs
	})
	appendFloat("Average server processing", config.MaxAvgServerProcessingMs, " ms", serverProcessing, hasServerProcessing)
	transfer, hasTransfer := ciGitHubCheckMaxServerTelemetry(live, func(summary *ServerTelemetrySummary) float64 {
		return summary.AvgTransferMs
	})
	appendFloat("Average transfer", config.MaxAvgTransferMs, " ms", transfer, hasTransfer)
	if config.MaxErrorCount != nil {
		actual, result := "Not available", ciGitHubCheckSLANoActualResult(terminal)
		if live != nil {
			actual = fmt.Sprintf("%d", live.TotalErrors)
			if live.TotalErrors > int64(*config.MaxErrorCount) {
				result = "FAIL"
			} else {
				result = "PASS"
			}
		}
		rows = append(rows, ciGitHubCheckSLARow{
			metric: "Error count", threshold: fmt.Sprintf("≤ %d", *config.MaxErrorCount), actual: actual, result: result,
		})
	}
	return rows
}

func ciGitHubCheckSLAResult(actual float64, available bool, threshold float64, terminal bool, unit string) (string, string) {
	if !available {
		return "Not available", ciGitHubCheckSLANoActualResult(terminal)
	}
	result := "PASS"
	if actual > threshold {
		result = "FAIL"
	}
	return fmt.Sprintf("%.2f%s", actual, unit), result
}

func ciGitHubCheckSLANoActualResult(terminal bool) string {
	if terminal {
		return "NOT EVALUATED"
	}
	return "PENDING"
}

func ciGitHubCheckMaxServerTelemetry(live *LiveMetrics, value func(*ServerTelemetrySummary) float64) (float64, bool) {
	if live == nil {
		return 0, false
	}
	var maximum float64
	found := false
	for _, transaction := range live.Transactions {
		if transaction == nil || transaction.ServerTelemetry == nil {
			continue
		}
		candidate := value(transaction.ServerTelemetry)
		if !found || candidate > maximum {
			maximum = candidate
			found = true
		}
	}
	return maximum, found
}
