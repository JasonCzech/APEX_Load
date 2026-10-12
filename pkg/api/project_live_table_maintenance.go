package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

type projectTableMaintenanceStore interface {
	ListProjectLiveTableMaintenance(context.Context, string, int) ([]storage.ProjectLiveTableDefinition, error)
	DetachOrphanProjectLiveTableAttachments(context.Context, string, string, storage.LiveTableExecutionTerm) error
	PruneProjectLiveTableInstances(context.Context, string, storage.LiveTableExecutionTerm) (int, error)
}

// sweep shares the controller's existing leader-gated cleanup cadence. Each
// pass has a ten-second total deadline, at most sixteen definitions and sixty-
// four tombstones per instance. Failed scopes remain durable for the next pass.
func (s *projectLiveTableService) sweep(ctx context.Context) error {
	maintenance, ok := s.tr.dataPoolStore.(projectTableMaintenanceStore)
	if !ok || s.tr.continuityKeyWrapper == nil {
		return nil
	}
	if !s.tr.projectLiveMaintenanceMu.TryLock() {
		return nil
	}
	defer s.tr.projectLiveMaintenanceMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	term, err := s.tr.captureProjectLiveTableTerm()
	if err != nil {
		return err
	}
	defs, err := maintenance.ListProjectLiveTableMaintenance(ctx, s.tr.projectLiveMaintenanceCursor, 16)
	if err != nil {
		return err
	}
	if len(defs) == 0 {
		s.tr.projectLiveMaintenanceCursor = ""
	}
	var failures []error
	for _, d := range defs {
		s.tr.projectLiveMaintenanceCursor = d.ID
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := maintenance.DetachOrphanProjectLiveTableAttachments(ctx, d.ProjectID, d.ID, liveStorageTerm(term)); err != nil {
			failures = append(failures, err)
			continue
		}
		if d.State != "archived" {
			rt, err := s.acquire(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
			if err == nil {
				err = s.reconcileAttachments(ctx, rt)
				s.release(rt)
			}
			if err != nil {
				failures = append(failures, err)
			}
		}
		if _, err := maintenance.PruneProjectLiveTableInstances(ctx, d.ProjectID, liveStorageTerm(term)); err != nil {
			failures = append(failures, err)
		}
	}
	if err := s.evictIdle(ctx, false); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
func (s *projectLiveTableService) reconcileAttachments(ctx context.Context, rt *projectLiveTableRuntime) error {
	store, err := s.store()
	if err != nil {
		return err
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.attachmentsMu.RLock()
	version := rt.attachmentsVersion
	rt.attachmentsMu.RUnlock()
	attachments, err := store.ListProjectLiveTableAttachments(ctx, rt.projectID, rt.definition.ID)
	if err != nil {
		return err
	}
	rt.attachmentsMu.Lock()
	if rt.attachmentsVersion != version {
		rt.attachmentsMu.Unlock()
		// A run published an attachment or revocation during this database read.
		// None of this stale enumeration may drive authorization or retirement.
		return nil
	}
	rt.attachmentsVersion++
	rt.attachments = map[string]storage.ProjectLiveTableAttachment{}
	for _, a := range attachments {
		if a.InstanceID == rt.instanceID {
			rt.attachments[a.Incarnation] = a
		}
	}
	rt.attachmentsMu.Unlock()
	count := 0
	for _, a := range attachments {
		if a.InstanceID != rt.instanceID || a.State != "detached" {
			continue
		}
		if a.SettledRevision != nil && (a.RetireAfter == nil || time.Now().Before(*a.RetireAfter)) {
			continue // retained receipts need no work; do not starve later tombstones
		}
		if count >= 64 {
			break
		}
		count++
		if err := rt.table.RevokeOwnerScope(ctx, projectAttachmentPrefix(a.Incarnation)); err != nil {
			return err
		}
		if err := store.MarkProjectLiveTableAttachmentSettled(ctx, a.ID, rt.table.Stats().Revision, liveStorageTerm(rt.term)); err != nil {
			return err
		}
		if a.RetireAfter != nil && !time.Now().Before(*a.RetireAfter) {
			if err := rt.table.RetireOwnerScope(ctx, projectAttachmentPrefix(a.Incarnation)); err != nil {
				return err
			}
			if err := store.RetireProjectLiveTableAttachment(ctx, a.ID, rt.table.Stats().Revision, liveStorageTerm(rt.term)); err != nil {
				return err
			}
			rt.attachmentsMu.Lock()
			delete(rt.attachments, a.Incarnation)
			rt.attachmentsVersion++
			rt.attachmentsMu.Unlock()
		}
	}
	if rt.definition.State == "quiescing" {
		if _, err := s.completeQuiesce(ctx, rt, "all run attachments detached"); err != nil {
			return err
		}
	}
	return s.checkpoint(ctx, rt, true)
}
func (s *projectLiveTableService) evictIdle(ctx context.Context, force bool) error {
	// Pin acquisition and eviction are ordered by the map lock. Once evicting is
	// set, a queued operator cannot publish through a second same-owner actor.
	candidates := []*projectLiveTableRuntime{}
	s.tr.liveTablesMu.Lock()
	for _, rt := range s.tr.projectLiveTables {
		select {
		case <-rt.ready:
		default:
			continue
		}
		if rt.pins != 0 || rt.evicting || !force && time.Since(rt.lastUsed) < time.Minute {
			continue
		}
		active := false
		rt.attachmentsMu.RLock()
		for _, a := range rt.attachments {
			active = active || a.State == "active"
		}
		rt.attachmentsMu.RUnlock()
		if active {
			continue
		}
		rt.evicting = true
		candidates = append(candidates, rt)
	}
	s.tr.liveTablesMu.Unlock()
	var failures []error
	for _, rt := range candidates {
		rt.mu.Lock()
		var err error
		if rt.instance.State != "retired" {
			err = s.checkpoint(ctx, rt, true)
		}
		rt.mu.Unlock()
		s.tr.liveTablesMu.Lock()
		if err == nil || !s.tr.projectLiveTableTermCurrent(rt.term) {
			if s.tr.projectLiveTables[rt.instanceID] == rt {
				delete(s.tr.projectLiveTables, rt.instanceID)
			}
		} else {
			rt.evicting = false
			failures = append(failures, err)
		}
		s.tr.liveTablesMu.Unlock()
	}
	return errors.Join(failures...)
}
func (s *projectLiveTableService) withRetainedCore(ctx context.Context, project, table, instance string, fn func(*livetable.Table) error) error {
	store, err := s.store()
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, project, table)
	if err != nil {
		return err
	}
	budget := liveTableReservation(projectTableOptions(d.Config))
	if err := s.reserve(project, budget); err != nil {
		return err
	}
	defer s.unreserve(project, budget)
	recovered, err := store.ReadProjectLiveTableRecovery(ctx, project, instance)
	if err != nil {
		return err
	}
	if recovered.Instance.TableID != table || recovered.Instance.State != "retired" {
		return storage.ErrLiveTableConflict
	}
	size := len(recovered.Checkpoint)
	for _, e := range recovered.Entries {
		size += len(e.Payload)
	}
	if size > liveTableRecoveryBytes {
		return storage.ErrLiveTableLimit
	}
	var metadata liveTablePrivateMetadata
	if err := json.Unmarshal([]byte(recovered.Instance.SeedMetadata), &metadata); err != nil || metadata.Version != 1 {
		return storage.ErrLiveTableCorrupt
	}
	cipher, err := restoreLiveTableCipher(ctx, s.tr.continuityKeyWrapper, instance, metadata.Key)
	if err != nil {
		return err
	}
	var snapshot livetable.Snapshot
	if err := cipher.open(recovered.Checkpoint, projectTableAAD(instance, "checkpoint", recovered.Instance.CheckpointRevision), &snapshot); err != nil {
		return err
	}
	if snapshot.Scope != "project" || snapshot.ScopeID != project || snapshot.TableID != instance {
		return storage.ErrLiveTableCorrupt
	}
	if err := validateProjectTableSnapshot(snapshot, d.Config); err != nil {
		return err
	}
	core, err := livetable.Restore(snapshot, nil)
	if err != nil {
		return err
	}
	for _, entry := range recovered.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		var decoded livetable.Entry
		if err := cipher.open(entry.Payload, projectTableAAD(instance, "entry", entry.Revision), &decoded); err != nil {
			return err
		}
		if err := core.Replay(decoded); err != nil {
			return err
		}
	}
	return fn(core)
}
func (s *projectLiveTableService) inspectRetained(ctx context.Context, project, table, instance string, revision uint64, offset, limit int) (livetable.Result, error) {
	var out livetable.Result
	err := s.withRetainedCore(ctx, project, table, instance, func(core *livetable.Table) error {
		var err error
		out, err = core.Inspect(offset, limit, revision)
		if err == nil && out.Revision != revision {
			return storage.ErrLiveTableConflict
		}
		return err
	})
	return out, err
}

// Stats exposes metadata for retained instances without enabling commands or
// including any row values, receipts or lease credentials.
func (s *projectLiveTableService) Stats(ctx context.Context, project, table, instance string) (livetable.Stats, error) {
	ctx, cancel := context.WithTimeout(ctx, projectLiveTableServiceTimeout)
	defer cancel()
	var out livetable.Stats
	d, err := s.Get(ctx, project, table)
	if err != nil {
		return out, err
	}
	if d.State == "archived" || instance != d.CurrentInstanceID {
		err = s.withRetainedCore(ctx, project, table, instance, func(core *livetable.Table) error { out = core.Stats(); return nil })
	} else {
		err = s.withProjectLiveTable(ctx, project, table, instance, func(rt *projectLiveTableRuntime) error { out = rt.table.Stats(); return nil })
	}
	return out, err
}

// WithInspection pins a single bounded read actor for the entire response.
// The callback may only inspect; it must not retain the core or issue commands.
func (s *projectLiveTableService) WithInspection(ctx context.Context, project, table, instance string, fn func(context.Context, *livetable.Table) error) error {
	ctx, cancel := context.WithTimeout(ctx, projectLiveTableServiceTimeout)
	defer cancel()
	d, err := s.Get(ctx, project, table)
	if err != nil {
		return err
	}
	if d.State == "archived" || instance != d.CurrentInstanceID {
		return s.withRetainedCore(ctx, project, table, instance, func(core *livetable.Table) error { return fn(ctx, core) })
	}
	return s.withProjectLiveTable(ctx, project, table, instance, func(rt *projectLiveTableRuntime) error { return fn(ctx, rt.table) })
}
