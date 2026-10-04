package api

import (
	"github.com/jasonczech/apexload/pkg/storage"
	"log/slog"
	"time"
)

func (tr *TestRunner) recordBackgroundActivity(event *storage.ObjectActivityEvent) {
	store, ok := tr.auditStore.(storage.ObjectActivityStore)
	if !ok {
		return
	}
	if err := store.SaveObjectActivity(event); err != nil {
		slog.Warn("background object activity could not be recorded", "resource_type", event.ResourceType, "resource_id", event.ResourceID, "action", event.Action, "error", err)
	}
}
func (tr *TestRunner) recordTestCreationActivity(test *ManagedTest, actors ...testCreationActor) {
	actorSubject, actorName := test.CreatedBySubject, test.CreatedByDisplayName
	actorType := "user"
	if test.CreatedBySubject == "" {
		actorType = "system"
	}
	if test.CreatedBySubject == "dev-mode" {
		actorType = "dev_mode"
	}
	if len(actors) > 0 {
		actorType = normalizeTestCreationActor(actors[0]).Type
	}
	initiatedBy := ""
	if len(actors) > 0 && actors[0].InitiatedBy != "" {
		actorType, actorSubject, actorName = "system", "execution-plan-scheduler", "Execution Plan Scheduler"
		initiatedBy = actors[0].InitiatedBy
	}
	if metadata, err := decodeCIRunMetadata(test.MaterializationKey); err == nil && metadata.Source.Actor != "" {
		initiatedBy = "github:" + metadata.Source.Actor
	}
	tr.recordBackgroundActivity(&storage.ObjectActivityEvent{InitiatedBy: initiatedBy, ID: "create:test:" + test.ID + ":" + test.CreatedAt.UTC().Format(time.RFC3339Nano), Action: "create", ActorType: actorType, ActorSubject: actorSubject, ActorDisplayName: actorName, ProjectID: test.ProjectID, ResourceType: "test", ResourceID: test.ID, ResourceName: test.Name, Status: "success", CreatedAt: test.CreatedAt})
}

func (tr *TestRunner) recordTestStartActivity(test *ManagedTest, actor testCreationActor) {
	initiatedBy := ""
	if actor.Type == "system" && actor.Subject == test.CreatedBySubject {
		initiatedBy = test.CreatedBySubject
		actor = testCreationActor{Type: "system", Subject: "execution-plan-runner", DisplayName: "Execution Plan Runner"}
	}
	if actor.Type == "" || actor.Subject == "" {
		actor = systemTestCreationActor("test-runner")
		actor.DisplayName = "Test Runner"
		initiatedBy = test.CreatedBySubject
	}
	if metadata, err := decodeCIRunMetadata(test.MaterializationKey); err == nil && metadata.Source.Actor != "" {
		initiatedBy = "github:" + metadata.Source.Actor
	}
	tr.recordBackgroundActivity(&storage.ObjectActivityEvent{ID: testStartActivityID(test.ID, test.CreatedAt), Action: "start", Status: "success", ActorType: actor.Type, ActorSubject: actor.Subject, ActorDisplayName: actor.DisplayName, InitiatedBy: initiatedBy, ProjectID: test.ProjectID, ResourceType: "test", ResourceID: test.ID, ResourceName: test.Name, ChangedFields: []string{"status"}, CreatedAt: time.Now().UTC()})
}

func testStartActivityID(id string, createdAt time.Time) string {
	return "start:test:" + id + ":" + createdAt.UTC().Format(time.RFC3339Nano) + ":success"
}
