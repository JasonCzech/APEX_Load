package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type githubSyncService struct {
	runner    *TestRunner
	store     storage.GitHubEnterpriseStore
	linkStore storage.GitHubScriptLinkStore
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
	return &githubSyncService{
		runner:    runner,
		store:     store,
		linkStore: linkStore,
	}
}

func (svc *githubSyncService) Run(ctx context.Context, resolved *githubResolvedClient, input githubSyncJobInput) (*storage.GitHubSyncJob, error) {
	switch input.Kind {
	case "import", "push":
	default:
		return nil, fmt.Errorf("kind must be import or push")
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
	if svc.store != nil {
		if err := svc.store.SaveGitHubSyncJob(job); err != nil {
			return nil, fmt.Errorf("persist GitHub sync job before execution: %w", err)
		}
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
	if svc.store != nil {
		if err := svc.store.SaveGitHubSyncJob(job); err != nil {
			return job, fmt.Errorf("persist completed GitHub sync job: %w", err)
		}
	}
	return job, nil
}

func (svc *githubSyncService) runImport(ctx context.Context, client *githubClient, job *storage.GitHubSyncJob) {
	if svc.runner == nil {
		job.Errors++
		job.SummaryJSON = `{"error":"test runner is not configured"}`
		return
	}
	contents, err := client.GetContents(ctx, job.Owner, job.Repo, job.Path, job.Branch)
	if err != nil {
		job.Errors++
		job.SummaryJSON = fmt.Sprintf(`{"error":%q}`, err.Error())
		return
	}
	remoteCommitSHA, _ := client.GetBranchHeadSHA(ctx, job.Owner, job.Repo, job.Branch)
	entries := contents.Entries
	if !contents.IsDir && contents.File != nil {
		entries = []githubContent{*contents.File}
	}
	job.Total = len(entries)
	for _, entry := range entries {
		if entry.Type != "file" || !strings.HasSuffix(strings.ToLower(entry.Path), ".json") {
			continue
		}
		fileContents, err := client.GetContents(ctx, job.Owner, job.Repo, entry.Path, job.Branch)
		if err != nil || fileContents.File == nil {
			job.Errors++
			continue
		}
		data, err := fileContents.File.DecodedContent()
		if err != nil {
			job.Errors++
			continue
		}
		script, err := dsl.Parse(data)
		if err != nil {
			job.Errors++
			continue
		}
		script.ProjectID = job.ProjectID
		stored, err := svc.runner.StoreScriptDurable(script)
		if err != nil {
			job.Errors++
			continue
		}
		job.Created++
		job.Processed++
		if svc.linkStore != nil {
			_, contentHash, _ := githubScriptEnvelope(stored.Script)
			if err := svc.linkStore.SaveGitHubScriptLink(&storage.GitHubScriptLink{
				ScriptID:        stored.ID,
				RefKey:          stored.RefKey,
				Owner:           job.Owner,
				Repo:            job.Repo,
				Branch:          job.Branch,
				Path:            entry.Path,
				LastCommitSHA:   fileContents.File.SHA,
				RemoteBlobSHA:   fileContents.File.SHA,
				RemoteCommitSHA: remoteCommitSHA,
				ContentHash:     contentHash,
			}); err != nil {
				job.Errors++
			}
		}
	}
}

func (svc *githubSyncService) runPush(ctx context.Context, resolved *githubResolvedClient, job *storage.GitHubSyncJob, scriptIDs []string, strategy string) {
	if svc.runner == nil {
		job.Errors++
		job.SummaryJSON = `{"error":"test runner is not configured"}`
		return
	}
	if strategy == "" {
		strategy = "pr"
	}
	selected := map[string]struct{}{}
	for _, id := range scriptIDs {
		selected[id] = struct{}{}
	}
	scripts := svc.runner.ListScriptsFiltered(job.ProjectID, nil)
	job.Total = len(scripts)
	for _, stored := range scripts {
		if len(selected) > 0 {
			if _, ok := selected[stored.ID]; !ok {
				continue
			}
		}
		targetPath := renderGitHubPathTemplate(firstNonEmpty(resolved.Policy.PathTemplate, resolved.Settings.PathTemplate), stored)
		if err := enforceGitHubProjectPolicy(resolved.Policy, "push", job.Owner, job.Repo, job.Branch, targetPath, strategy); err != nil {
			job.Conflicts++
			continue
		}
		content, contentHash, err := githubScriptEnvelope(stored.Script)
		if err != nil {
			job.Errors++
			continue
		}
		existing, _ := resolved.Client.GetContents(ctx, job.Owner, job.Repo, targetPath, job.Branch)
		remoteSHA := ""
		if existing != nil && existing.File != nil {
			remoteSHA = existing.File.SHA
		}
		result, err := resolved.Client.PutFile(ctx, job.Owner, job.Repo, targetPath, job.Branch, "Sync "+stored.Name+" via APEX Load", content, remoteSHA)
		if err != nil {
			job.Errors++
			continue
		}
		if svc.linkStore != nil {
			if err := svc.linkStore.SaveGitHubScriptLink(&storage.GitHubScriptLink{
				ScriptID:        stored.ID,
				RefKey:          stored.RefKey,
				Owner:           job.Owner,
				Repo:            job.Repo,
				Branch:          job.Branch,
				Path:            targetPath,
				LastCommitSHA:   result.Content.SHA,
				RemoteBlobSHA:   result.Content.SHA,
				RemoteCommitSHA: result.Commit.SHA,
				ContentHash:     contentHash,
			}); err != nil {
				job.Errors++
			}
		}
		job.Updated++
		job.Processed++
	}
}
