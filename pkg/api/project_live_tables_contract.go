package api

import (
	"context"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

const projectLiveTableServiceTimeout = 15 * time.Second

// HTTP adapters perform principal checks and supply value-free audit events.
// The service enforces current execution ownership and the table state machine.
type ProjectLiveTableCreateInput struct {
	ProjectID    string
	Name         string
	Config       storage.ProjectLiveTableConfig
	SeedPoolID   string
	ActorSubject string
	Audit        *storage.AuditEvent
}
type ProjectLiveTableLifecycleInput struct {
	ProjectID, TableID, Action, Reason string
	ExpectedDefinitionRevision         uint64
	ExpectedInstanceID                 string
	ExpectedRevision                   uint64
	Audit                              *storage.AuditEvent
}
type ProjectLiveTableResetInput struct {
	ProjectLiveTableLifecycleInput
	// A reset makes an empty new instance with the same fixed schema and limits.
	// Seeding a replacement requires a new definition and explicit review.
}
type projectLiveTableService struct{ tr *TestRunner }
type projectLiveTableRuntime struct {
	ready                     chan struct{}
	loadErr                   error
	projectID                 string
	instanceID                string
	pins                      int
	evicting                  bool
	mu                        sync.Mutex // serializes operator/lifecycle/inspection; never held for waits
	table                     *livetable.Table
	cipher                    *liveTableCipher
	term                      executionLeaseTerm
	definition                storage.ProjectLiveTableDefinition
	instance                  storage.ProjectLiveTableInstance
	attachmentsMu             sync.RWMutex
	attachmentsVersion        uint64
	attachments               map[string]storage.ProjectLiveTableAttachment
	checkpointMu              sync.Mutex
	lastCheckpoint            uint64
	pendingCheckpointRevision uint64
	pendingCheckpoint         []byte
	recoveryBytes             int
	budget                    int64
	lastUsed                  time.Time
}

func (tr *TestRunner) projectLiveTableService() *projectLiveTableService {
	return &projectLiveTableService{tr: tr}
}

// withProjectLiveTable passes the current immutable instance under its metadata
// lock. Callbacks may perform bounded core commands, not waits. They must not
// acquire the runner map lock or recursively enter the service.
func (s *projectLiveTableService) withProjectLiveTable(ctx context.Context, project, table, instance string, fn func(*projectLiveTableRuntime) error) error {
	rt, err := s.acquire(ctx, project, table, instance)
	if err != nil {
		return err
	}
	defer s.release(rt)
	if err := rt.lockMetadata(ctx); err != nil {
		return err
	}
	defer rt.mu.Unlock()
	return fn(rt)
}

// Metadata callers must not wait out other operators after their own deadline.
// The actor mutex remains separate so bounded engine commands and core waits
// retain their existing execution contract. No helper goroutine outlives a call.
func (rt *projectLiveTableRuntime) lockMetadata(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rt.mu.TryLock() {
		return nil
	}
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if err := ctx.Err(); err != nil {
				return err
			}
			if rt.mu.TryLock() {
				return nil
			}
		}
	}
}
