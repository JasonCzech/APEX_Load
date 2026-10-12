package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *projectLiveTableService) store() (storage.ProjectLiveTableStore, error) {
	v, ok := s.tr.dataPoolStore.(storage.ProjectLiveTableStore)
	if !ok {
		return nil, errors.New("project live table storage unavailable")
	}
	return v, nil
}
func projectTableAAD(id, kind string, revision uint64) string {
	return "project/" + liveTableAAD(id, kind, revision)
}
func (s *projectLiveTableService) pin(rt *projectLiveTableRuntime) bool {
	s.tr.liveTablesMu.Lock()
	defer s.tr.liveTablesMu.Unlock()
	if rt.evicting || s.tr.projectLiveTables[rt.instanceID] != rt {
		return false
	}
	rt.pins++
	return true
}

// cachedInstance pins the sole counted actor without doing a storage read.
// Runs retain only attachment metadata, never a second copy of shared raw state.
func (s *projectLiveTableService) cachedInstance(instance string, term executionLeaseTerm) *projectLiveTableRuntime {
	s.tr.liveTablesMu.Lock()
	defer s.tr.liveTablesMu.Unlock()
	rt := s.tr.projectLiveTables[instance]
	if rt == nil || rt.term != term || rt.evicting {
		return nil
	}
	select {
	case <-rt.ready:
	default:
		return nil
	}
	if rt.loadErr != nil {
		return nil
	}
	rt.pins++
	return rt
}
func (s *projectLiveTableService) release(rt *projectLiveTableRuntime) {
	s.tr.liveTablesMu.Lock()
	defer s.tr.liveTablesMu.Unlock()
	rt.pins--
	rt.lastUsed = time.Now()
}
func (s *projectLiveTableService) require(ctx context.Context, project string) (executionLeaseTerm, error) {
	if err := ctx.Err(); err != nil {
		return executionLeaseTerm{}, err
	}
	if err := s.tr.validateLiveTablePolicy(project); err != nil {
		return executionLeaseTerm{}, err
	}
	term, err := s.tr.captureProjectLiveTableTerm()
	if err != nil {
		return term, err
	}
	if !term.valid() || s.tr.continuityKeyWrapper == nil {
		return term, errors.New("project live tables require durable execution fencing and a wrapping key")
	}
	return term, nil
}
func (s *projectLiveTableService) List(ctx context.Context, project string) ([]storage.ProjectLiveTableDefinition, error) {
	ctx, cancel := context.WithTimeout(ctx, projectLiveTableServiceTimeout)
	defer cancel()
	if err := s.tr.validateLiveTablePolicy(project); err != nil {
		return nil, err
	}
	store, err := s.store()
	if err != nil {
		return nil, err
	}
	return store.ListProjectLiveTables(ctx, project)
}
func (s *projectLiveTableService) Get(ctx context.Context, project, id string) (*storage.ProjectLiveTableDefinition, error) {
	ctx, cancel := context.WithTimeout(ctx, projectLiveTableServiceTimeout)
	defer cancel()
	if err := s.tr.validateLiveTablePolicy(project); err != nil {
		return nil, err
	}
	store, err := s.store()
	if err != nil {
		return nil, err
	}
	return store.GetProjectLiveTable(ctx, project, id)
}
func (s *projectLiveTableService) Create(ctx context.Context, input ProjectLiveTableCreateInput) (*storage.ProjectLiveTableDefinition, error) {
	ctx, cancel := context.WithTimeout(ctx, projectLiveTableServiceTimeout)
	defer cancel()
	term, err := s.require(ctx, input.ProjectID)
	if err != nil {
		return nil, err
	}
	store, err := s.store()
	if err != nil {
		return nil, err
	}
	config, err := normalizeProjectTableConfig(input.Config)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 256 || input.ActorSubject == "" {
		return nil, errors.New("project table name and actor are required")
	}
	// Creation reserves against exactly the same aggregate budgets as recovery.
	budget := liveTableReservation(projectTableOptions(config))
	if err := s.reserve(input.ProjectID, budget); err != nil {
		return nil, err
	}
	defer s.unreserve(input.ProjectID, budget)
	id, instance := "project-table-"+uuid.NewString(), "project-instance-"+uuid.NewString()
	rows := []livetable.Row{}
	if input.SeedPoolID != "" {
		_, seeds, err := s.tr.liveStore()
		if err != nil {
			return nil, err
		}
		seed, err := seeds.ReadLiveDataPoolSeed(ctx, input.SeedPoolID, input.ProjectID, config.MaxRows, liveTableSeedBytes)
		if err != nil {
			return nil, err
		}
		for _, row := range seed.Rows {
			values := map[string]string{}
			for k, v := range row.Data {
				values[k] = fmt.Sprint(v)
			}
			rows = append(rows, livetable.Row{ID: row.ID, Version: uint64(max(1, row.Version)), Values: values})
		}
	}
	cipher, err := newLiveTableCipher(ctx, s.tr.continuityKeyWrapper, instance)
	if err != nil {
		return nil, err
	}
	table, err := livetable.New(livetable.Config{ID: instance, Scope: "project", ScopeID: input.ProjectID, Profile: livetable.Profile(config.Profile), Columns: config.Columns, Rows: rows, Limits: liveCoreLimits(projectTableOptions(config))})
	if err != nil {
		return nil, err
	}
	snapshot, err := table.Snapshot()
	if err != nil {
		return nil, err
	}
	sealed, err := cipher.seal(snapshot, projectTableAAD(instance, "checkpoint", 0))
	if err != nil {
		return nil, err
	}
	metadata, err := json.Marshal(liveTablePrivateMetadata{Version: 1, Key: cipher.wrapped})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(snapshot.Payload)
	d := storage.ProjectLiveTableDefinition{ID: id, ProjectID: input.ProjectID, Name: strings.TrimSpace(input.Name), CurrentInstanceID: instance, Config: config, RetentionDays: 7, CreatedBySubject: input.ActorSubject}
	i := storage.ProjectLiveTableInstance{ID: instance, TableID: id, ProjectID: input.ProjectID, SeedDigest: hex.EncodeToString(digest[:]), SeedMetadata: string(metadata)}
	var audit *storage.AuditEvent
	if input.Audit != nil {
		copy := *input.Audit
		copy.ResourceID = id
		copy.ProjectID = input.ProjectID
		audit = &copy
	}
	return store.CreateProjectLiveTable(ctx, storage.ProjectLiveTableCreate{Definition: d, Instance: i, Checkpoint: sealed, Audit: audit}, liveStorageTerm(term))
}
func (s *projectLiveTableService) reserve(project string, budget int64) error {
	s.tr.liveTablesMu.Lock()
	defer s.tr.liveTablesMu.Unlock()
	if err := s.tr.liveTableBudgetError(project, budget, ""); err != nil {
		return err
	}
	s.tr.liveTablesRecoveryReserved += budget
	if s.tr.liveTablesRecoveryByProject == nil {
		s.tr.liveTablesRecoveryByProject = map[string]int64{}
	}
	s.tr.liveTablesRecoveryByProject[project] += budget
	return nil
}
func (s *projectLiveTableService) unreserve(project string, budget int64) {
	s.tr.liveTablesMu.Lock()
	defer s.tr.liveTablesMu.Unlock()
	s.tr.liveTablesRecoveryReserved -= budget
	s.tr.liveTablesRecoveryByProject[project] -= budget
	if s.tr.liveTablesRecoveryByProject[project] == 0 {
		delete(s.tr.liveTablesRecoveryByProject, project)
	}
}
func (s *projectLiveTableService) acquire(ctx context.Context, project, id, expected string) (*projectLiveTableRuntime, error) {
	term, err := s.require(ctx, project)
	if err != nil {
		return nil, err
	}
	store, err := s.store()
	if err != nil {
		return nil, err
	}
	d, err := store.GetProjectLiveTable(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if expected != "" && expected != d.CurrentInstanceID {
		return nil, storage.ErrLiveTableConflict
	}
	if d.State == "archived" {
		return nil, storage.ErrLiveTableClosed
	}
	s.tr.liveTablesMu.Lock()
	if prior := s.tr.projectLiveTables[d.CurrentInstanceID]; prior != nil && prior.term != term {
		ready := false
		select {
		case <-prior.ready:
			ready = true
		default:
		}
		if !ready || prior.pins != 0 {
			s.tr.liveTablesMu.Unlock()
			return nil, errors.New("project table prior owner is draining")
		}
		prior.evicting = true
		delete(s.tr.projectLiveTables, d.CurrentInstanceID)
	}
	if rt := s.tr.projectLiveTables[d.CurrentInstanceID]; rt != nil && rt.term == term {
		if rt.evicting {
			s.tr.liveTablesMu.Unlock()
			return nil, errors.New("project live table is checkpointing for idle release")
		}
		ready := rt.ready
		s.tr.liveTablesMu.Unlock()
		select {
		case <-ready:
			if rt.loadErr != nil {
				return nil, rt.loadErr
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if err := rt.lockMetadata(ctx); err != nil {
			return nil, err
		}
		// A lifecycle command may have committed while this acquisition waited
		// for the metadata lock. A previously read definition must not undo it.
		if d.Revision >= rt.definition.Revision {
			rt.definition = *d
		}
		rt.mu.Unlock()
		if !rt.table.Stats().Unavailable {
			if !s.pin(rt) {
				return nil, errors.New("project table changed during acquisition")
			}
			return rt, nil
		}
		// An ambiguous commit permanently poisons this actor. Once all calls
		// drain it can be replaced from authoritative storage, even in the
		// same owner epoch; it can never serve alongside the replacement.
		s.tr.liveTablesMu.Lock()
		if s.tr.projectLiveTables[rt.instanceID] != rt || rt.pins != 0 {
			s.tr.liveTablesMu.Unlock()
			return nil, errors.New("project table recovery is waiting for in-flight operations")
		}
		rt.evicting = true
		delete(s.tr.projectLiveTables, rt.instanceID)
		s.tr.liveTablesMu.Unlock()
		return s.acquire(ctx, project, id, expected)
	}
	s.tr.liveTablesMu.Unlock()
	// Reserve before any decrypt/replay allocation. Other tables remain available.
	budget := liveTableReservation(projectTableOptions(d.Config))
	if err := s.reserve(project, budget); err != nil {
		// Only unpinned, unattached actors are eligible for pressure eviction.
		if cleanupErr := s.evictIdle(ctx, true); cleanupErr != nil {
			return nil, errors.Join(err, cleanupErr)
		}
		if err = s.reserve(project, budget); err != nil {
			return nil, err
		}
	}
	defer s.unreserve(project, budget)
	rt := &projectLiveTableRuntime{term: term, definition: *d, budget: 0, projectID: project, instanceID: d.CurrentInstanceID, ready: make(chan struct{}), attachments: map[string]storage.ProjectLiveTableAttachment{}, lastUsed: time.Now()}
	s.tr.liveTablesMu.Lock()
	if existing := s.tr.projectLiveTables[d.CurrentInstanceID]; existing != nil && existing.term == term {
		s.tr.liveTablesMu.Unlock()
		return s.acquire(ctx, project, id, expected)
	}
	if s.tr.projectLiveTables == nil {
		s.tr.projectLiveTables = map[string]*projectLiveTableRuntime{}
	}
	s.tr.projectLiveTables[d.CurrentInstanceID] = rt
	s.tr.liveTablesMu.Unlock()
	// The reservation lives in the recovery counters until ready; budget zero on
	// the map entry avoids charging it twice during that bounded interval.
	loadErr := func() error {
		if err := store.ClaimProjectLiveTableInstance(ctx, project, d.CurrentInstanceID, liveStorageTerm(term)); err != nil {
			return err
		}
		recovery, err := store.ReadProjectLiveTableRecovery(ctx, project, d.CurrentInstanceID)
		if err != nil {
			return err
		}
		rt.instance = recovery.Instance
		bytes := len(recovery.Checkpoint) + len(recovery.Instance.SeedMetadata)
		for _, e := range recovery.Entries {
			bytes += len(e.Payload)
		}
		if bytes > liveTableRecoveryBytes {
			return errors.New("project table recovery byte budget exceeded")
		}
		rt.recoveryBytes = bytes
		var metadata liveTablePrivateMetadata
		if err := json.Unmarshal([]byte(recovery.Instance.SeedMetadata), &metadata); err != nil || metadata.Version != 1 {
			return errors.New("invalid project table recovery metadata")
		}
		rt.cipher, err = restoreLiveTableCipher(ctx, s.tr.continuityKeyWrapper, recovery.Instance.ID, metadata.Key)
		if err != nil {
			return err
		}
		var snapshot livetable.Snapshot
		if err := rt.cipher.open(recovery.Checkpoint, projectTableAAD(recovery.Instance.ID, "checkpoint", recovery.Instance.CheckpointRevision), &snapshot); err != nil {
			return err
		}
		if snapshot.Scope != "project" || snapshot.ScopeID != project || snapshot.TableID != recovery.Instance.ID {
			return errors.New("project table snapshot identity mismatch")
		}
		if err := validateProjectTableSnapshot(snapshot, d.Config); err != nil {
			return err
		}
		rt.table, err = livetable.Restore(snapshot, s.committer(rt))
		if err != nil {
			return err
		}
		for _, stored := range recovery.Entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			var e livetable.Entry
			if err := rt.cipher.open(stored.Payload, projectTableAAD(stored.TableID, "entry", stored.Revision), &e); err != nil {
				return err
			}
			if err := rt.table.Replay(e); err != nil {
				return err
			}
		}
		stats := rt.table.Stats()
		if stats.Closed || stats.Counts.Leased != recovery.Instance.LeaseCount {
			return errors.New("project table lifecycle witness mismatch")
		}
		rt.lastCheckpoint = recovery.Instance.CheckpointRevision
		attachments, err := store.ListProjectLiveTableAttachments(ctx, project, id)
		if err != nil {
			return err
		}
		for _, a := range attachments {
			if a.InstanceID == rt.instance.ID {
				rt.attachments[a.Incarnation] = a
			}
		}
		rt.table.SetAuthorizer(rt.authorize)
		return nil
	}()
	s.tr.liveTablesMu.Lock()
	rt.loadErr = loadErr
	if loadErr == nil {
		rt.budget = budget
		rt.pins = 1
	} else {
		delete(s.tr.projectLiveTables, d.CurrentInstanceID)
	}
	close(rt.ready)
	s.tr.liveTablesMu.Unlock()
	if loadErr != nil {
		return nil, loadErr
	}
	return rt, nil
}
func (rt *projectLiveTableRuntime) authorize(r livetable.Request) error {
	if r.Operation == livetable.OpCreateColumn {
		return &livetable.Error{Code: livetable.CodeInvalid, Message: "project live tables have a fixed schema"}
	}
	if strings.HasPrefix(r.ClientID, "operator/") {
		return nil
	} // only server-created local commands have this prefix
	rt.attachmentsMu.RLock()
	defer rt.attachmentsMu.RUnlock()
	for incarnation, a := range rt.attachments {
		prefix := projectAttachmentPrefix(incarnation)
		if strings.HasPrefix(r.ClientID, prefix) {
			if a.State != "active" {
				return &livetable.Error{Code: livetable.CodeClosed, Message: "project table attachment detached"}
			}
			for _, op := range a.AllowedOperations {
				if op == string(r.Operation) {
					return nil
				}
			}
			return &livetable.Error{Code: livetable.CodeInvalid, Message: "operation is outside this attachment's admitted requirements"}
		}
	}
	return &livetable.Error{Code: livetable.CodeClosed, Message: "project table attachment is unavailable"}
}
func projectAttachmentPrefix(incarnation string) string { return "attachment/" + incarnation + "/" }
func (s *projectLiveTableService) committer(rt *projectLiveTableRuntime) livetable.Committer {
	return livetable.CommitFunc(func(ctx context.Context, e livetable.Entry) error {
		if !s.tr.projectLiveTableTermCurrent(rt.term) {
			return storage.ErrLiveTableFenced
		}
		store, err := s.store()
		if err != nil {
			return err
		}
		sealed, err := rt.cipher.seal(e, projectTableAAD(e.TableID, "entry", e.Revision))
		if err != nil {
			return err
		}
		entry := storage.ProjectLiveTableEntry{LiveTableEntry: storage.LiveTableEntry{TableID: e.TableID, Revision: e.Revision, PreviousRevision: e.PreviousRevision, At: e.At, Payload: sealed}, LeasedCount: e.Counts.Leased}
		if e.Outcome != nil {
			rt.attachmentsMu.RLock()
			for incarnation, a := range rt.attachments {
				if strings.HasPrefix(e.Outcome.ClientID, projectAttachmentPrefix(incarnation)) {
					entry.AttachmentID = a.ID
					break
				}
			}
			rt.attachmentsMu.RUnlock()
		}
		if handled, err := tryCommitProjectLiveTableOperator(ctx, entry, e.Outcome); handled || err != nil {
			return err
		}
		// A missing attachment must never turn a user command into the trusted
		// maintenance path. Operator commands require their exact audit context.
		if e.Outcome != nil && entry.AttachmentID == "" {
			return storage.ErrLiveTableFenced
		}
		return store.AppendProjectLiveTableEntry(ctx, entry, liveStorageTerm(rt.term))
	})
}
func (s *projectLiveTableService) checkpoint(ctx context.Context, rt *projectLiveTableRuntime, force bool) error {
	if !rt.checkpointMu.TryLock() {
		if !force {
			return nil
		}
		rt.checkpointMu.Lock()
	}
	defer rt.checkpointMu.Unlock()
	stats := rt.table.Stats()
	if stats.Revision == rt.lastCheckpoint || !force && stats.Revision-rt.lastCheckpoint < 8 {
		return nil
	}
	snapshot, err := rt.table.Snapshot()
	if err != nil {
		return err
	}
	payload := rt.pendingCheckpoint
	if payload == nil || rt.pendingCheckpointRevision != snapshot.Revision {
		payload, err = rt.cipher.seal(snapshot, projectTableAAD(rt.instance.ID, "checkpoint", snapshot.Revision))
		if err != nil {
			return err
		}
		rt.pendingCheckpointRevision, rt.pendingCheckpoint = snapshot.Revision, payload
	}
	store, err := s.store()
	if err != nil {
		return err
	}
	if err := store.PublishProjectLiveTableCheckpoint(ctx, rt.instance.ID, snapshot.Revision, payload, liveStorageTerm(rt.term)); err != nil {
		return err
	}
	rt.lastCheckpoint = snapshot.Revision
	rt.pendingCheckpoint = nil
	return nil
}
func (s *projectLiveTableService) Inspect(ctx context.Context, project, id, instance string, revision uint64, offset, limit int) (livetable.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, projectLiveTableServiceTimeout)
	defer cancel()
	d, err := s.Get(ctx, project, id)
	if err != nil {
		return livetable.Result{}, err
	}
	if d.State == "archived" || instance != d.CurrentInstanceID {
		return s.inspectRetained(ctx, project, id, instance, revision, offset, limit)
	}
	var out livetable.Result
	err = s.withProjectLiveTable(ctx, project, id, instance, func(rt *projectLiveTableRuntime) error {
		var err error
		out, err = rt.table.Inspect(offset, limit, revision)
		if err == nil && out.Revision != revision {
			return storage.ErrLiveTableConflict
		}
		return err
	})
	return out, err
}
func (s *projectLiveTableService) ChangeLifecycle(ctx context.Context, input ProjectLiveTableLifecycleInput) (*storage.ProjectLiveTableDefinition, error) {
	ctx, cancel := context.WithTimeout(ctx, projectLiveTableServiceTimeout)
	defer cancel()
	if input.Action == "reset" {
		return s.Reset(ctx, ProjectLiveTableResetInput{input})
	}
	var out *storage.ProjectLiveTableDefinition
	err := s.withProjectLiveTable(ctx, input.ProjectID, input.TableID, input.ExpectedInstanceID, func(rt *projectLiveTableRuntime) error {
		store, err := s.store()
		if err != nil {
			return err
		}
		state := input.Action
		switch state {
		case "quiesce":
			state = "quiescing"
		case "resume":
			state = "active"
		case "archive":
			state = "archived"
		default:
			return errors.New("unsupported project table lifecycle action")
		}
		out, err = store.TransitionProjectLiveTable(ctx, storage.ProjectLiveTableLifecycle{ProjectID: input.ProjectID, TableID: input.TableID, ExpectedDefinitionRevision: input.ExpectedDefinitionRevision, ExpectedInstanceID: input.ExpectedInstanceID, ExpectedRevision: input.ExpectedRevision, State: state, Reason: input.Reason, Audit: input.Audit}, liveStorageTerm(rt.term))
		if err != nil {
			return err
		}
		rt.definition = *out
		if state == "archived" {
			rt.instance.State = "retired"
		}
		if state == "quiescing" {
			out, err = s.completeQuiesce(ctx, rt, input.Reason)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}
func (s *projectLiveTableService) completeQuiesce(ctx context.Context, rt *projectLiveTableRuntime, reason string) (*storage.ProjectLiveTableDefinition, error) {
	rt.attachmentsMu.RLock()
	active := false
	for _, a := range rt.attachments {
		active = active || a.State == "active"
	}
	rt.attachmentsMu.RUnlock()
	if active || rt.table.Stats().Counts.Leased > 0 {
		return &rt.definition, nil
	}
	store, err := s.store()
	if err != nil {
		return nil, err
	}
	d, err := store.TransitionProjectLiveTable(ctx, storage.ProjectLiveTableLifecycle{ProjectID: rt.definition.ProjectID, TableID: rt.definition.ID, ExpectedDefinitionRevision: rt.definition.Revision, ExpectedInstanceID: rt.instance.ID, ExpectedRevision: rt.table.Stats().Revision, State: "quiesced", Reason: reason, Audit: &storage.AuditEvent{ID: uuid.NewString(), Action: "project_live_table.quiesced", ActorType: "system", ActorSubject: "project-table-lifecycle", ProjectID: rt.projectID, ResourceType: "project_live_table", ResourceID: rt.definition.ID, Status: "success", Details: map[string]any{"instance_id": rt.instanceID, "reason": "attachments_settled"}, CreatedAt: time.Now().UTC()}}, liveStorageTerm(rt.term))
	if err != nil {
		return nil, err
	}
	rt.definition = *d
	return d, nil
}
func (s *projectLiveTableService) Reset(ctx context.Context, input ProjectLiveTableResetInput) (*storage.ProjectLiveTableDefinition, error) {
	ctx, cancel := context.WithTimeout(ctx, projectLiveTableServiceTimeout)
	defer cancel()
	var out *storage.ProjectLiveTableDefinition
	err := s.withProjectLiveTable(ctx, input.ProjectID, input.TableID, input.ExpectedInstanceID, func(rt *projectLiveTableRuntime) error {
		store, err := s.store()
		if err != nil {
			return err
		}
		if err := s.checkpoint(ctx, rt, true); err != nil {
			return err
		}
		id := "project-instance-" + uuid.NewString()
		cipher, err := newLiveTableCipher(ctx, s.tr.continuityKeyWrapper, id)
		if err != nil {
			return err
		}
		table, err := livetable.New(livetable.Config{ID: id, Scope: "project", ScopeID: input.ProjectID, Profile: livetable.Profile(rt.definition.Config.Profile), Columns: rt.definition.Config.Columns, Limits: liveCoreLimits(projectTableOptions(rt.definition.Config))})
		if err != nil {
			return err
		}
		snapshot, err := table.Snapshot()
		if err != nil {
			return err
		}
		sealed, err := cipher.seal(snapshot, projectTableAAD(id, "checkpoint", 0))
		if err != nil {
			return err
		}
		metadata, err := json.Marshal(liveTablePrivateMetadata{Version: 1, Key: cipher.wrapped})
		if err != nil {
			return err
		}
		digest := sha256.Sum256(snapshot.Payload)
		out, err = store.ReplaceProjectLiveTableInstance(ctx, storage.ProjectLiveTableReset{ProjectLiveTableLifecycle: storage.ProjectLiveTableLifecycle{ProjectID: input.ProjectID, TableID: input.TableID, ExpectedDefinitionRevision: input.ExpectedDefinitionRevision, ExpectedInstanceID: input.ExpectedInstanceID, ExpectedRevision: input.ExpectedRevision, Reason: input.Reason, Audit: input.Audit}, Instance: storage.ProjectLiveTableInstance{ID: id, TableID: input.TableID, ProjectID: input.ProjectID, SeedDigest: hex.EncodeToString(digest[:]), SeedMetadata: string(metadata)}, Checkpoint: sealed}, liveStorageTerm(rt.term))
		if err == nil {
			rt.definition = *out
			rt.instance.State = "retired"
		}
		return err
	})
	return out, err
}

func projectGroupRequirements(script *dsl.Script, bindings []storage.ScenarioDataPoolBinding) (map[string][]string, error) {
	if _, err := dsl.ScriptLiveTableAliases(script); err != nil {
		return nil, err
	}
	sets := map[string]map[string]bool{}
	add := func(alias, op string) {
		if sets[alias] == nil {
			sets[alias] = map[string]bool{}
		}
		sets[alias][op] = true
	}
	native, err := dsl.ScriptNativeServiceRequirements(script)
	if err != nil {
		return nil, err
	}
	for _, req := range native {
		for _, op := range req.Operations {
			add(req.Alias, op)
		}
	}
	var walk func([]dsl.Action)
	walk = func(actions []dsl.Action) {
		for _, a := range actions {
			if dsl.CanonicalActionType(a.Type) == "live_table" {
				parsed, _ := dsl.ParseLiveTableAction(a.Params)
				add(parsed.Alias, parsed.Operation)
			}
			if a.Control != nil {
				for _, branch := range a.Control.ActionBranches() {
					walk(branch.Actions)
				}
			}
		}
	}
	if script != nil {
		walk(script.Init)
		walk(script.Actions)
		walk(script.Teardown)
	}
	for _, b := range bindings {
		if b.Scope == "project" && b.SelectionStrategy != "manual" {
			add(b.Alias, b.SelectionStrategy)
			if b.SelectionStrategy == "lease" {
				add(b.Alias, "ack")
			}
		}
	}
	out := map[string][]string{}
	for alias, ops := range sets {
		if ops["lease"] {
			ops["renew"] = true
			ops["release"] = true
		}
		for op := range ops {
			out[alias] = append(out[alias], op)
		}
		sort.Strings(out[alias])
	}
	return out, nil
}
func projectBindingEngine(b storage.ScenarioDataPoolBinding, d storage.ProjectLiveTableDefinition, a storage.ProjectLiveTableAttachment) engine.LiveTableBinding {
	options := projectTableOptions(d.Config)
	options.EmptyPolicy = b.LiveOptions.EmptyPolicy
	options.WaitMS = b.LiveOptions.WaitMS
	options.OperationTimeoutMS = b.LiveOptions.OperationTimeoutMS
	b.LiveOptions = options
	return liveEngineBinding(b, a.ID, d.Config.Columns)
}

// The immutable definition is the admission budget. An encrypted but mismatched
// checkpoint must not enlarge those limits when it is restored.
func validateProjectTableSnapshot(snapshot livetable.Snapshot, config storage.ProjectLiveTableConfig) error {
	var header struct {
		Profile livetable.Profile `json:"profile"`
		Columns []string          `json:"columns"`
		Limits  livetable.Limits  `json:"limits"`
	}
	if err := json.Unmarshal(snapshot.Payload, &header); err != nil {
		return storage.ErrLiveTableCorrupt
	}
	if header.Profile != livetable.Profile(config.Profile) || !reflect.DeepEqual(header.Columns, config.Columns) || header.Limits != liveCoreLimits(projectTableOptions(config)) {
		return storage.ErrLiveTableCorrupt
	}
	return nil
}
