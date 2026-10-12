package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

type projectDeadlineStore struct {
	*storage.SQLiteStore
	deadline chan time.Time
	failFast bool
}

func (s *projectDeadlineStore) block(ctx context.Context) error {
	deadline, _ := ctx.Deadline()
	s.deadline <- deadline
	if s.failFast {
		return context.Canceled
	}
	<-ctx.Done()
	return ctx.Err()
}
func (s *projectDeadlineStore) GetProjectLiveTable(ctx context.Context, _, _ string) (*storage.ProjectLiveTableDefinition, error) {
	return nil, s.block(ctx)
}
func (s *projectDeadlineStore) ListProjectLiveTables(ctx context.Context, _ string) ([]storage.ProjectLiveTableDefinition, error) {
	return nil, s.block(ctx)
}
func (s *projectDeadlineStore) CreateProjectLiveTable(ctx context.Context, _ storage.ProjectLiveTableCreate, _ storage.LiveTableExecutionTerm) (*storage.ProjectLiveTableDefinition, error) {
	return nil, s.block(ctx)
}

func TestProjectLiveTableServiceBoundsStorageCalls(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	svc := runner.projectLiveTableService()
	for _, name := range []string{"list", "get", "create", "inspect", "lifecycle", "reset", "stats", "export", "operator", "shorter caller"} {
		t.Run(name, func(t *testing.T) {
			wrapper := &projectDeadlineStore{SQLiteStore: store, deadline: make(chan time.Time, 1)}
			runner.dataPoolStore = wrapper
			defer func() { runner.dataPoolStore = store }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var callerDeadline time.Time
			if name == "shorter caller" {
				callerDeadline = time.Now().Add(time.Second)
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithDeadline(ctx, callerDeadline)
				defer deadlineCancel()
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch name {
				case "list":
					_, err = svc.List(ctx, d.ProjectID)
				case "get", "shorter caller":
					_, err = svc.Get(ctx, d.ProjectID, d.ID)
				case "create":
					_, err = svc.Create(ctx, ProjectLiveTableCreateInput{ProjectID: d.ProjectID, Name: "bounded", Config: d.Config, ActorSubject: "dev-mode"})
				case "inspect":
					_, err = svc.Inspect(ctx, d.ProjectID, d.ID, d.CurrentInstanceID, 0, 0, 1)
				case "lifecycle":
					_, err = svc.ChangeLifecycle(ctx, ProjectLiveTableLifecycleInput{ProjectID: d.ProjectID, TableID: d.ID, Action: "quiesce", ExpectedInstanceID: d.CurrentInstanceID})
				case "reset":
					_, err = svc.Reset(ctx, ProjectLiveTableResetInput{ProjectLiveTableLifecycleInput{ProjectID: d.ProjectID, TableID: d.ID, ExpectedInstanceID: d.CurrentInstanceID}})
				case "stats":
					_, err = svc.Stats(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
				case "export":
					err = svc.WithInspection(ctx, d.ProjectID, d.ID, d.CurrentInstanceID, func(context.Context, *livetable.Table) error { return nil })
				case "operator":
					_, err = svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "dev-mode", ProjectLiveTableMutation{ExpectedInstanceID: d.CurrentInstanceID}, &storage.AuditEvent{ActorSubject: "dev-mode"})
				}
				done <- err
			}()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
						t.Errorf("canceled storage call returned %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Error("bounded service worker did not exit")
				}
			}()
			select {
			case deadline := <-wrapper.deadline:
				if remaining := time.Until(deadline); deadline.IsZero() || remaining <= 0 || remaining > projectLiveTableServiceTimeout {
					t.Fatalf("storage received unbounded deadline: %v", deadline)
				}
				if !callerDeadline.IsZero() && !deadline.Equal(callerDeadline) {
					t.Fatalf("service extended caller deadline: %v != %v", deadline, callerDeadline)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("service did not reach context-aware storage")
			}
		})
	}
}

func TestProjectLiveTableServiceCancelsWhileMetadataBusy(t *testing.T) {
	runner, _, d := newProjectTableFixture(t)
	svc := runner.projectLiveTableService()
	rt, err := svc.acquire(context.Background(), d.ProjectID, d.ID, d.CurrentInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.release(rt)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := svc.Inspect(ctx, d.ProjectID, d.ID, d.CurrentInstanceID, 0, 0, 1)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("busy metadata returned %v", err)
		}
	case <-time.After(time.Second):
		// Unlock before joining even when the regression fails, so the worker
		// cannot outlive this test or race fixture shutdown.
		rt.mu.Unlock()
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("metadata worker did not exit after unlock")
		}
		rt.mu.Lock()
		t.Fatal("metadata acquisition ignored caller deadline")
	}
}

func TestProjectLiveTableAuthoringBoundsReferenceRead(t *testing.T) {
	_, store, d := newProjectTableFixture(t)
	wrapper := &projectDeadlineStore{SQLiteStore: store, deadline: make(chan time.Time, 1), failFast: true}
	err := validateProjectLiveTableReference(wrapper, d.ProjectID, storage.ScenarioDataPoolBinding{Scope: "project", ProjectTableID: d.ID, AccessMode: "live"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("reference error not preserved: %v", err)
	}
	deadline := <-wrapper.deadline
	if remaining := time.Until(deadline); deadline.IsZero() || remaining <= 0 || remaining > projectLiveTableServiceTimeout {
		t.Fatalf("authoring reference read has unbounded deadline: %v", deadline)
	}
}

type projectDetailDeadlineStore struct {
	*storage.SQLiteStore
	deadline chan time.Time
}

func (s *projectDetailDeadlineStore) ListProjectLiveTableAttachments(ctx context.Context, _, _ string) ([]storage.ProjectLiveTableAttachment, error) {
	deadline, _ := ctx.Deadline()
	s.deadline <- deadline
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestProjectLiveTableDetailBoundsDirectAttachmentRead(t *testing.T) {
	server, store, d := newProjectOperatorServer(t)
	for _, shorter := range []bool{false, true} {
		name := "service budget"
		if shorter {
			name = "shorter caller"
		}
		t.Run(name, func(t *testing.T) {
			wrapper := &projectDetailDeadlineStore{SQLiteStore: store, deadline: make(chan time.Time, 1)}
			server.store = wrapper
			defer func() { server.store = store }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var callerDeadline time.Time
			if shorter {
				callerDeadline = time.Now().Add(time.Second)
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithDeadline(ctx, callerDeadline)
				defer deadlineCancel()
			}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+d.ProjectID+"/live-tables/"+d.ID, nil).WithContext(ctx)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); server.mux.ServeHTTP(response, req) }()
			defer func() {
				cancel()
				select {
				case <-done:
					if response.Code == http.StatusOK {
						t.Error("canceled attachment read emitted success")
					}
				case <-time.After(3 * time.Second):
					t.Error("detail handler did not stop on canceled storage")
				}
			}()
			select {
			case deadline := <-wrapper.deadline:
				if remaining := time.Until(deadline); deadline.IsZero() || remaining <= 0 || remaining > projectLiveTableServiceTimeout {
					t.Fatalf("direct attachment read has unbounded deadline: %v", deadline)
				}
				if shorter && !deadline.Equal(callerDeadline) {
					t.Fatalf("handler extended caller deadline: %v", deadline)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("detail handler did not reach attachment read")
			}
		})
	}
}
