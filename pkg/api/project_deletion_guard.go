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

// acquireProjectTestMutation prevents a same-leader project deletion from
// crossing the durable-create-to-memory-install boundary. Storage lease terms
// provide the corresponding cross-replica fence: a creator that loses its term
// after commit returns the committed identity but does not install local state.
func (tr *TestRunner) acquireProjectTestMutation(projectID string) func() {
	if tr == nil {
		return func() {}
	}
	return tr.projectDeletionGates.acquire(normalizeProjectID(projectID), false)
}

// beginProjectDeletion installs a tombstone while holding the same lock used by
// test/script/scenario creation and lifecycle transitions. Once it succeeds, no
// new project work can cross a durable mutation boundary until deletion ends.
func (tr *TestRunner) beginProjectDeletion(projectID string) error {
	if tr == nil {
		return nil
	}
	projectID = normalizeProjectID(projectID)
	release := tr.projectDeletionGates.acquire(projectID, true)
	tr.mu.Lock()
	if err := tr.projectDeletionErrorLocked(projectID); err != nil {
		tr.mu.Unlock()
		release()
		return err
	}
	for _, test := range tr.tests {
		if test == nil || normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		switch test.Status {
		case "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
			tr.mu.Unlock()
			release()
			return fmt.Errorf("project has active tests")
		}
	}
	if tr.projectDeletionUnlock == nil {
		tr.projectDeletionUnlock = make(map[string]func())
	}
	tr.deletingProjects[projectID] = struct{}{}
	tr.projectDeletionUnlock[projectID] = release
	tr.mu.Unlock()
	return nil
}

func (tr *TestRunner) endProjectDeletion(projectID string) {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	projectID = normalizeProjectID(projectID)
	delete(tr.deletingProjects, projectID)
	release := tr.projectDeletionUnlock[projectID]
	delete(tr.projectDeletionUnlock, projectID)
	tr.mu.Unlock()
	if release != nil {
		release()
	}
}
