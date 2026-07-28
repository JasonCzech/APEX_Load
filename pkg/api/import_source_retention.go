package api

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// retainProtectedImportSource writes a committed import's raw source as an
// encrypted artifact and makes it the script's active source revision.
//
// The bytes come from the protected session envelope rather than being written
// at inspect time, so an abandoned import leaves nothing behind: the session
// expires on its own and no artifact was ever created. That also means this
// runs after the script row exists, which is why it cannot simply be folded
// into the create call.
//
// Retention is best-effort by design. The script is already durably committed
// by the time this runs, and failing the request here would tell the caller the
// import did not happen when it did -- provoking a retry that creates a second
// script. Losing retained source degrades traceability; a phantom duplicate
// import corrupts the project. The error is logged and swallowed deliberately.
func (s *Server) retainProtectedImportSource(
	ctx context.Context,
	script *dsl.Script,
	sourceProfile string,
	source []byte,
	actor string,
) {
	if s == nil || script == nil || len(source) == 0 {
		return
	}
	retentionStore, ok := s.store.(storage.ImportSourceRetentionStore)
	if !ok {
		return
	}
	present, err := s.brunoArtifactStorePresent()
	if err != nil || !present {
		// No envelope key configured. Protected sessions are additive in this
		// deployment shape and retention follows them.
		return
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		slog.Error("import source retention unavailable", "source", sourceProfile, "script_id", script.ID, "error", err)
		return
	}
	projectID := normalizeProjectID(script.ProjectID)
	object, err := objects.PutBytes(ctx, projectID, "raw", source, int64(len(source)))
	if err != nil {
		slog.Error("failed to store retained import source", "source", sourceProfile, "script_id", script.ID, "error", err)
		return
	}
	if _, err := retentionStore.RetainImportSource(&storage.ImportSourceRetention{
		ScriptID:  script.ID,
		ProjectID: projectID,
		Artifact: storage.ImportSourceArtifact{
			ProjectID:          projectID,
			ArtifactKind:       "raw",
			BackendKey:         object.BackendKey,
			SHA256:             object.SHA256,
			SizeBytes:          object.SizeBytes,
			SourceFormat:       sourceProfile,
			EncryptionMetadata: object.EncryptionMetadata,
			LifecycleState:     "active",
		},
		SourceFormat:    sourceProfile,
		CanonicalScript: script,
		CreatedBy:       strings.TrimSpace(actor),
	}); err != nil {
		slog.Error("failed to record retained import source revision", "source", sourceProfile, "script_id", script.ID, "error", err)
	}
}
