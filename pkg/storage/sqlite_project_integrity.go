package storage

import (
	"context"
	"errors"
	"fmt"
)

// ErrOwnedProjectNotFound is returned when a project-owned mutation names a
// project that does not exist.
var ErrOwnedProjectNotFound = errors.New("project not found")

// lockProjectForOwnedMutation both validates a project owner and serializes the
// child mutation with project deletion. Several newer project-owned tables do
// not carry schema-level foreign keys for legacy-database compatibility, so
// their write paths must enforce the same invariant explicitly.
func lockProjectForOwnedMutation(tx *storeTx, projectID string) (string, error) {
	return lockProjectForOwnedMutationContext(context.Background(), tx, projectID)
}

func lockProjectForOwnedMutationContext(ctx context.Context, tx *storeTx, projectID string) (string, error) {
	projectID = normalizeProjectID(projectID)
	result, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at = updated_at WHERE id = ?`, projectID)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected != 1 {
		return "", fmt.Errorf("%w: %s", ErrOwnedProjectNotFound, projectID)
	}
	return projectID, nil
}
