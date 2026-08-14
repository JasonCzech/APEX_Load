package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/secretstore"
)

const importSecretRollbackTimeout = 20 * time.Second

// importSecretMigrationActor is deliberately operation-specific. An
// ownership-checked rollback must never delete a value created by a different
// import performed by the same human actor.
func importSecretMigrationActor(profile, operationID string) string {
	return "script-import:" + strings.ToLower(strings.TrimSpace(profile)) + ":" + strings.TrimSpace(operationID)
}

// rollbackCreatedImportSecrets compensates only values created by the current
// import actor. It returns metadata for any value that could not be proven
// removed so callers can report a truthful partial outcome without disclosing
// write-only values.
func (s *Server) rollbackCreatedImportSecrets(
	r *http.Request,
	projectID string,
	createdBy string,
	created []secretstore.Metadata,
	trigger string,
) ([]secretstore.Metadata, error) {
	if len(created) == 0 {
		return nil, nil
	}
	rollback, ok := s.projectSecretStore.(secretstore.CreatedValueRollbackStore)
	if !ok || !secretstore.SupportsCreatedValueRollback(s.projectSecretStore) {
		return append([]secretstore.Metadata(nil), created...), secretstore.ErrRollbackUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), importSecretRollbackTimeout)
	defer cancel()
	retained := make([]secretstore.Metadata, 0)
	var rollbackErr error
	for index := len(created) - 1; index >= 0; index-- {
		metadata := created[index]
		removed, err := rollback.RollbackCreated(ctx, projectID, metadata.Name, createdBy)
		status, outcome, reason := "success", "rolled_back", ""
		if err != nil {
			status, outcome, reason = "failure", "provider_error", "provider_error"
			retained = append(retained, metadata)
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("roll back imported project secret %s: provider operation failed", metadata.Name))
		} else if !removed {
			status, outcome, reason = "failure", "ownership_changed", "ownership_changed"
			retained = append(retained, metadata)
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("roll back imported project secret %s: ownership changed", metadata.Name))
		}
		if auditErr := s.persistProjectSecretOutcome(
			r,
			"script_import.secret.migrate.rollback",
			status,
			outcome,
			metadata.Name,
			projectID,
			secretStoreProvider(s.projectSecretStore),
			reason,
			map[string]any{"trigger": strings.TrimSpace(trigger)},
		); auditErr != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("persist imported project secret rollback audit: %w", auditErr))
		}
	}
	// Restore source order in the value-free partial response.
	for left, right := 0, len(retained)-1; left < right; left, right = left+1, right-1 {
		retained[left], retained[right] = retained[right], retained[left]
	}
	return retained, rollbackErr
}

// remainingImportSecretMigrationNames adds successfully compensated creates
// back to the retry plan. Values that could not be safely rolled back remain in
// migrated/committed_names and must not also be presented as missing.
func remainingImportSecretMigrationNames(
	remaining []string,
	attempted []secretstore.Metadata,
	retained []secretstore.Metadata,
) []string {
	retainedNames := make(map[string]struct{}, len(retained))
	for _, metadata := range retained {
		retainedNames[strings.TrimSpace(metadata.Name)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(remaining)+len(attempted))
	result := make([]string, 0, len(remaining)+len(attempted))
	appendName := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, duplicate := seen[name]; duplicate {
			return
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	for _, metadata := range attempted {
		name := strings.TrimSpace(metadata.Name)
		if _, stillCommitted := retainedNames[name]; !stillCommitted {
			appendName(name)
		}
	}
	for _, name := range remaining {
		appendName(name)
	}
	return result
}
