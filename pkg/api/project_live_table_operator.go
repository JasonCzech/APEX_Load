package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

type projectOperatorContextKey struct{}
type projectOperatorCommit struct {
	store    storage.ProjectLiveTableOperatorStore
	term     storage.LiveTableExecutionTerm
	cipher   *liveTableCipher
	receipt  storage.ProjectLiveTableOperatorReceipt
	audit    *storage.AuditEvent
	clientID string
}

func projectOperatorAAD(receipt storage.ProjectLiveTableOperatorReceipt) string {
	return "project-table/operator/" + receipt.InstanceID + "/" + receipt.KeyHash + "/" + receipt.Digest
}

// Older waiters and lease expiration may commit under this caller's context.
// Only the exact resolved receipt of the intended command owns its audit.
func tryCommitProjectLiveTableOperator(ctx context.Context, entry storage.ProjectLiveTableEntry, outcome *livetable.CommittedOutcome) (bool, error) {
	operation, ok := ctx.Value(projectOperatorContextKey{}).(*projectOperatorCommit)
	if !ok || outcome == nil || outcome.ClientID != operation.clientID || outcome.Sequence != 1 {
		return false, nil
	}
	if entry.TableID != operation.receipt.InstanceID {
		return true, storage.ErrLiveTableConflict
	}
	if outcome.Error != nil {
		return true, errors.New("unexpected deferred operator outcome")
	}
	response := ProjectLiveTableMutationResponse{Result: projectOperatorResult(outcome.Result), ExpiresAt: operation.receipt.ExpiresAt}
	sealed, err := operation.cipher.seal(response, projectOperatorAAD(operation.receipt))
	if err != nil {
		return true, err
	}
	receipt := operation.receipt
	receipt.Outcome = sealed
	audit := *operation.audit
	details := make(map[string]any, len(audit.Details)+2)
	for key, value := range audit.Details {
		details[key] = value
	}
	details["outcome"] = string(outcome.Result.Status)
	details["revision"] = response.Result.Revision
	audit.Details = details
	audit.Status = "success"
	return true, operation.store.AppendProjectLiveTableOperatorEntry(ctx, entry, receipt, &audit, operation.term)
}

func (s *projectLiveTableService) ExecuteOperator(ctx context.Context, project, table, actor string, input ProjectLiveTableMutation, audit *storage.AuditEvent) (ProjectLiveTableMutationResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, projectLiveTableServiceTimeout)
	defer cancel()
	var response ProjectLiveTableMutationResponse
	operatorStore, ok := s.tr.dataPoolStore.(storage.ProjectLiveTableOperatorStore)
	if !ok || audit == nil || actor == "" || audit.ActorSubject != actor {
		return response, errors.New("project live table operator audit unavailable")
	}
	if err := s.tr.validateLiveTablePolicy(project); err != nil {
		return response, err
	}
	definition, err := s.Get(ctx, project, table)
	if err != nil {
		return response, err
	}
	if definition.State == "archived" || input.ExpectedInstanceID != definition.CurrentInstanceID {
		return s.replayRetainedOperator(ctx, project, table, actor, input, definition, operatorStore)
	}
	err = s.withProjectLiveTable(ctx, project, table, input.ExpectedInstanceID, func(rt *projectLiveTableRuntime) error {
		now := time.Now().UTC()
		request, expires, err := input.request(livetable.Profile(rt.definition.Config.Profile), now, time.Duration(rt.definition.Config.RetryWindowMS)*time.Millisecond)
		if err != nil {
			return err
		}
		keyDigest := sha256.Sum256([]byte(actor + "\x00" + input.IdempotencyKey))
		keyHash := hex.EncodeToString(keyDigest[:])
		receipt := storage.ProjectLiveTableOperatorReceipt{InstanceID: rt.instance.ID, ActorSubject: actor, KeyHash: keyHash, Digest: projectOperatorDigest(input), IssuedAt: input.IssuedAt.UTC(), ExpiresAt: expires, ClientScope: fmt.Sprintf("operator/%d/%s/", expires.UnixMilli(), keyHash)}
		saved, err := operatorStore.GetProjectLiveTableOperatorReceipt(ctx, rt.instance.ID, actor, keyHash)
		if err == nil {
			if saved.Digest != receipt.Digest {
				return &livetable.Error{Code: livetable.CodeConflict, Message: "idempotency key already used for a different command"}
			}
			if !now.Before(saved.ExpiresAt) {
				return &livetable.Error{Code: livetable.CodeExpired, Message: "operator retry window expired"}
			}
			if err = rt.cipher.open(saved.Outcome, projectOperatorAAD(*saved), &response); err != nil {
				return errors.New("operator receipt is unavailable")
			}
			response.Replayed = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if rt.definition.State != "active" {
			return storage.ErrLiveTableClosed
		}
		// This cleanup is bounded and never removes a still-retryable command.
		expired, err := operatorStore.ListExpiredProjectLiveTableOperatorReceipts(ctx, rt.instance.ID, now)
		if err != nil {
			return err
		}
		for _, old := range expired {
			if err = rt.table.RetireOwnerScope(ctx, old.ClientScope); err != nil {
				return err
			}
			if err = operatorStore.DeleteProjectLiveTableOperatorReceipt(ctx, old, rt.table.Stats().Revision, liveStorageTerm(rt.term)); err != nil {
				return err
			}
		}
		request.ClientID = receipt.ClientScope + "command"
		request.OwnerID = request.ClientID
		request.Sequence = 1
		operation := &projectOperatorCommit{store: operatorStore, term: liveStorageTerm(rt.term), cipher: rt.cipher, receipt: receipt, audit: audit, clientID: request.ClientID}
		result, err := rt.table.Execute(context.WithValue(ctx, projectOperatorContextKey{}, operation), request)
		if err != nil {
			return err
		}
		response = ProjectLiveTableMutationResponse{Result: projectOperatorResult(result), ExpiresAt: expires}
		return nil
	})
	return response, err
}

// Retired instances remain readable for exact operator reconciliation. This
// path opens only the encrypted outcome and cannot construct a writable actor.
func (s *projectLiveTableService) replayRetainedOperator(ctx context.Context, project, table, actor string, input ProjectLiveTableMutation, definition *storage.ProjectLiveTableDefinition, operatorStore storage.ProjectLiveTableOperatorStore) (ProjectLiveTableMutationResponse, error) {
	var response ProjectLiveTableMutationResponse
	term, err := s.tr.captureProjectLiveTableTerm()
	if err != nil {
		return response, err
	}
	now := time.Now().UTC()
	if _, _, err := input.request(livetable.Profile(definition.Config.Profile), now, time.Duration(definition.Config.RetryWindowMS)*time.Millisecond); err != nil {
		return response, err
	}
	store, err := s.store()
	if err != nil {
		return response, err
	}
	instance, err := store.GetProjectLiveTableInstance(ctx, project, input.ExpectedInstanceID)
	if err != nil {
		return response, err
	}
	if instance.TableID != table || instance.State != "retired" {
		return response, storage.ErrLiveTableConflict
	}
	keyDigest := sha256.Sum256([]byte(actor + "\x00" + input.IdempotencyKey))
	keyHash := hex.EncodeToString(keyDigest[:])
	saved, err := operatorStore.GetProjectLiveTableOperatorReceipt(ctx, instance.ID, actor, keyHash)
	if errors.Is(err, sql.ErrNoRows) {
		return response, storage.ErrLiveTableClosed
	}
	if err != nil {
		return response, err
	}
	if saved.InstanceID != instance.ID || saved.ActorSubject != actor || saved.KeyHash != keyHash || saved.Digest != projectOperatorDigest(input) {
		return response, &livetable.Error{Code: livetable.CodeConflict, Message: "idempotency key already used for a different command"}
	}
	if !now.Before(saved.ExpiresAt) {
		return response, &livetable.Error{Code: livetable.CodeExpired, Message: "operator retry window expired"}
	}
	var metadata liveTablePrivateMetadata
	if err := json.Unmarshal([]byte(instance.SeedMetadata), &metadata); err != nil || metadata.Version != 1 {
		return response, storage.ErrLiveTableCorrupt
	}
	cipher, err := restoreLiveTableCipher(ctx, s.tr.continuityKeyWrapper, instance.ID, metadata.Key)
	if err != nil {
		return response, err
	}
	if err := cipher.open(saved.Outcome, projectOperatorAAD(*saved), &response); err != nil {
		return ProjectLiveTableMutationResponse{}, errors.New("operator receipt is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return ProjectLiveTableMutationResponse{}, err
	}
	if err := s.tr.validateLiveTablePolicy(project); err != nil {
		return ProjectLiveTableMutationResponse{}, err
	}
	if !s.tr.projectLiveTableTermCurrent(term) {
		return ProjectLiveTableMutationResponse{}, storage.ErrLiveTableConflict
	}
	response.Replayed = true
	return response, nil
}
