package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

func hasProjectLiveTables(bindings []storage.ScenarioDataPoolBinding) bool {
	for _, b := range bindings {
		if b.AccessMode == "live" && b.Scope == "project" {
			return true
		}
	}
	return false
}
func (s *projectLiveTableService) attachRun(ctx context.Context, test *ManagedTest, run *runLiveTables, bindings []storage.ScenarioDataPoolBinding, restore bool) error {
	if !hasProjectLiveTables(bindings) {
		return nil
	}
	projectTerm, err := s.tr.captureProjectLiveTableTerm()
	if err != nil {
		return err
	}
	run.projectTerm = projectTerm
	store, err := s.store()
	if err != nil {
		return err
	}
	s.tr.mu.RLock()
	groups := append([]TestGroup(nil), test.Groups...)
	principal := firstNonEmpty(test.StartedBySubject, test.CreatedBySubject)
	s.tr.mu.RUnlock()
	if principal == "" {
		principal = "dev-mode"
	}
	retained, err := store.ListProjectLiveTableRunAttachments(ctx, run.projectID, run.testID)
	if err != nil {
		return err
	}
	existing := map[string]storage.ProjectLiveTableAttachment{}
	for _, a := range retained {
		if a.RunIncarnation != test.ID || a.State != "active" {
			return errors.New("project table attachment is no longer active")
		}
		existing[a.Group+"\x00"+a.Alias] = a
	}
	creates := []storage.ProjectLiveTableAttachmentCreate{}
	resolved := map[string]*projectLiveTableRuntime{}
	definitions := map[string]storage.ProjectLiveTableDefinition{}
	bindingsByAlias := map[string]storage.ScenarioDataPoolBinding{}
	var total int64
	recoveryBytes := run.recoveryBytes
	for _, b := range bindings {
		if b.Scope != "project" {
			continue
		}
		rt, err := s.acquire(ctx, run.projectID, b.ProjectTableID, "")
		if err != nil {
			return err
		}
		defer s.release(rt)
		if rt.term != run.projectTerm {
			return storage.ErrLiveTableFenced
		}
		rt.mu.Lock()
		d := rt.definition
		rt.mu.Unlock()
		if !restore && d.State != "active" {
			return errors.New("project live table is not accepting new attachments")
		}
		if b.SelectionStrategy == "lease" && d.Config.Profile != "native_queue" {
			return errors.New("column project tables do not support leases")
		}
		if _, ok := resolved[d.CurrentInstanceID]; !ok {
			total += liveTableReservation(projectTableOptions(d.Config))
			recoveryBytes += rt.recoveryBytes
			if restore && recoveryBytes > liveTableRecoveryBytes {
				return errors.New("project and run live tables exceed aggregate recovery byte budget")
			}
			resolved[d.CurrentInstanceID] = rt
		}
		definitions[b.Alias] = d
		bindingsByAlias[b.Alias] = b
	}
	if total+run.budget > liveTableRunBudget {
		return errors.New("project and run live tables exceed the aggregate run memory budget")
	}
	used := map[string]bool{}
	needsWrite := false
	for _, group := range groups {
		requirements, err := projectGroupRequirements(group.script, bindings)
		if err != nil {
			return err
		}
		for alias, ops := range requirements {
			b, ok := bindingsByAlias[alias]
			if !ok {
				continue
			}
			d := definitions[alias]
			key := group.Name + "\x00" + alias
			used[key] = true
			for _, op := range ops {
				if op == "create_column" {
					return errors.New("project live tables have a fixed schema; create_column requires a run-scoped binding")
				}
				needsWrite = needsWrite || op != "read" && op != "read_column"
			}
			if prior, ok := existing[key]; ok {
				if prior.TableID != d.ID || prior.InstanceID != d.CurrentInstanceID || prior.Principal != principal || !reflect.DeepEqual(prior.AllowedOperations, ops) {
					return errors.New("project attachment does not match immutable run requirements")
				}
				continue
			}
			if restore {
				return errors.New("project table recovery attachment manifest is incomplete")
			}
			creates = append(creates, storage.ProjectLiveTableAttachmentCreate{ExpectedDefinitionRevision: d.Revision, Attachment: storage.ProjectLiveTableAttachment{ID: "attachment-" + uuid.NewString(), Incarnation: uuid.NewString(), ProjectID: run.projectID, TableID: b.ProjectTableID, InstanceID: d.CurrentInstanceID, TestID: run.testID, RunIncarnation: test.ID, Group: group.Name, Alias: alias, AllowedOperations: ops, Principal: principal}})
		}
	}
	if needsWrite {
		if err := s.tr.authorizeLiveTablePersistence(test); err != nil {
			return fmt.Errorf("project live table mutation attachment: %w", err)
		}
	}
	if restore && len(used) != len(existing) {
		return errors.New("project table recovery includes unbound attachments")
	}
	added, err := store.CreateProjectLiveTableAttachments(ctx, creates, liveStorageTerm(run.projectTerm))
	if err != nil {
		return err
	}
	for _, a := range added {
		existing[a.Group+"\x00"+a.Alias] = a
	}
	run.projectAttachments = map[string]storage.ProjectLiveTableAttachment{}
	run.groupBindings = map[string][]engine.LiveTableBinding{}
	for key, a := range existing {
		if !used[key] {
			return errors.New("project table includes stale run attachments")
		}
		rt := resolved[a.InstanceID]
		rt.publishAttachment(a)
		run.projectAttachments[a.ID] = a
		run.groupBindings[a.Group] = append(run.groupBindings[a.Group], projectBindingEngine(bindingsByAlias[a.Alias], definitions[a.Alias], a))
	}
	return nil
}
func (s *projectLiveTableService) executeAttachment(ctx context.Context, run *runLiveTables, a storage.ProjectLiveTableAttachment, scope string, req livetable.Request) (livetable.Result, error) {
	if a.State != "active" || !strings.HasSuffix(scope, "/"+a.Group) || !s.tr.projectLiveTableTermCurrent(run.projectTerm) {
		return livetable.Result{}, storage.ErrLiveTableFenced
	}
	rt := s.cachedInstance(a.InstanceID, run.projectTerm)
	if rt != nil && rt.table.Stats().Unavailable {
		s.release(rt)
		rt = nil
	}
	if rt == nil {
		var err error
		rt, err = s.acquire(ctx, run.projectID, a.TableID, a.InstanceID)
		if err != nil {
			return livetable.Result{}, err
		}
	}
	defer s.release(rt)
	if rt.term != run.projectTerm {
		return livetable.Result{}, storage.ErrLiveTableFenced
	}
	run.mu.RLock()
	current, exists := run.projectAttachments[a.ID]
	serving := !run.closed && run.ready && exists && current.Incarnation == a.Incarnation && current.State == "active"
	run.mu.RUnlock()
	if !serving {
		return livetable.Result{}, storage.ErrLiveTableFenced
	}
	if len(req.ClientID) > 256 || len(req.OwnerID) > 256 || len(req.Cursor) > 256 {
		return livetable.Result{}, errors.New("invalid project table client identity")
	}
	if err := s.tr.validateLiveTablePolicy(run.projectID); err != nil {
		return livetable.Result{}, err
	}
	prefix := projectAttachmentPrefix(a.Incarnation)
	namespace := func(raw string) string {
		digest := sha256.Sum256([]byte(scope + "\x00" + raw))
		return prefix + hex.EncodeToString(digest[:])
	}
	req.ClientID = namespace(req.ClientID)
	if req.OwnerID == "" {
		req.OwnerID = req.ClientID
	} else {
		req.OwnerID = namespace(req.OwnerID)
	}
	// The unnamed sequential cursor is table-wide. Explicit cursor names are
	// private to this attachment and retire with its immutable owner scope.
	if req.Cursor != "" {
		digest := sha256.Sum256([]byte(req.Cursor))
		req.Cursor = prefix + hex.EncodeToString(digest[:])
	}
	result, err := rt.table.Execute(ctx, req)
	if !s.tr.projectLiveTableTermCurrent(run.projectTerm) {
		return livetable.Result{}, &livetable.Error{Code: livetable.CodeUnavailable, Message: "project table owner changed before response", OutcomeUnknown: req.Operation != livetable.OpRead && req.Operation != livetable.OpReadColumn}
	}
	_ = s.checkpoint(ctx, rt, false)
	s.tr.notifyLiveTableChange(run, time.Now())
	return result, err
}
func (s *projectLiveTableService) detachRun(ctx context.Context, run *runLiveTables) error {
	if len(run.projectAttachments) == 0 {
		return nil
	}
	store, err := s.store()
	if err != nil {
		return err
	}
	instances := map[string]*projectLiveTableRuntime{}
	retireAfter := time.Now()
	for _, a := range run.projectAttachments {
		if _, ok := instances[a.InstanceID]; ok {
			continue
		}
		rt, err := s.acquire(ctx, run.projectID, a.TableID, a.InstanceID)
		if err != nil {
			return err
		}
		defer s.release(rt)
		if rt.term != run.projectTerm {
			return storage.ErrLiveTableFenced
		}
		instances[a.InstanceID] = rt
		rt.mu.Lock()
		retry := rt.definition.Config.RetryWindowMS
		rt.mu.Unlock()
		retireAfter = maxTime(retireAfter, time.Now().Add(time.Duration(retry)*time.Millisecond))
	}
	// Revoke admission durably before settling core leases. The core's actor
	// authorization hook also rejects calls that passed the gateway earlier.
	if err := store.DetachProjectLiveTableAttachments(ctx, run.projectID, run.testID, retireAfter, liveStorageTerm(run.projectTerm)); err != nil {
		return err
	}
	for id, a := range run.projectAttachments {
		a.State = "detached"
		a.RetireAfter = &retireAfter
		run.projectAttachments[id] = a
		rt := instances[a.InstanceID]
		rt.publishAttachment(a)
		if err := rt.table.RevokeOwnerScope(ctx, projectAttachmentPrefix(a.Incarnation)); err != nil {
			return err
		}
		if err := store.MarkProjectLiveTableAttachmentSettled(ctx, a.ID, rt.table.Stats().Revision, liveStorageTerm(run.projectTerm)); err != nil {
			return err
		}
		if err := s.checkpoint(ctx, rt, true); err != nil {
			return err
		}
	}
	for _, rt := range instances {
		rt.mu.Lock()
		if rt.definition.State == "quiescing" {
			_, err = s.completeQuiesce(ctx, rt, "all run attachments detached")
		}
		rt.mu.Unlock()
		if err != nil {
			return err
		}
	}

	run.projectSummaries = projectLiveTableProvenanceLocked(s.tr, run)
	run.projectAttachments = nil
	return nil
}
func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
func (tr *TestRunner) liveBindingsForGroup(id, group string) []engine.LiveTableBinding {
	run := tr.liveRun(id)
	if run == nil {
		return nil
	}
	run.mu.RLock()
	defer run.mu.RUnlock()
	if !run.ready {
		return nil
	}
	out := append([]engine.LiveTableBinding(nil), run.bindings...)
	return append(out, run.groupBindings[group]...)
}
func (tr *TestRunner) liveBindingJSONForGroup(id, group string) []byte {
	bindings := tr.liveBindingsForGroup(id, group)
	if len(bindings) == 0 {
		return nil
	}
	body, _ := json.Marshal(bindings)
	return body
}

// Publication and snapshot reconciliation share a version fence. An immutable
// attachment cannot become active again once this actor has seen its detach.
func (rt *projectLiveTableRuntime) publishAttachment(a storage.ProjectLiveTableAttachment) {
	rt.attachmentsMu.Lock()
	defer rt.attachmentsMu.Unlock()
	if prior, ok := rt.attachments[a.Incarnation]; ok && prior.State == "detached" && a.State == "active" {
		return
	}
	rt.attachments[a.Incarnation] = a
	rt.attachmentsVersion++
}
