package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

type githubSyncService struct {
	runner     *TestRunner
	store      storage.GitHubEnterpriseStore
	linkStore  storage.GitHubScriptLinkStore
	claimStore storage.GitHubScriptSyncClaimStore
}

const githubScriptSyncClaimTTL = 10 * time.Minute

type githubSyncSummaryItem struct {
	Error      string   `json:"error"`
	ScriptID   string   `json:"script_id,omitempty"`
	ScriptIDs  []string `json:"script_ids,omitempty"`
	Path       string   `json:"path,omitempty"`
	Paths      []string `json:"paths,omitempty"`
	PrevPath   string   `json:"prev_path,omitempty"`
	TargetPath string   `json:"target_path,omitempty"`
}

type githubSyncSummary struct {
	Errors             []githubSyncSummaryItem `json:"errors,omitempty"`
	Conflicts          []githubSyncSummaryItem `json:"conflicts,omitempty"`
	ErrorsTruncated    int                     `json:"errors_truncated,omitempty"`
	ConflictsTruncated int                     `json:"conflicts_truncated,omitempty"`
	Strategy           string                  `json:"strategy,omitempty"`
	Branch             string                  `json:"branch,omitempty"`
	PullURL            string                  `json:"pull_request_url,omitempty"`
	PullNumber         int                     `json:"pull_request_number,omitempty"`
}

const (
	// githubSyncSummaryMaxItems bounds each retained issue list. SummaryJSON is
	// persisted and returned to the dashboard, so a directory import with
	// hundreds of failing files must not write an unbounded blob; the overflow
	// is reported as a count instead. job.Errors/job.Conflicts remain exact.
	githubSyncSummaryMaxItems = 50
	// githubSyncSummaryMaxErrorBytes bounds a single retained error message.
	githubSyncSummaryMaxErrorBytes = 512
)

type githubSyncSummaryBuilder struct {
	summary githubSyncSummary
}

// sanitizeGitHubSyncSummaryItem scrubs credential-bearing text out of the
// upstream error strings and bounds their length. These messages originate from
// the GitHub API and the storage layer and are surfaced verbatim in the job
// payload, so they must not carry tokens or unbounded internal detail.
func sanitizeGitHubSyncSummaryItem(issue githubSyncSummaryItem) githubSyncSummaryItem {
	issue.Error = scrub.Value(issue.Error, scrub.Policy{})
	if len(issue.Error) > githubSyncSummaryMaxErrorBytes {
		issue.Error = issue.Error[:githubSyncSummaryMaxErrorBytes] + "… (truncated)"
	}
	return issue
}

func (b *githubSyncSummaryBuilder) addError(issue githubSyncSummaryItem) {
	if len(b.summary.Errors) >= githubSyncSummaryMaxItems {
		b.summary.ErrorsTruncated++
		return
	}
	b.summary.Errors = append(b.summary.Errors, sanitizeGitHubSyncSummaryItem(issue))
}

func (b *githubSyncSummaryBuilder) addConflict(issue githubSyncSummaryItem) {
	if len(b.summary.Conflicts) >= githubSyncSummaryMaxItems {
		b.summary.ConflictsTruncated++
		return
	}
	b.summary.Conflicts = append(b.summary.Conflicts, sanitizeGitHubSyncSummaryItem(issue))
}

func (b *githubSyncSummaryBuilder) setPullRequest(branch, pullURL string, pullNumber int) {
	b.summary.Strategy = "pr"
	b.summary.Branch = branch
	b.summary.PullURL = pullURL
	b.summary.PullNumber = pullNumber
}

func (b *githubSyncSummaryBuilder) JSON() string {
	if len(b.summary.Errors) == 0 && len(b.summary.Conflicts) == 0 && b.summary.Strategy == "" {
		return ""
	}
	encoded, err := json.Marshal(b.summary)
	if err != nil {
		return fmt.Sprintf(`{"error":"marshal github sync summary: %v"}`, err)
	}
	return string(encoded)
}

type githubSyncJobInput struct {
	ProjectID string
	Kind      string
	Owner     string
	Repo      string
	Branch    string
	Path      string
	ScriptIDs []string
	Strategy  string
	StartedBy string
}

func newGitHubSyncService(runner *TestRunner, store storage.GitHubEnterpriseStore, linkStore storage.GitHubScriptLinkStore) *githubSyncService {
	claimStore, _ := linkStore.(storage.GitHubScriptSyncClaimStore)
	return &githubSyncService{
		runner:     runner,
		store:      store,
		linkStore:  linkStore,
		claimStore: claimStore,
	}
}

func (svc *githubSyncService) Run(ctx context.Context, resolved *githubResolvedClient, input githubSyncJobInput) (*storage.GitHubSyncJob, error) {
	switch input.Kind {
	case "import", "push":
	default:
		return nil, fmt.Errorf("kind must be import or push")
	}
	if input.Kind == "push" {
		input.Strategy = strings.ToLower(strings.TrimSpace(input.Strategy))
		if input.Strategy == "" {
			input.Strategy = "pr"
		}
		if input.Strategy != "pr" && input.Strategy != "commit" {
			return nil, fmt.Errorf("strategy must be 'commit' or 'pr'")
		}
	}
	if resolved == nil || resolved.Client == nil {
		return nil, fmt.Errorf("github client is not configured")
	}
	if svc.store == nil {
		return nil, fmt.Errorf("github enterprise job storage is unavailable")
	}
	if input.Kind == "import" {
		if svc.runner == nil {
			return nil, fmt.Errorf("test runner is not configured")
		}
		if svc.linkStore == nil {
			return nil, fmt.Errorf("github link storage is unavailable")
		}
		if _, ok := svc.linkStore.(storage.GitHubScriptImportStore); !ok {
			return nil, fmt.Errorf("atomic github import storage is unavailable")
		}
	}

	job := &storage.GitHubSyncJob{
		ID:        uuid.New().String(),
		ProjectID: input.ProjectID,
		Kind:      input.Kind,
		Status:    "running",
		Owner:     input.Owner,
		Repo:      input.Repo,
		Branch:    input.Branch,
		Path:      input.Path,
		StartedBy: input.StartedBy,
	}
	if err := svc.store.SaveGitHubSyncJob(job); err != nil {
		return nil, fmt.Errorf("persist GitHub sync job before execution: %w", err)
	}

	switch input.Kind {
	case "import":
		svc.runImport(ctx, resolved.Client, job)
	case "push":
		svc.runPush(ctx, resolved, job, input.ScriptIDs, input.Strategy)
	}

	job.Status = "complete"
	completed := time.Now()
	job.CompletedAt = &completed
	if job.Errors > 0 || job.Conflicts > 0 {
		job.Status = "partial"
	}
	if err := svc.store.SaveGitHubSyncJob(job); err != nil {
		return job, fmt.Errorf("persist completed GitHub sync job: %w", err)
	}
	return job, nil
}

func (svc *githubSyncService) runImport(ctx context.Context, client *githubClient, job *storage.GitHubSyncJob) {
	summary := githubSyncSummaryBuilder{}
	refreshSummary := func() {
		if next := summary.JSON(); next != "" {
			job.SummaryJSON = next
		}
	}

	if svc.runner == nil {
		job.Errors++
		summary.addError(githubSyncSummaryItem{Error: "test runner is not configured"})
		refreshSummary()
		return
	}
	if svc.linkStore == nil {
		job.Errors++
		summary.addError(githubSyncSummaryItem{Error: "github link storage is unavailable"})
		refreshSummary()
		return
	}
	if _, ok := svc.linkStore.(storage.GitHubScriptImportStore); !ok {
		job.Errors++
		summary.addError(githubSyncSummaryItem{Error: "atomic github import storage is unavailable"})
		refreshSummary()
		return
	}
	remoteCommitSHA, err := client.GetBranchHeadSHA(ctx, job.Owner, job.Repo, job.Branch)
	if err != nil {
		job.Errors++
		summary.addError(githubSyncSummaryItem{Error: "resolve github import branch head: " + err.Error()})
		refreshSummary()
		return
	}
	remoteCommitSHA = strings.TrimSpace(remoteCommitSHA)
	if remoteCommitSHA == "" {
		job.Errors++
		summary.addError(githubSyncSummaryItem{Error: "resolve github import branch head: branch does not exist"})
		refreshSummary()
		return
	}
	// Resolve the mutable branch exactly once, then fetch the directory and every
	// file from that immutable commit. The requested branch remains the durable
	// synchronization target, while RemoteCommitSHA records the snapshot read.
	contents, err := client.GetContents(ctx, job.Owner, job.Repo, job.Path, remoteCommitSHA)
	if err != nil {
		job.Errors++
		summary.addError(githubSyncSummaryItem{Error: err.Error()})
		refreshSummary()
		return
	}
	entries := contents.Entries
	if !contents.IsDir && contents.File != nil {
		entries = []githubContent{*contents.File}
	}
	job.Total = len(entries)
	type importCandidate struct {
		script    *dsl.Script
		path      string
		blobSHA   string
		target    storage.GitHubScriptSyncTarget
		commitSHA string
	}
	candidates := make([]importCandidate, 0, len(entries))
	targets := make(map[string]string)
	scriptIDs := make(map[string]string)
	for _, entry := range entries {
		if entry.Type != "file" || !strings.HasSuffix(strings.ToLower(entry.Path), ".json") {
			continue
		}
		fileContents, err := client.GetContents(ctx, job.Owner, job.Repo, entry.Path, remoteCommitSHA)
		if err != nil || fileContents.File == nil {
			job.Errors++
			summary.addError(githubSyncSummaryItem{Error: "read github import script file", Path: entry.Path})
			refreshSummary()
			continue
		}
		data, err := fileContents.File.DecodedContent()
		if err != nil {
			job.Errors++
			summary.addError(githubSyncSummaryItem{Error: "decode github import script file", Path: entry.Path})
			refreshSummary()
			continue
		}
		script, err := dsl.Parse(data)
		if err != nil {
			job.Errors++
			summary.addError(githubSyncSummaryItem{Error: "parse github import script", Path: entry.Path})
			refreshSummary()
			continue
		}
		script.ProjectID = job.ProjectID
		script.ID = strings.TrimSpace(script.ID)
		if script.ID == "" {
			script.ID = deterministicGitHubImportScriptID(job.ProjectID, job.Owner, job.Repo, job.Branch, entry.Path)
		}
		target := storage.GitHubScriptSyncTarget{Owner: job.Owner, Repo: job.Repo, Branch: job.Branch, Path: entry.Path}
		targetKey := normalizedGitHubImportTargetKey(target)
		if previousPath, exists := targets[targetKey]; exists {
			job.Conflicts++
			summary.addConflict(githubSyncSummaryItem{
				Error:    "duplicate normalized github import target",
				Paths:    []string{previousPath, entry.Path},
				Path:     entry.Path,
				PrevPath: previousPath,
			})
			refreshSummary()
			continue
		}
		targets[targetKey] = entry.Path
		if previousPath, exists := scriptIDs[script.ID]; exists {
			job.Conflicts++
			summary.addConflict(githubSyncSummaryItem{
				Error:    "duplicate github import script id",
				ScriptID: script.ID,
				Path:     entry.Path,
				PrevPath: previousPath,
				Paths:    []string{previousPath, entry.Path},
			})
			refreshSummary()
			continue
		}
		scriptIDs[script.ID] = entry.Path
		candidates = append(candidates, importCandidate{
			script: script, path: entry.Path, blobSHA: fileContents.File.SHA, target: target, commitSHA: remoteCommitSHA,
		})
	}
	// Remote parse/read failures and intra-batch identity collisions are a batch
	// preflight failure: no local script, link, ref-key, or target owner has been
	// mutated yet, so abort rather than committing a partial directory import.
	if job.Errors != 0 || job.Conflicts != 0 {
		return
	}
	for _, candidate := range candidates {
		if err := svc.runner.PreflightGitHubScriptImport(candidate.script, candidate.target); err != nil {
			if errors.Is(err, errScriptConcurrentChange) || storage.IsGitHubScriptSyncBusy(err) {
				job.Conflicts++
				summary.addConflict(githubSyncSummaryItem{Error: err.Error(), Path: candidate.path})
			} else {
				job.Errors++
				summary.addError(githubSyncSummaryItem{Error: err.Error(), Path: candidate.path})
			}
			refreshSummary()
		}
	}
	if job.Errors != 0 || job.Conflicts != 0 {
		return
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
	for _, candidate := range candidates {
		stored, outcome, err := svc.runner.StoreGitHubScriptImportDurable(candidate.script, &storage.GitHubScriptLink{
			ScriptID:        candidate.script.ID,
			Owner:           candidate.target.Owner,
			Repo:            candidate.target.Repo,
			Branch:          candidate.target.Branch,
			Path:            candidate.target.Path,
			LastCommitSHA:   candidate.blobSHA,
			RemoteBlobSHA:   candidate.blobSHA,
			RemoteCommitSHA: candidate.commitSHA,
		})
		if err != nil {
			if errors.Is(err, errScriptConcurrentChange) || storage.IsGitHubScriptSyncBusy(err) {
				job.Conflicts++
				summary.addConflict(githubSyncSummaryItem{Error: err.Error(), Path: candidate.path})
			} else {
				job.Errors++
				summary.addError(githubSyncSummaryItem{Error: err.Error(), Path: candidate.path})
			}
			refreshSummary()
			continue
		}
		if outcome == storage.GitHubScriptImportConflict || stored == nil {
			job.Conflicts++
			summary.addConflict(githubSyncSummaryItem{Error: "gitHub script import conflict", Path: candidate.path})
			refreshSummary()
			continue
		}
		switch outcome {
		case storage.GitHubScriptImportCreated:
			job.Created++
		case storage.GitHubScriptImportUpdated:
			job.Updated++
		}
		job.Processed++
	}
}

func normalizedGitHubImportTargetKey(target storage.GitHubScriptSyncTarget) string {
	return strings.Join([]string{
		strings.ToLower(strings.TrimSpace(target.Owner)),
		strings.ToLower(strings.TrimSpace(target.Repo)),
		strings.TrimSpace(target.Branch),
		strings.TrimSpace(target.Path),
	}, "\x00")
}

func deterministicGitHubImportScriptID(projectID, owner, repo, branch, path string) string {
	identity := strings.Join([]string{
		"apexload:github-import-script:v1",
		normalizeProjectID(projectID),
		strings.ToLower(strings.TrimSpace(owner)),
		strings.ToLower(strings.TrimSpace(repo)),
		strings.TrimSpace(branch),
		strings.TrimSpace(path),
	}, "\x00")
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity)).String()
}

func (svc *githubSyncService) runPush(ctx context.Context, resolved *githubResolvedClient, job *storage.GitHubSyncJob, scriptIDs []string, strategy string) {
	summary := githubSyncSummaryBuilder{}
	refreshSummary := func() {
		if next := summary.JSON(); next != "" {
			job.SummaryJSON = next
		}
	}

	if svc.runner == nil {
		job.Errors++
		summary.addError(githubSyncSummaryItem{Error: "test runner is not configured"})
		refreshSummary()
		return
	}
	if svc.linkStore == nil {
		job.Errors++
		summary.addError(githubSyncSummaryItem{Error: "github link storage is unavailable"})
		refreshSummary()
		return
	}
	if svc.claimStore == nil {
		job.Errors++
		summary.addError(githubSyncSummaryItem{Error: "github sync claim storage is unavailable"})
		refreshSummary()
		return
	}
	if resolved == nil || resolved.Client == nil {
		job.Errors++
		summary.addError(githubSyncSummaryItem{Error: "github client is not configured"})
		refreshSummary()
		return
	}
	strategy = strings.ToLower(strings.TrimSpace(strategy))
	if strategy == "" {
		strategy = "pr"
	}
	writeBranch := job.Branch
	if strategy == "pr" {
		if strings.TrimSpace(job.ID) == "" {
			job.ID = uuid.NewString()
		}
		writeBranch = "apexload/bulk-" + job.ID
	}
	selected := map[string]struct{}{}
	for _, id := range scriptIDs {
		selected[id] = struct{}{}
	}
	scripts := svc.runner.ListScriptsFiltered(job.ProjectID, nil)
	type pushCandidate struct {
		stored      *StoredScript
		targetPath  string
		content     []byte
		contentHash string
		remoteSHA   string
		claimID     string
		targets     []storage.GitHubScriptSyncTarget
	}
	candidates := make([]pushCandidate, 0, len(scripts))
	paths := make(map[string]string)
	pathCollision := false
	for _, stored := range scripts {
		if stored == nil || stored.Script == nil || normalizeProjectID(stored.ProjectID) != normalizeProjectID(job.ProjectID) || normalizeProjectID(stored.Script.ProjectID) != normalizeProjectID(job.ProjectID) {
			job.Conflicts++
			continue
		}
		if len(selected) > 0 {
			if _, ok := selected[stored.ID]; !ok {
				continue
			}
		}
		job.Total++
		targetPath := strings.TrimSpace(renderGitHubPathTemplate(firstNonEmpty(resolved.Policy.PathTemplate, resolved.Settings.PathTemplate), stored))
		if err := enforceGitHubProjectPolicy(resolved.Policy, "push", job.Owner, job.Repo, job.Branch, targetPath, strategy); err != nil {
			job.Conflicts++
			summary.addConflict(githubSyncSummaryItem{Error: err.Error(), TargetPath: targetPath, ScriptID: stored.ID})
			refreshSummary()
			continue
		}
		content, contentHash, err := githubScriptEnvelope(stored.Script)
		if err != nil {
			job.Errors++
			summary.addError(githubSyncSummaryItem{Error: err.Error(), ScriptID: stored.ID, TargetPath: targetPath})
			refreshSummary()
			continue
		}
		if priorID, collision := paths[targetPath]; collision {
			job.Conflicts++
			pathCollision = true
			summary.addConflict(githubSyncSummaryItem{
				Error:     "rendered path collision",
				ScriptIDs: []string{priorID, stored.ID},
				Path:      targetPath,
			})
			refreshSummary()
			continue
		}
		paths[targetPath] = stored.ID
		targets := []storage.GitHubScriptSyncTarget{{Owner: job.Owner, Repo: job.Repo, Branch: job.Branch, Path: targetPath}}
		if strategy == "pr" {
			targets = append(targets, storage.GitHubScriptSyncTarget{Owner: job.Owner, Repo: job.Repo, Branch: writeBranch, Path: targetPath})
		}
		candidates = append(candidates, pushCandidate{
			stored: stored, targetPath: targetPath, content: content, contentHash: contentHash, targets: targets,
		})
	}
	if pathCollision {
		return
	}

	// Claim every script in stable order before the first remote read. Claims
	// are durable and HA-safe, so a concurrent single or bulk sync cannot make a
	// second stale preflight decision for the same script on another controller.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].stored.ID < candidates[j].stored.ID })
	claimed := make([]pushCandidate, 0, len(candidates))
	retainClaim := make(map[string]bool, len(candidates))
	defer func() {
		for _, candidate := range claimed {
			if retainClaim[candidate.stored.ID] {
				continue
			}
			_, _ = svc.claimStore.ReleaseGitHubScriptSyncClaim(candidate.stored.ID, candidate.claimID)
		}
	}()
	for index := range candidates {
		candidates[index].claimID = uuid.NewString()
		acquired, err := svc.claimStore.AcquireGitHubScriptSyncClaim(
			candidates[index].stored.ID, candidates[index].claimID, candidates[index].targets,
			time.Now().UTC().Add(githubScriptSyncClaimTTL),
		)
		if err != nil {
			job.Errors++
			summary.addError(githubSyncSummaryItem{Error: "failed to acquire github sync claim", ScriptID: candidates[index].stored.ID})
			refreshSummary()
			return
		}
		if !acquired {
			job.Conflicts++
			summary.addConflict(githubSyncSummaryItem{Error: "another github sync is already running for a selected script", ScriptID: candidates[index].stored.ID})
			refreshSummary()
			return
		}
		claimed = append(claimed, candidates[index])
	}
	renewClaim := func(candidate pushCandidate) bool {
		renewed, err := svc.claimStore.RenewGitHubScriptSyncClaim(
			candidate.stored.ID, candidate.claimID, time.Now().UTC().Add(githubScriptSyncClaimTTL),
		)
		if err != nil {
			job.Errors++
			return false
		}
		if !renewed {
			job.Conflicts++
			return false
		}
		return true
	}

	// Resolve every remote and durable drift witness before the first mutation.
	// A failed read aborts the whole bulk write so an error on a later script
	// cannot leave earlier direct commits or a partial pull-request branch.
	preflighted := make([]pushCandidate, 0, len(candidates))
	preflightReadFailed := false
	for _, candidate := range candidates {
		driftSHA, err := githubSyncRemoteBlobSHA(ctx, resolved.Client, job.Owner, job.Repo, candidate.targetPath, job.Branch)
		if err != nil {
			job.Errors++
			summary.addError(githubSyncSummaryItem{Error: err.Error(), Path: candidate.targetPath, ScriptID: candidate.stored.ID})
			refreshSummary()
			preflightReadFailed = true
			continue
		}
		link, err := svc.linkStore.GetGitHubScriptLink(candidate.stored.ID)
		if err != nil {
			job.Errors++
			summary.addError(githubSyncSummaryItem{Error: err.Error(), ScriptID: candidate.stored.ID, Path: candidate.targetPath})
			refreshSummary()
			preflightReadFailed = true
			continue
		}
		primaryTarget := githubSyncLinkMatchesTarget(link, job.Owner, job.Repo, job.Branch, candidate.targetPath)
		baseTarget := githubSyncBaseWitnessMatchesTarget(link, job.Owner, job.Repo, job.Branch, candidate.targetPath)
		witnessSHA, hasWitness := githubSyncDriftWitnessForTarget(link, job.Owner, job.Repo, job.Branch, candidate.targetPath)
		if (primaryTarget || baseTarget) && !hasWitness {
			// A link that names this target but has no coherent blob/missing
			// witness is unsafe to overwrite automatically.
			job.Conflicts++
			summary.addConflict(githubSyncSummaryItem{Error: "target has no safe witness for overwrite", ScriptID: candidate.stored.ID, Path: candidate.targetPath})
			refreshSummary()
			continue
		}
		if hasWitness && witnessSHA != strings.TrimSpace(driftSHA) {
			job.Conflicts++
			summary.addConflict(githubSyncSummaryItem{Error: "drift witness mismatch", ScriptID: candidate.stored.ID, Path: candidate.targetPath})
			refreshSummary()
			continue
		}
		if strategy == "commit" && strings.TrimSpace(driftSHA) != "" && !hasWitness {
			// A direct commit is not a safe first-sync takeover mechanism. Without a
			// durable witness, replacing a pre-existing remote file would bypass the
			// optimistic-concurrency contract even though GitHub accepts its SHA.
			job.Conflicts++
			summary.addConflict(githubSyncSummaryItem{Error: "commit strategy requires a known drift witness for existing file", ScriptID: candidate.stored.ID, Path: candidate.targetPath})
			refreshSummary()
			continue
		}
		if strategy == "pr" && link != nil && !primaryTarget && !baseTarget {
			// A pull request needs to retain both its temporary head and the
			// preflighted base. Do not replace an unrelated script link when
			// this single row cannot represent all three targets.
			job.Conflicts++
			summary.addConflict(githubSyncSummaryItem{Error: "existing link is for unrelated target", ScriptID: candidate.stored.ID, Path: candidate.targetPath})
			refreshSummary()
			continue
		}
		candidate.remoteSHA = driftSHA
		preflighted = append(preflighted, candidate)
	}
	if preflightReadFailed {
		return
	}
	candidates = preflighted
	if len(candidates) == 0 {
		return
	}
	// A large all-candidate preflight can consume meaningful lease time. Renew
	// every surviving claim before creating a shared branch or writing a file.
	for _, candidate := range candidates {
		if !renewClaim(candidate) {
			return
		}
	}

	if strategy == "pr" {
		baseCommitSHA, err := resolved.Client.GetBranchHeadSHA(ctx, job.Owner, job.Repo, job.Branch)
		if err != nil || strings.TrimSpace(baseCommitSHA) == "" {
			job.Errors++
			summary.addError(githubSyncSummaryItem{Error: "unable to resolve base branch for pull request", Path: writeBranch})
			refreshSummary()
			return
		}
		// Job IDs are unique. Reusing a pre-existing branch would require another
		// complete head-branch preflight, so fail closed on every create error.
		if err := resolved.Client.CreateBranch(ctx, job.Owner, job.Repo, writeBranch, baseCommitSHA); err != nil {
			job.Errors++
			summary.addError(githubSyncSummaryItem{Error: "failed to create pull request head branch", Path: writeBranch})
			refreshSummary()
			return
		}
	}

	type pendingPRLink struct {
		candidate pushCandidate
		link      *storage.GitHubScriptLink
	}
	pendingPRLinks := make([]pendingPRLink, 0, len(candidates))
	for _, candidate := range candidates {
		stored, targetPath := candidate.stored, candidate.targetPath
		if !renewClaim(candidate) {
			continue
		}
		result, err := resolved.Client.PutFile(ctx, job.Owner, job.Repo, targetPath, writeBranch, "Sync "+stored.Name+" via APEX Load", candidate.content, candidate.remoteSHA)
		if err != nil {
			job.Errors++
			summary.addError(githubSyncSummaryItem{Error: err.Error(), Path: targetPath, ScriptID: stored.ID})
			refreshSummary()
			continue
		}
		if strategy == "pr" {
			// Keep the temporary head as the truthful synced target while also
			// retaining the base blob (including known-missing) used during
			// preflight. The link is only written after PR creation succeeds.
			pendingPRLinks = append(pendingPRLinks, pendingPRLink{
				candidate: candidate,
				link: &storage.GitHubScriptLink{
					ScriptID: stored.ID, RefKey: stored.RefKey, Owner: job.Owner, Repo: job.Repo,
					Branch: writeBranch, Path: targetPath, LastCommitSHA: result.Content.SHA,
					RemoteBlobSHA: result.Content.SHA, RemoteCommitSHA: result.Commit.SHA, ContentHash: candidate.contentHash,
					BaseBranch: job.Branch, BaseBlobSHA: candidate.remoteSHA, BaseMissing: candidate.remoteSHA == "",
				},
			})
		} else {
			finalized, err := svc.claimStore.FinalizeGitHubScriptSyncClaim(&storage.GitHubScriptLink{
				ScriptID: stored.ID, RefKey: stored.RefKey, Owner: job.Owner, Repo: job.Repo,
				Branch: writeBranch, Path: targetPath, LastCommitSHA: result.Content.SHA,
				RemoteBlobSHA: result.Content.SHA, RemoteCommitSHA: result.Commit.SHA, ContentHash: candidate.contentHash,
			}, candidate.claimID)
			if err != nil {
				job.Errors++
				summary.addError(githubSyncSummaryItem{Error: err.Error(), Path: targetPath, ScriptID: stored.ID})
				retainClaim[stored.ID] = true
				refreshSummary()
			} else if !finalized {
				job.Conflicts++
				summary.addConflict(githubSyncSummaryItem{Error: "sync claim expired before commit finalization", Path: targetPath, ScriptID: stored.ID})
				retainClaim[stored.ID] = true
				refreshSummary()
			}
		}
		job.Updated++
		job.Processed++
	}
	if strategy == "pr" && job.Updated > 0 {
		// Earlier writes can make a large bulk PR take longer than one lease
		// interval. Renew every successful candidate before exposing the branch.
		for _, pending := range pendingPRLinks {
			if !renewClaim(pending.candidate) {
				return
			}
		}
		pull, err := resolved.Client.CreatePull(ctx, job.Owner, job.Repo, "Sync APEX Load scripts", writeBranch, job.Branch, "Bulk script synchronization from APEX Load.")
		if err != nil {
			job.Errors++
			summary.addError(githubSyncSummaryItem{Error: "failed to create pull request", Path: writeBranch})
			refreshSummary()
			return
		}
		summary.setPullRequest(writeBranch, pull.HTMLURL, pull.Number)
		job.SummaryJSON = summary.JSON()
		for _, pending := range pendingPRLinks {
			finalized, err := svc.claimStore.FinalizeGitHubScriptSyncClaim(pending.link, pending.candidate.claimID)
			if err != nil {
				job.Errors++
				summary.addError(githubSyncSummaryItem{Error: err.Error(), Path: pending.candidate.targetPath, ScriptID: pending.candidate.stored.ID})
				retainClaim[pending.candidate.stored.ID] = true
				refreshSummary()
			} else if !finalized {
				job.Conflicts++
				summary.addConflict(githubSyncSummaryItem{Error: "sync claim expired before pull-request finalization", Path: pending.candidate.targetPath, ScriptID: pending.candidate.stored.ID})
				retainClaim[pending.candidate.stored.ID] = true
				refreshSummary()
			}
		}
	}
}

func githubSyncRemoteBlobSHA(ctx context.Context, client *githubClient, owner, repo, path, branch string) (string, error) {
	contents, err := client.GetContents(ctx, owner, repo, path, branch)
	if err != nil {
		if apiErr, ok := err.(*githubAPIError); ok && apiErr.StatusCode == http.StatusNotFound {
			return "", nil
		}
		return "", err
	}
	if contents == nil || contents.File == nil {
		return "", fmt.Errorf("github content path %q on branch %q is not a file", path, branch)
	}
	return strings.TrimSpace(contents.File.SHA), nil
}

func githubSyncLinkMatchesTarget(link *storage.GitHubScriptLink, owner, repo, branch, path string) bool {
	return githubSyncLinkCoordinatesMatch(link, owner, repo, path) &&
		strings.TrimSpace(link.Branch) == strings.TrimSpace(branch)
}

func githubSyncBaseWitnessMatchesTarget(link *storage.GitHubScriptLink, owner, repo, branch, path string) bool {
	return githubSyncLinkCoordinatesMatch(link, owner, repo, path) &&
		strings.TrimSpace(link.BaseBranch) == strings.TrimSpace(branch)
}

func githubSyncLinkCoordinatesMatch(link *storage.GitHubScriptLink, owner, repo, path string) bool {
	return link != nil &&
		strings.EqualFold(strings.TrimSpace(link.Owner), strings.TrimSpace(owner)) &&
		strings.EqualFold(strings.TrimSpace(link.Repo), strings.TrimSpace(repo)) &&
		strings.TrimSpace(link.Path) == strings.TrimSpace(path)
}

// githubSyncDriftWitnessForTarget returns the blob observed at the last safe
// synchronization point. An empty SHA is a valid witness only when the target
// is explicitly known to have been missing (or is a legacy primary link, whose
// target match historically carried that meaning).
func githubSyncDriftWitnessForTarget(link *storage.GitHubScriptLink, owner, repo, branch, path string) (string, bool) {
	if githubSyncLinkMatchesTarget(link, owner, repo, branch, path) {
		return strings.TrimSpace(firstNonEmpty(link.RemoteBlobSHA, link.LastCommitSHA)), true
	}
	if !githubSyncBaseWitnessMatchesTarget(link, owner, repo, branch, path) {
		return "", false
	}
	baseSHA := strings.TrimSpace(link.BaseBlobSHA)
	if link.BaseMissing {
		return "", baseSHA == ""
	}
	return baseSHA, baseSHA != ""
}
