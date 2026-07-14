package api

import "fmt"

func (tr *TestRunner) projectDeletionErrorLocked(projectID string) error {
	if tr == nil {
		return nil
	}
	projectID = normalizeProjectID(projectID)
	if _, deleting := tr.deletingProjects[projectID]; deleting {
		return fmt.Errorf("project %s is being deleted", projectID)
	}
	return nil
}

// beginProjectDeletion installs a tombstone while holding the same lock used by
// test/script/scenario creation and lifecycle transitions. Once it succeeds, no
// new project work can cross a durable mutation boundary until deletion ends.
func (tr *TestRunner) beginProjectDeletion(projectID string) error {
	if tr == nil {
		return nil
	}
	projectID = normalizeProjectID(projectID)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if err := tr.projectDeletionErrorLocked(projectID); err != nil {
		return err
	}
	for _, test := range tr.tests {
		if test == nil || normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		switch test.Status {
		case "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
			return fmt.Errorf("project has active tests")
		}
	}
	tr.deletingProjects[projectID] = struct{}{}
	return nil
}

func (tr *TestRunner) endProjectDeletion(projectID string) {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	delete(tr.deletingProjects, normalizeProjectID(projectID))
	tr.mu.Unlock()
}
