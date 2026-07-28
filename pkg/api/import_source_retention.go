package api

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
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
// The script is already durably committed by the time this runs. A false
// result tells the commit workflow to report its partial state explicitly and
// leave the protected claim retryable. The script fingerprint supplied to
// storage prevents that retry (or a delayed publication) from attaching this
// source to a different live script revision.
func (s *Server) retainProtectedImportSource(
	ctx context.Context,
	script *dsl.Script,
	sourceProfile string,
	source []byte,
	actor string,
	skipMatchingActive bool,
) bool {
	if s == nil || script == nil {
		return false
	}
	if len(source) == 0 {
		// Legacy v1 candidates did not carry retained source. Completing those
		// sessions preserves their established compatibility behavior.
		return true
	}
	retentionStore, ok := s.store.(storage.ImportSourceRetentionStore)
	if !ok {
		// Lightweight/in-memory stores predate durable source revisions. The
		// protected candidate remains useful for server-owned commit integrity,
		// but there is no durable source-retention facility to fail.
		return true
	}
	present, err := s.brunoArtifactStorePresent()
	if err != nil {
		slog.Error("inspect import source retention availability", "source", sourceProfile, "script_id", script.ID, "error", err)
		return false
	}
	if !present {
		// No envelope key configured. Protected sessions are additive in this
		// deployment shape and retention follows them.
		return true
	}
	objects, err := s.brunoArtifactStore()
	if err != nil {
		slog.Error("import source retention unavailable", "source", sourceProfile, "script_id", script.ID, "error", err)
		return false
	}

	// The script commit has already crossed its durable boundary. Do not let a
	// disconnected client cancel the bounded source publication and then
	// complete the one-shot session that held the only remaining source copy.
	if ctx == nil {
		ctx = context.Background()
	}
	retentionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	unlockSourcePublication, err := objects.LockSourcePublication()
	if err != nil {
		slog.Error("failed to reserve retained import source publication", "source", sourceProfile, "script_id", script.ID, "error", err)
		return false
	}
	defer func() {
		if unlockErr := unlockSourcePublication(); unlockErr != nil {
			slog.Warn("release retained import source publication lock", "source", sourceProfile, "script_id", script.ID, "error", unlockErr)
		}
	}()

	projectID := normalizeProjectID(script.ProjectID)
	plan, err := objects.PlanBytes(projectID, "raw", source, int64(len(source)))
	if err != nil {
		slog.Error("failed to plan retained import source", "source", sourceProfile, "script_id", script.ID, "error", err)
		return false
	}
	if skipMatchingActive && s.retainedImportSourceMatchesActive(script, sourceProfile, plan) {
		return true
	}

	publicationID := uuid.NewString()
	if err := objects.SavePublication(importartifact.PublicationDescriptor{
		ID:        publicationID,
		Profile:   strings.ToLower(strings.TrimSpace(sourceProfile)),
		ProjectID: projectID,
		ExpiresAt: time.Now().UTC(),
		Objects:   []importartifact.OpenRequest{plan},
	}); err != nil {
		slog.Error("failed to journal retained import source publication", "source", sourceProfile, "script_id", script.ID, "error", err)
		return false
	}
	publicationRecorded := true
	published := false
	plannedArtifact := storage.ImportSourceArtifact{
		ProjectID:    projectID,
		ArtifactKind: "raw",
		BackendKey:   plan.BackendKey,
		SHA256:       plan.SHA256,
		SizeBytes:    plan.SizeBytes,
	}
	defer func() {
		if published {
			return
		}
		cleanupErr := s.cleanupUnreferencedBrunoArtifactsLocked(
			objects,
			[]storage.BrunoSourceArtifact{plannedArtifact},
			"",
		)
		if cleanupErr != nil {
			// Keep the expired publication journal. The janitor retries the
			// same database/session reference checks once the dependency that
			// failed here is healthy again.
			slog.Warn("retain failed import source publication for janitor cleanup", "source", sourceProfile, "script_id", script.ID, "publication_id", publicationID, "error", cleanupErr)
			return
		}
		if publicationRecorded {
			if deleteErr := objects.DeletePublication(publicationID, projectID); deleteErr != nil {
				slog.Warn("remove cleaned import source publication", "source", sourceProfile, "script_id", script.ID, "publication_id", publicationID, "error", deleteErr)
			}
		}
	}()

	object, err := objects.PutBytes(retentionCtx, projectID, "raw", source, int64(len(source)))
	if err != nil {
		slog.Error("failed to store retained import source", "source", sourceProfile, "script_id", script.ID, "error", err)
		return false
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
		SourceFormat:              sourceProfile,
		CanonicalScript:           script,
		ExpectedScriptFingerprint: importSourceScriptFingerprint(script),
		CreatedBy:                 strings.TrimSpace(actor),
	}); err != nil {
		slog.Error("failed to record retained import source revision", "source", sourceProfile, "script_id", script.ID, "error", err)
		return false
	}
	published = true
	if err := objects.DeletePublication(publicationID, projectID); err != nil {
		// The database reference is authoritative now. Leaving the journal is
		// safe: the janitor observes that reference before deleting the expired
		// publication descriptor.
		slog.Warn("remove completed import source publication", "source", sourceProfile, "script_id", script.ID, "publication_id", publicationID, "error", err)
	} else {
		publicationRecorded = false
	}
	return true
}

func (s *Server) retainedImportSourceMatchesActive(
	script *dsl.Script,
	sourceProfile string,
	plan importartifact.OpenRequest,
) bool {
	if s == nil || script == nil {
		return false
	}
	revisions, ok := s.store.(storage.BrunoImportStore)
	if !ok || revisions == nil {
		return false
	}
	projectID := normalizeProjectID(script.ProjectID)
	active, err := revisions.GetActiveBrunoSourceRevision(
		script.ID,
		projectID,
	)
	if err != nil || active == nil || active.CanonicalScript == nil ||
		!strings.EqualFold(strings.TrimSpace(active.SourceFormat), strings.TrimSpace(sourceProfile)) {
		return false
	}
	artifact, err := revisions.GetBrunoSourceArtifact(active.RawArtifactID, projectID)
	if err != nil || artifact == nil ||
		artifact.ProjectID != projectID ||
		!strings.EqualFold(strings.TrimSpace(artifact.ArtifactKind), "raw") ||
		!strings.EqualFold(strings.TrimSpace(artifact.SHA256), strings.TrimSpace(plan.SHA256)) ||
		artifact.SizeBytes != plan.SizeBytes {
		return false
	}
	currentFingerprint, err := storage.ImportSourceScriptFingerprint(script)
	if err != nil {
		return false
	}
	activeFingerprint, err := storage.ImportSourceScriptFingerprint(active.CanonicalScript)
	return err == nil && activeFingerprint == currentFingerprint
}

func importSourceScriptFingerprint(script *dsl.Script) string {
	fingerprint, err := storage.ImportSourceScriptFingerprint(script)
	if err != nil {
		return ""
	}
	return fingerprint
}
