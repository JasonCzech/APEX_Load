package api

import (
	"context"
	"sort"

	"github.com/jasonczech/apexload/pkg/storage"
)

// RunArchiveProjectLiveTableAttachment records a run's immutable attachment,
// never the shared table's global counts or another run's operations or values.
type RunArchiveProjectLiveTableAttachment struct {
	TableID      string `json:"table_id"`
	InstanceID   string `json:"instance_id"`
	AttachmentID string `json:"attachment_id"`
	Group        string `json:"group"`
	Alias        string `json:"alias"`
	State        string `json:"state"`
	Revision     uint64 `json:"revision,string"`
}

func projectAttachmentProvenance(a storage.ProjectLiveTableAttachment, revision uint64) RunArchiveProjectLiveTableAttachment {
	return RunArchiveProjectLiveTableAttachment{TableID: a.TableID, InstanceID: a.InstanceID, AttachmentID: a.ID, Group: a.Group, Alias: a.Alias, State: a.State, Revision: revision}
}

// run.mu must be held. The revision is only a position in the shared instance;
// it deliberately conveys no attribution of its contents to this run.
func projectLiveTableProvenanceLocked(tr *TestRunner, run *runLiveTables) []RunArchiveProjectLiveTableAttachment {
	out := append([]RunArchiveProjectLiveTableAttachment(nil), run.projectSummaries...)
	if len(run.projectAttachments) > 0 {
		out = nil
		for _, a := range run.projectAttachments {
			revision := uint64(0)
			if rt := tr.projectLiveTableService().cachedInstance(a.InstanceID, run.projectTerm); rt != nil {
				revision = rt.table.Stats().Revision
				tr.projectLiveTableService().release(rt)
			}
			out = append(out, projectAttachmentProvenance(a, revision))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group == out[j].Group {
			return out[i].Alias < out[j].Alias
		}
		return out[i].Group < out[j].Group
	})
	return out
}
func (tr *TestRunner) projectLiveTableArchiveSummaries(id string) []RunArchiveProjectLiveTableAttachment {
	run := tr.liveRun(id)
	if run == nil {
		return nil
	}
	run.mu.RLock()
	defer run.mu.RUnlock()
	return projectLiveTableProvenanceLocked(tr, run)
}
func (tr *TestRunner) projectLiveTableRunMetadata(ctx context.Context, test *ManagedTest) ([]RunArchiveProjectLiveTableAttachment, error) {
	if run := tr.liveRun(test.ID); run != nil {
		run.mu.RLock()
		defer run.mu.RUnlock()
		return projectLiveTableProvenanceLocked(tr, run), nil
	}
	if !hasProjectLiveTables(test.DataPools) {
		return nil, nil
	}
	store, err := tr.projectLiveTableService().store()
	if err != nil {
		return nil, err
	}
	attachments, err := store.ListProjectLiveTableRunAttachments(ctx, test.ProjectID, test.ID)
	if err != nil {
		return nil, err
	}
	out := make([]RunArchiveProjectLiveTableAttachment, 0, len(attachments))
	for _, a := range attachments {
		revision := uint64(0)
		if a.SettledRevision != nil {
			revision = *a.SettledRevision
		}
		out = append(out, projectAttachmentProvenance(a, revision))
	}
	return out, nil
}
