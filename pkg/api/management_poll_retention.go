package api

import (
	"context"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const managementPollJournalRetention = 7 * 24 * time.Hour

// Reuse the leader-only maintenance sweep. The persistent bounded keyset scan advances
// across protected rows without enumerating all project metadata. Durable receipt references and unresolved
// physical ownership remain the storage layer's authority for retention.
func (s *Server) pruneManagementPollJournals(ctx context.Context, now time.Time) error {
	store, ok := s.store.(storage.ManagementPollRetentionStore)
	if !ok {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	before := now.UTC().Add(-managementPollJournalRetention)
	_, _, err := store.SweepManagementPollRetention(ctx, before, 512)
	return err
}
