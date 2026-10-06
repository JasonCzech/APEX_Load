package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Preparation probes own immutable private configuration so failed cleanup can
// be handed off independently from a successful verification result.
func beginPreparedElasticInspection(ctx context.Context, store storage.ElasticInspectionStore, source storage.MonitorSource, snapshot *storage.ExpandedElasticConnectionSnapshot) (*storage.ElasticInspection, string, error) {
	now := time.Now().UTC()
	expires := now.Add(time.Minute)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(expires) {
		expires = deadline
	}
	if ctx.Err() != nil || !expires.After(now) {
		return nil, "", errors.New("Elastic preparation connectivity timed out")
	}
	work := elasticInspectionWork{Connection: snapshot, CollectorPublicKey: source.CollectorPublicKey, Request: ElasticInspectionRequest{Kind: "verify", Indices: source.Elastic.Indices, Selection: source.Elastic, FromUTC: now.Add(-time.Minute), ToUTC: now}}
	body, err := json.Marshal(work)
	if err != nil || len(body) > 96<<10 {
		return nil, "", errors.New("Elastic preparation inspection exceeds limits")
	}
	collector := source.CollectorID
	if source.ExecutorMode == "direct" {
		collector = ""
	}
	op := &storage.ElasticInspection{ID: "elastic-inspection-" + uuid.NewString(), ProjectID: source.ProjectID, ConnectionID: source.ConnectionID, ConnectionRevision: snapshot.Revision, CollectorID: collector, Kind: "verify", PayloadJSON: string(body), CreatedAt: now, ExpiresAt: expires}
	if err = store.CreateElasticInspection(op); err != nil {
		return nil, "", err
	}
	if source.ExecutorMode != "direct" {
		return op, "", nil
	}
	owner := uuid.NewString()
	if claimed, err := store.ClaimElasticInspection(op.ID, owner, now, expires); err != nil || !claimed {
		return nil, "", errors.New("Elastic preparation inspection claim unavailable")
	}
	return op, owner, nil
}

func completePreparedElasticInspection(store storage.ElasticInspectionStore, op *storage.ElasticInspection, owner string, result collectoragent.ElasticInspectionData, inspectionErr error, now time.Time) error {
	if len(result.CleanupContinuations) > 0 {
		if retained, err := store.RetainElasticInspectionCleanup(op.ID, owner, result.CleanupContinuations, now); err != nil || !retained {
			return errors.New("Elastic preparation cleanup ownership unavailable")
		}
	}
	message := ""
	if inspectionErr != nil {
		message = "Elastic inspection failed"
	}
	body, err := json.Marshal(result)
	if err != nil || len(body) > 1<<20 {
		body = []byte(`{}`)
		message = "Elastic inspection result exceeds limits"
	}
	if completed, err := store.CompleteElasticInspection(op.ID, owner, string(body), message, now); err != nil || !completed {
		return errors.New("Elastic preparation inspection claim expired or cancelled")
	}
	return nil
}
