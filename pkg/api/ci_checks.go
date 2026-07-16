package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const ciGitHubCheckName = "APEX Load / Performance Gate"

const (
	// GitHub's Checks output summary is limited to 65,535 characters. Keep the
	// payload within the stricter byte interpretation used by some GHES releases
	// while preserving valid UTF-8 and the core metrics at the beginning.
	ciGitHubCheckSummaryMaxBytes = 65_535
	ciGitHubCheckTruncatedSuffix = "\n\n_Additional execution details were truncated. Open the APEX Load report for the complete evidence._"
)

const (
	ciGitHubCheckCreationLease = 30 * time.Second
	ciGitHubCheckClaimPoll     = 100 * time.Millisecond
	ciGitHubCheckClaimRenew    = 5 * time.Second

	ciGitHubCheckTerminalBatch  = 100
	ciGitHubCheckIntentBatch    = 20
	ciGitHubCheckPhaseTimeout   = 15 * time.Second
	ciGitHubCheckAttemptTimeout = 5 * time.Second
)

var errCIGitHubCheckCreationClaimLost = errors.New("GitHub Check creation claim was lost")

type ciGitHubCheckCreationClaimGuard struct {
	ctx      context.Context
	cancel   context.CancelCauseFunc
	store    storage.GitHubEnterpriseStore
	testID   string
	claimant string
	lease    time.Duration
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newCIGitHubCheckCreationClaimGuard(parent context.Context, store storage.GitHubEnterpriseStore, testID, claimant string, lease, interval time.Duration) *ciGitHubCheckCreationClaimGuard {
	if interval <= 0 || interval >= lease {
		interval = lease / 3
	}
	if interval <= 0 {
		interval = time.Millisecond
	}
	ctx, cancel := context.WithCancelCause(parent)
	guard := &ciGitHubCheckCreationClaimGuard{
		ctx: ctx, cancel: cancel, store: store, testID: testID, claimant: claimant,
		lease: lease, interval: interval, stop: make(chan struct{}), done: make(chan struct{}),
	}
	go guard.heartbeat()
	return guard
}

func (g *ciGitHubCheckCreationClaimGuard) heartbeat() {
	defer close(g.done)
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-g.stop:
			return
		case <-ticker.C:
			if err := g.renew(); err != nil {
				g.cancel(err)
				return
			}
		}
	}
}

func (g *ciGitHubCheckCreationClaimGuard) renew() error {
	held, err := g.store.RenewGitHubCheckCreation(g.testID, g.claimant, g.lease)
	if err != nil {
		return fmt.Errorf("renew GitHub Check creation claim: %w", err)
	}
	if !held {
		return errCIGitHubCheckCreationClaimLost
	}
	return nil
}

func (g *ciGitHubCheckCreationClaimGuard) renewOrCancel() error {
	if err := g.renew(); err != nil {
		g.cancel(err)
		return err
	}
	return nil
}

func (g *ciGitHubCheckCreationClaimGuard) operationError(err error) error {
	if cause := context.Cause(g.ctx); cause != nil && !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return err
}

func (g *ciGitHubCheckCreationClaimGuard) stopHeartbeat() {
	g.stopOnce.Do(func() {
		close(g.stop)
		<-g.done
	})
}

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
	if s == nil || s.runner == nil || !s.isExecutionLeader() {
		return
	}
	leaderCtx, cancelLeader := s.runner.executionOperationContext(ctx)
	defer cancelLeader()
	if leaderCtx.Err() != nil {
		return
	}
	store, ok := s.githubEnterpriseStore()
	if !ok {
		return
	}
	// Complete already-terminal required Checks first. A backlog of slow or
	// poisoned create intents must never delay branch-protecting conclusions.
	s.reconcileTerminalCIGitHubChecks(leaderCtx, store)
	if leaderCtx.Err() != nil {
		return
	}
	s.reconcilePendingCIGitHubCheckIntents(leaderCtx, store)
}

func (s *Server) reconcileTerminalCIGitHubChecks(ctx context.Context, store storage.GitHubEnterpriseStore) {
	links, err := store.ListIncompleteGitHubCheckLinks(ciGitHubCheckTerminalBatch)
	if err != nil {
		slog.Warn("failed to list incomplete controller-owned GitHub Checks", "error", err)
		return
	}
	phaseCtx, cancelPhase := context.WithTimeout(ctx, ciGitHubCheckPhaseTimeout)
	defer cancelPhase()
	for _, link := range links {
		if phaseCtx.Err() != nil {
			return
		}
		if link == nil || strings.TrimSpace(link.TestID) == "" {
			continue
		}
		test, exists := s.runner.GetTest(link.TestID)
		if !exists || !ciRunTerminal(test.Status) {
			continue
		}
		attemptCtx, cancel := context.WithTimeout(phaseCtx, ciGitHubCheckAttemptTimeout)
		_, syncErr := s.buildCIRunResponse(attemptCtx, test)
		cancel()
		if syncErr != nil {
			slog.Warn("controller-owned GitHub Check reconciliation failed", "test_id", link.TestID, "error", syncErr)
			if phaseCtx.Err() == nil {
				touchCIGitHubCheckLink(store, link)
			}
		}
	}
}

func (s *Server) reconcilePendingCIGitHubCheckIntents(ctx context.Context, store storage.GitHubEnterpriseStore) {
	intents, err := store.ListPendingGitHubCheckIntents(ciGitHubCheckIntentBatch)
	if err != nil {
		slog.Warn("failed to list pending controller-owned GitHub Check intents", "error", err)
		return
	}
	phaseCtx, cancelPhase := context.WithTimeout(ctx, ciGitHubCheckPhaseTimeout)
	defer cancelPhase()
	for _, intent := range intents {
		if phaseCtx.Err() != nil {
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
			touchCIGitHubCheckIntent(store, intent)
			continue
		}
		if err := validateCIGitHubCheckIntent(intent, test, metadata); err != nil {
			slog.Warn("pending GitHub Check intent does not match its CI run", "test_id", intent.TestID, "error", err)
			touchCIGitHubCheckIntent(store, intent)
			continue
		}
		attemptCtx, cancel := context.WithTimeout(phaseCtx, ciGitHubCheckAttemptTimeout)
		client, clientErr := s.githubClientForCICheckIntent(attemptCtx, intent, metadata)
		if clientErr == nil {
			_, clientErr = s.ensureCIGitHubCheck(attemptCtx, client, test, metadata)
		}
		cancel()
		if clientErr != nil {
			slog.Warn("pending controller-owned GitHub Check reconciliation failed", "test_id", intent.TestID, "error", clientErr)
			if phaseCtx.Err() == nil {
				touchCIGitHubCheckIntent(store, intent)
			}
			continue
		}
		if err := s.runner.recoverQueuedCIRun(intent.TestID); err != nil && !errors.Is(err, errNotExecutionLeader) {
			slog.Warn("recovered GitHub Check but failed to start queued CI run", "test_id", intent.TestID, "error", err)
		} else if errors.Is(err, errNotExecutionLeader) {
			return
		}
		if ciRunTerminal(test.Status) {
			attemptCtx, cancel = context.WithTimeout(phaseCtx, ciGitHubCheckAttemptTimeout)
			_, syncErr := s.buildCIRunResponse(attemptCtx, test)
			cancel()
			if syncErr != nil {
				slog.Warn("recovered controller-owned GitHub Check completion failed", "test_id", intent.TestID, "error", syncErr)
			}
		}
	}
}

func touchCIGitHubCheckLink(store storage.GitHubEnterpriseStore, link *storage.GitHubCheckLink) {
	if store == nil || link == nil || strings.TrimSpace(link.ID) == "" {
		return
	}
	if err := store.TouchGitHubCheckLink(link.ID, time.Now().UTC()); err != nil {
		slog.Warn("failed to rotate a retried GitHub Check link", "test_id", link.TestID, "error", err)
	}
}

func touchCIGitHubCheckIntent(store storage.GitHubEnterpriseStore, intent *storage.GitHubCheckIntent) {
	if store == nil || intent == nil {
		return
	}
	if err := store.TouchGitHubCheckIntent(intent.TestID, time.Now().UTC()); err != nil {
		slog.Warn("failed to rotate a retried GitHub Check intent", "test_id", intent.TestID, "error", err)
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

func (s *Server) githubClientForCICheckIntent(ctx context.Context, intent *storage.GitHubCheckIntent, metadata ciRunMetadata) (*githubClient, error) {
	if intent == nil {
		return nil, fmt.Errorf("GitHub Check intent is required")
	}
	settings, err := s.githubSettingsForCICheckSource(metadata.Source)
	if err != nil {
		return nil, err
	}
	if intent.InstallationID <= 0 {
		return nil, fmt.Errorf("controller GitHub App installation is unavailable")
	}
	return s.githubInstallationClient(ctx, settings, intent.InstallationID, intent.Owner, intent.Repo)
}

func (s *Server) githubSettingsForCICheckSource(source ciRunSource) (GitHubSettings, error) {
	settings := s.currentGitHubSettings()
	if !sameURLOrigin(settings.WebBaseURL, source.ServerURL) || !trustedGitHubAPIURL(source.ServerURL, settings.APIBaseURL) {
		return GitHubSettings{}, fmt.Errorf("controller GitHub App server does not match the accepted CI source server")
	}
	if !settings.Enabled || !settings.appConfigured() {
		return GitHubSettings{}, fmt.Errorf("controller GitHub App is unavailable")
	}
	return settings, nil
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
	if !sameURLOrigin(settings.WebBaseURL, source.ServerURL) || !trustedGitHubAPIURL(source.ServerURL, settings.APIBaseURL) {
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
	return s.ensureCIGitHubCheckWithClaimTiming(ctx, client, test, metadata, ciGitHubCheckCreationLease, ciGitHubCheckClaimRenew)
}

func (s *Server) ensureCIGitHubCheckWithClaimTiming(ctx context.Context, client *githubClient, test *ManagedTest, metadata ciRunMetadata, claimLease, claimRenewInterval time.Duration) (*storage.GitHubCheckLink, error) {
	if client == nil || test == nil {
		return nil, fmt.Errorf("GitHub Check client and CI run are required")
	}
	if claimLease <= 0 || claimRenewInterval <= 0 || claimRenewInterval >= claimLease {
		return nil, fmt.Errorf("GitHub Check creation claim timing is invalid")
	}
	owner, repo, ok := splitOwnerRepo(metadata.Source.Repository)
	if !ok {
		return nil, fmt.Errorf("invalid GitHub repository")
	}
	store, ok := s.githubEnterpriseStore()
	if !ok {
		return nil, fmt.Errorf("GitHub Check link storage is unavailable")
	}
	if link, err := storedCIGitHubCheckForRun(store, test, metadata); err != nil {
		return nil, err
	} else if link != nil {
		s.deleteCIGitHubCheckIntent(test.ID)
		return link, nil
	}
	claimant := uuid.NewString()
	for {
		claimed, err := store.ClaimGitHubCheckCreation(test.ID, claimant, claimLease)
		if err != nil {
			if link, linkErr := storedCIGitHubCheckForRun(store, test, metadata); linkErr != nil {
				return nil, linkErr
			} else if link != nil {
				s.deleteCIGitHubCheckIntent(test.ID)
				return link, nil
			}
			return nil, fmt.Errorf("claim GitHub Check creation: %w", err)
		}
		if claimed {
			break
		}
		if link, err := storedCIGitHubCheckForRun(store, test, metadata); err != nil {
			return nil, err
		} else if link != nil {
			s.deleteCIGitHubCheckIntent(test.ID)
			return link, nil
		}
		timer := time.NewTimer(ciGitHubCheckClaimPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("wait for GitHub Check creation claim: %w", ctx.Err())
		case <-timer.C:
		}
	}
	claimGuard := newCIGitHubCheckCreationClaimGuard(ctx, store, test.ID, claimant, claimLease, claimRenewInterval)
	defer func() {
		claimGuard.stopHeartbeat()
		if err := store.ReleaseGitHubCheckCreation(test.ID, claimant); err != nil {
			slog.Warn("failed to release GitHub Check creation claim", "test_id", test.ID, "error", err)
		}
	}()
	// The durable claim may have been held while another request completed and
	// released it. Re-check local storage before touching GitHub.
	if link, err := storedCIGitHubCheckForRun(store, test, metadata); err != nil {
		return nil, err
	} else if link != nil {
		s.deleteCIGitHubCheckIntent(test.ID)
		return link, nil
	}
	check, err := client.FindCheckRunByExternalID(claimGuard.ctx, owner, repo, metadata.Source.SHA, ciGitHubCheckName, test.ID)
	if err != nil {
		return nil, fmt.Errorf("recover GitHub Check by external id: %w", claimGuard.operationError(err))
	}
	if err := claimGuard.renewOrCancel(); err != nil {
		return nil, err
	}
	summary := ciGitHubCheckSummary(test, ciRunResponse{Status: test.Status, Outcome: "queued", ReportURL: ciPublicURL("/tests/" + test.ID)}, metadata)
	if check == nil {
		check, err = client.CreateCheckRunWithExternalID(claimGuard.ctx, owner, repo, ciGitHubCheckName, metadata.Source.SHA, "in_progress", "", summary, ciPublicURL("/tests/"+test.ID), test.ID)
		if err != nil {
			return nil, claimGuard.operationError(err)
		}
	}
	if err := claimGuard.renewOrCancel(); err != nil {
		return nil, err
	}
	if err := validateAcceptedCIGitHubCheck(check, test, metadata); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	// CI execution scripts are ephemeral snapshots removed from the script
	// library as soon as the run has been materialized. Anchor the authoritative
	// Check lifecycle only to the durable test ID; otherwise script cleanup would
	// cascade-delete the Check link before the terminal update.
	link := &storage.GitHubCheckLink{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:"+test.ID)).String(), Kind: "ci", TestID: test.ID, Owner: owner, Repo: repo, CommitSHA: metadata.Source.SHA, CheckRunID: check.ID, CheckRunURL: check.HTMLURL, Status: firstNonEmpty(check.Status, "in_progress"), Conclusion: check.Conclusion, PublishedAt: now, UpdatedAt: now}
	saved, err := store.SaveGitHubCheckLinkIfCreationClaimHeld(link, claimant)
	if err != nil {
		return nil, err
	}
	if !saved {
		return nil, errCIGitHubCheckCreationClaimLost
	}
	claimGuard.stopHeartbeat()
	s.deleteCIGitHubCheckIntent(test.ID)
	return link, nil
}

func validateAcceptedCIGitHubCheck(check *githubCheckRun, test *ManagedTest, metadata ciRunMetadata) error {
	if check == nil || test == nil {
		return fmt.Errorf("GitHub Check response and CI run are required")
	}
	if check.ID <= 0 || strings.TrimSpace(check.HTMLURL) == "" {
		return fmt.Errorf("GitHub returned an incomplete Check identity")
	}
	status := strings.ToLower(strings.TrimSpace(check.Status))
	if status != "queued" && status != "in_progress" {
		// Recovery is only for the crash window around initial creation. A
		// completed Check with the same external ID can belong to a previously
		// deleted/restored run and must never be attached to new execution.
		return fmt.Errorf("GitHub returned a non-startable Check status %q for this CI run", status)
	}
	if strings.TrimSpace(check.Conclusion) != "" {
		return fmt.Errorf("GitHub returned a terminal Check conclusion for a nonterminal CI run")
	}
	if strings.TrimSpace(check.ExternalID) != test.ID {
		return fmt.Errorf("GitHub Check external id does not match the CI run")
	}
	if !strings.EqualFold(strings.TrimSpace(check.HeadSHA), strings.TrimSpace(metadata.Source.SHA)) {
		return fmt.Errorf("GitHub Check commit does not match the CI run")
	}
	return nil
}

func storedCIGitHubCheck(store storage.GitHubEnterpriseStore, testID string) (*storage.GitHubCheckLink, error) {
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return nil, nil
	}
	linkID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:"+testID)).String()
	link, err := store.GetGitHubCheckLink(linkID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, nil
	}
	if !strings.EqualFold(strings.TrimSpace(link.Kind), "ci") || link.TestID != testID || link.CheckRunID <= 0 {
		return nil, fmt.Errorf("reserved CI GitHub Check link %q is invalid", linkID)
	}
	return link, nil
}

func storedCIGitHubCheckForRun(store storage.GitHubEnterpriseStore, test *ManagedTest, metadata ciRunMetadata) (*storage.GitHubCheckLink, error) {
	if test == nil {
		return nil, fmt.Errorf("CI run is required")
	}
	link, err := storedCIGitHubCheck(store, test.ID)
	if err != nil || link == nil {
		return link, err
	}
	owner, repo, ok := splitOwnerRepo(metadata.Source.Repository)
	if !ok || !strings.EqualFold(strings.TrimSpace(link.Owner), owner) ||
		!strings.EqualFold(strings.TrimSpace(link.Repo), repo) ||
		!strings.EqualFold(strings.TrimSpace(link.CommitSHA), strings.TrimSpace(metadata.Source.SHA)) {
		return nil, fmt.Errorf("stored GitHub Check is bound to different CI source metadata")
	}
	if strings.TrimSpace(link.CheckRunURL) == "" {
		return nil, fmt.Errorf("stored GitHub Check URL is missing")
	}
	return link, nil
}

func (s *Server) syncCIGitHubCheck(ctx context.Context, test *ManagedTest, metadata ciRunMetadata, response ciRunResponse) (string, error) {
	if !metadata.PublishCheck || test == nil {
		return "", nil
	}
	// The controller, not an intermediate caller snapshot, owns the Check
	// identity and cumulative SLA verdict. This also keeps the standalone sync
	// path equivalent to buildCIRunResponse.
	response.TestID = test.ID
	response.Status = strings.ToUpper(strings.TrimSpace(test.Status))
	response.SLAEvaluated = response.SLAEvaluated || test.ciSLAEvaluated
	response.SLABreached = response.SLABreached || test.ciSLABreached
	response.SLADetails = compactNonEmptyStrings(append(response.SLADetails, test.ciSLADetails...)...)
	if test.LiveMetrics != nil {
		response.Errors = test.LiveMetrics.TotalErrors
		response.TPS = test.LiveMetrics.TPS
		response.SLAEvaluated = response.SLAEvaluated || ciSLAEvidenceComplete(test.GoalConfig, test.LiveMetrics)
		breached, details := ciLiveSLABreach(test.LiveMetrics.GoalStatus)
		response.SLABreached = response.SLABreached || breached
		response.SLADetails = compactNonEmptyStrings(append(response.SLADetails, details...)...)
	}
	if ciRunTerminal(response.Status) {
		if !response.terminalEvidenceChecked {
			evidence, evidenceErr := s.validatedCIRunArchiveEvidence(ctx, test)
			applyCITerminalEvidence(&response, evidence, evidenceErr)
		}
		response.ResultsPresent = response.Transactions > 0
		if strings.EqualFold(response.Status, "COMPLETE") && !response.terminalEvidenceVerified {
			response.ReportURL = ""
			response.Error = ciTerminalEvidenceFailureReason
			response.Outcome = "execution_error"
		} else {
			response.Outcome = ciRunOutcome(response.Status, response.SLABreached, response.SLAEvaluated, response.ResultsPresent, metadata, response.Error)
		}
	}
	store, ok := s.githubEnterpriseStore()
	if !ok {
		return "", fmt.Errorf("GitHub Check link storage is unavailable")
	}
	link, err := storedCIGitHubCheckForRun(store, test, metadata)
	if err != nil {
		return "", err
	}
	if link == nil {
		return "", fmt.Errorf("GitHub Check link was not found")
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
	settings, err := s.githubSettingsForCICheckSource(metadata.Source)
	if err != nil {
		return link.CheckRunURL, err
	}
	if metadata.CheckInstallationID <= 0 {
		return link.CheckRunURL, fmt.Errorf("controller GitHub App installation is unavailable")
	}
	client, err := s.githubInstallationClient(ctx, settings, metadata.CheckInstallationID, link.Owner, link.Repo)
	if err != nil {
		return link.CheckRunURL, err
	}
	updated, err := client.UpdateCheckRun(ctx, link.Owner, link.Repo, link.CheckRunID, ciGitHubCheckName, "completed", desiredConclusion, ciGitHubCheckSummary(test, response, metadata), response.ReportURL)
	if err != nil {
		return link.CheckRunURL, err
	}
	if err := validateCompletedCIGitHubCheck(updated, link, test, metadata, desiredConclusion); err != nil {
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

func validateCompletedCIGitHubCheck(check *githubCheckRun, link *storage.GitHubCheckLink, test *ManagedTest, metadata ciRunMetadata, conclusion string) error {
	if check == nil || link == nil || test == nil {
		return fmt.Errorf("completed GitHub Check response, link, and CI run are required")
	}
	if check.ID != link.CheckRunID || check.ID <= 0 ||
		!strings.EqualFold(strings.TrimSpace(check.Status), "completed") ||
		!strings.EqualFold(strings.TrimSpace(check.Conclusion), strings.TrimSpace(conclusion)) ||
		strings.TrimSpace(check.ExternalID) != test.ID ||
		!strings.EqualFold(strings.TrimSpace(check.HeadSHA), strings.TrimSpace(metadata.Source.SHA)) ||
		strings.TrimSpace(check.HTMLURL) == "" {
		return fmt.Errorf("GitHub returned an incomplete or mismatched terminal Check response")
	}
	return nil
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
		"Workflow: `" + firstNonEmpty(metadata.Source.JobWorkflowRef, metadata.Source.WorkflowRef) + "`  ",
		"GitHub run: `" + metadata.Source.RunID + "/" + metadata.Source.RunAttempt + "`",
	}
	if metadata.Source.JobWorkflowRef != "" && metadata.Source.WorkflowRef != "" && metadata.Source.JobWorkflowRef != metadata.Source.WorkflowRef {
		lines = append(lines, "Caller workflow: `"+metadata.Source.WorkflowRef+"`")
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
	responseError := strings.TrimSpace(response.Error)
	if responseError == "" && test != nil {
		responseError = strings.TrimSpace(test.Error)
	}
	responseError = sanitizeCIExternalText(responseError, ciExternalErrorMaxBytes)
	if responseError != "" {
		lines = append(lines, "", "Execution error: "+responseError)
	}
	if details := sanitizeCIExternalDetails(response.SLADetails); len(details) > 0 {
		lines = append(lines, "", "SLA failures:")
		for _, detail := range details {
			lines = append(lines, "- "+detail)
		}
	}
	return truncateCIUTF8WithSuffix(strings.Join(lines, "\n"), ciGitHubCheckSummaryMaxBytes, ciGitHubCheckTruncatedSuffix)
}

func truncateCIUTF8WithSuffix(value string, maxBytes int, suffix string) string {
	if maxBytes <= 0 {
		return ""
	}
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= maxBytes {
		return value
	}
	suffix = strings.ToValidUTF8(suffix, "\uFFFD")
	if len(suffix) >= maxBytes {
		return truncateCIUTF8Prefix(suffix, maxBytes)
	}
	prefixBytes := maxBytes - len(suffix)
	return truncateCIUTF8Prefix(value, prefixBytes) + suffix
}

func truncateCIUTF8Prefix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
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
		if transaction == nil || transaction.ServerTelemetry == nil || transaction.ServerTelemetry.Samples <= 0 {
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
