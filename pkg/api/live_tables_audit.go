package api

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Finalization audit identities survive retries and controller takeover. A
// failed write is reconciled against that exact identity, so an uncertain audit
// commit never creates duplicate events or allows an unaudited pool mutation.
func (tr *TestRunner) auditLiveTableFinalization(test *ManagedTest, rt *runLiveTable, stage string) error {
	mode := rt.binding.LiveOptions.EndMode
	if rt.final != nil {
		mode = rt.final.Mode
	}
	if mode == "discard" || (rt.final != nil && rt.final.State == "pending") {
		return nil
	}
	writer, ok := tr.auditStore.(interface {
		SaveAuditEvent(*storage.AuditEvent) error
	})
	if !ok || writer == nil {
		return errors.New("live table finalization audit is unavailable")
	}
	tr.mu.RLock()
	subject := firstNonEmpty(test.StartedBySubject, test.CreatedBySubject)
	tr.mu.RUnlock()
	status := "started"
	details := map[string]any{"run_id": rt.record.TestID, "mode": mode, "stage": stage}
	if rt.final != nil {
		status = rt.final.State
		details["output_pool_id"] = rt.final.OutputPoolID
		details["reason"] = rt.final.Reason
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:live-table-audit:"+rt.record.ID+":"+mode+":"+stage)).String()
	action := "live_table.finalize." + stage
	actorType := "user"
	if strings.HasPrefix(subject, "service-account:") || strings.HasPrefix(subject, "github-ci:") {
		actorType = "service_account"
	}
	event := &storage.AuditEvent{ID: id, Action: action, ActorType: actorType, ActorSubject: subject, ProjectID: rt.record.ProjectID, ResourceType: "live_table", ResourceID: rt.record.ID, Status: status, Details: details, CreatedAt: time.Now().UTC()}
	if err := writer.SaveAuditEvent(event); err != nil {
		events, readErr := tr.auditStore.ListAuditEvents(storage.AuditEventFilter{ProjectID: event.ProjectID, ResourceType: event.ResourceType, ResourceID: event.ResourceID, Action: action, Limit: 8})
		if readErr == nil {
			for _, saved := range events {
				if saved.ID == id && saved.Status == status && saved.ActorSubject == subject {
					return nil
				}
			}
		}
		return fmt.Errorf("live table finalization audit %s is unavailable: %w", stage, err)
	}
	return nil
}
