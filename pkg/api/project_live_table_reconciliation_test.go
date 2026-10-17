package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

type projectDelayedAttachmentStore struct {
	SQLiteStore   *storage.SQLiteStore
	pause         atomic.Bool
	read, release chan struct{}
}

func (s *projectDelayedAttachmentStore) ListProjectLiveTableAttachments(ctx context.Context, project, table string) ([]storage.ProjectLiveTableAttachment, error) {
	rows, err := s.SQLiteStore.ListProjectLiveTableAttachments(ctx, project, table)
	if err == nil && s.pause.Swap(false) {
		close(s.read)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return rows, err
}

func TestProjectLiveTableReconciliationCannotUndoAttachmentPublication(t *testing.T) {
	for _, operation := range []string{"detach", "attach"} {
		t.Run(operation, func(t *testing.T) {
			runner, store, d := newProjectTableFixture(t)
			ctx := context.Background()
			run := projectTableRun(t, runner, store, "publication-run", d, "push")
			svc := runner.projectLiveTableService()
			rt, err := svc.acquire(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
			if err != nil {
				t.Fatal(err)
			}
			defer svc.release(rt)
			attachments, err := store.ListProjectLiveTableRunAttachments(ctx, d.ProjectID, run.ID)
			if err != nil || len(attachments) != 1 {
				t.Fatalf("attachments %+v %v", attachments, err)
			}
			a := attachments[0]
			wrapper := &projectDelayedAttachmentStore{SQLiteStore: store, read: make(chan struct{}), release: make(chan struct{})}
			wrapper.pause.Store(true)
			runner.dataPoolStore = sqliteOverlay(wrapper)
			defer func() { runner.dataPoolStore = store }()
			var once sync.Once
			done := make(chan error, 1)
			go func() { done <- svc.reconcileAttachments(ctx, rt) }()
			defer func() {
				once.Do(func() { close(wrapper.release) })
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("reconcile: %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Error("reconciliation worker did not exit")
				}
			}()
			select {
			case <-wrapper.read:
			case <-time.After(3 * time.Second):
				t.Fatal("reconciliation did not read attachments")
			}
			// The database operation has completed and its publication races with
			// an older successful reconciliation read, just as in run preparation
			// and finalization. Keep the actual store's fence in this regression.
			if operation == "detach" {
				expires := time.Now().Add(time.Minute)
				if err := store.DetachProjectLiveTableAttachments(ctx, d.ProjectID, run.ID, expires, liveStorageTerm(rt.term)); err != nil {
					t.Fatal(err)
				}
				a.State, a.RetireAfter = "detached", &expires
			} else {
				a.ID, a.Incarnation, a.Group = "added-attachment", "added-incarnation", "second-group"
				added, err := store.CreateProjectLiveTableAttachments(ctx, []storage.ProjectLiveTableAttachmentCreate{{Attachment: a, ExpectedDefinitionRevision: d.Revision}}, liveStorageTerm(rt.term))
				if err != nil || len(added) != 1 {
					t.Fatalf("concurrent attach %+v %v", added, err)
				}
				a = added[0]
			}
			rt.publishAttachment(a)
			once.Do(func() { close(wrapper.release) })
			// Acquiring the metadata lock joins reconciliation before checking the
			// authorization view, without consuming the worker's cleanup result.
			rt.mu.Lock()
			rt.mu.Unlock()
			err = rt.authorize(livetable.Request{ClientID: projectAttachmentPrefix(a.Incarnation) + "worker", Operation: livetable.OpPush})
			if operation == "detach" {
				if livetable.CodeOf(err) != livetable.CodeClosed {
					t.Fatalf("detached attachment reopened: %v", err)
				}
				// A delayed run-prepare result cannot reactivate this incarnation.
				a.State = "active"
				rt.publishAttachment(a)
				if err := rt.authorize(livetable.Request{ClientID: projectAttachmentPrefix(a.Incarnation) + "worker", Operation: livetable.OpPush}); livetable.CodeOf(err) != livetable.CodeClosed {
					t.Fatalf("late preparation reopened attachment: %v", err)
				}
			} else if err != nil {
				t.Fatalf("new attachment dropped: %v", err)
			}
		})
	}
}

func TestProjectLiveTableCommitRejectsMissingCommandProvenance(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	svc := runner.projectLiveTableService()
	ctx := context.Background()
	rt, err := svc.acquire(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.release(rt)
	for _, client := range []string{"attachment/removed/worker", "operator/missing-context/command", "unrecognized"} {
		entry := livetable.Entry{TableID: d.CurrentInstanceID, Revision: 1, At: time.Now(), Payload: json.RawMessage(`{}`), Outcome: &livetable.CommittedOutcome{ClientID: client, Sequence: 1}}
		if err := svc.committer(rt).Commit(ctx, entry); !errors.Is(err, storage.ErrLiveTableFenced) {
			t.Fatalf("missing command provenance %q: %v", client, err)
		}
	}
	recovery, err := store.ReadProjectLiveTableRecovery(ctx, d.ProjectID, d.CurrentInstanceID)
	if err != nil || recovery.Instance.Revision != 0 || len(recovery.Entries) != 0 {
		t.Fatalf("unfenced command persisted: %+v %v", recovery, err)
	}
}
