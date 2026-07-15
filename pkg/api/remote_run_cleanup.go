package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	remoteRunCleanupSettingKey  = "remote.run_cleanup.v1"
	maxRemoteRunCleanupRecords  = 10_000
	maxRemoteRunCleanupRawBytes = 16 << 20
	remoteRunCleanupWorkers     = 4
	remoteRunCleanupRetryDelay  = 15 * time.Second
)

var remoteRunCleanupMu sync.Mutex

type remoteRunCleanupRecord struct {
	ID           string        `json:"id"`
	Remote       LibraryRemote `json:"remote"`
	RemoteTestID string        `json:"remote_test_id"`
	CreatedAt    time.Time     `json:"created_at"`
}

func newRemoteRunCleanupRecord(remote LibraryRemote, remoteTestID string) remoteRunCleanupRecord {
	remote.APIKey = ""
	return remoteRunCleanupRecord{
		ID:           uuid.NewString(),
		Remote:       remote,
		RemoteTestID: strings.TrimSpace(remoteTestID),
		CreatedAt:    time.Now().UTC(),
	}
}

func (s *Server) enqueueRemoteRunCleanup(record remoteRunCleanupRecord) error {
	if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Remote.ID) == "" || strings.TrimSpace(record.RemoteTestID) == "" {
		return fmt.Errorf("remote cleanup identity is incomplete")
	}
	return s.mutateRemoteRunCleanups(func(records *[]remoteRunCleanupRecord) (bool, error) {
		for _, existing := range *records {
			if existing.ID == record.ID {
				return false, nil
			}
		}
		if len(*records) >= maxRemoteRunCleanupRecords {
			return false, fmt.Errorf("remote cleanup outbox limit of %d reached", maxRemoteRunCleanupRecords)
		}
		*records = append(*records, record)
		return true, nil
	})
}

func (s *Server) removeRemoteRunCleanup(id string) error {
	id = strings.TrimSpace(id)
	return s.mutateRemoteRunCleanups(func(records *[]remoteRunCleanupRecord) (bool, error) {
		for i := range *records {
			if (*records)[i].ID != id {
				continue
			}
			*records = append((*records)[:i], (*records)[i+1:]...)
			return true, nil
		}
		return false, nil
	})
}

func (s *Server) mutateRemoteRunCleanups(mutate func(*[]remoteRunCleanupRecord) (bool, error)) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("remote cleanup storage is not configured")
	}
	remoteRunCleanupMu.Lock()
	defer remoteRunCleanupMu.Unlock()
	for attempt := 0; attempt < remoteRunLinkCASMaxRetries; attempt++ {
		raw, err := s.store.GetSetting(remoteRunCleanupSettingKey)
		if err != nil {
			return err
		}
		records, err := decodeRemoteRunCleanups(raw)
		if err != nil {
			return err
		}
		changed, err := mutate(&records)
		if err != nil || !changed {
			return err
		}
		payload, err := json.Marshal(records)
		if err != nil {
			return err
		}
		if len(payload) > maxRemoteRunCleanupRawBytes {
			return fmt.Errorf("remote cleanup outbox exceeds the %d-byte limit", maxRemoteRunCleanupRawBytes)
		}
		if cas, ok := s.store.(librarySettingsCASStore); ok {
			updated, err := cas.CompareAndSwapSetting(remoteRunCleanupSettingKey, raw, string(payload))
			if err != nil {
				return err
			}
			if updated {
				return nil
			}
			continue
		}
		if s.runner != nil && s.runner.leadership != nil {
			return fmt.Errorf("HA storage must support compare-and-swap settings")
		}
		return s.store.SaveSetting(remoteRunCleanupSettingKey, string(payload))
	}
	return fmt.Errorf("remote cleanup outbox changed concurrently; retry")
}

func (s *Server) loadRemoteRunCleanups() ([]remoteRunCleanupRecord, error) {
	if s == nil || s.store == nil {
		return nil, nil
	}
	raw, err := s.store.GetSetting(remoteRunCleanupSettingKey)
	if err != nil {
		return nil, err
	}
	return decodeRemoteRunCleanups(raw)
}

func (s *Server) hasPendingRemoteRunCleanup(remoteID string) (bool, error) {
	remoteID = strings.TrimSpace(remoteID)
	records, err := s.loadRemoteRunCleanups()
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if strings.TrimSpace(record.Remote.ID) == remoteID {
			return true, nil
		}
	}
	return false, nil
}

func decodeRemoteRunCleanups(raw string) ([]remoteRunCleanupRecord, error) {
	if len(raw) > maxRemoteRunCleanupRawBytes {
		return nil, fmt.Errorf("remote cleanup outbox exceeds the %d-byte limit", maxRemoteRunCleanupRawBytes)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var records []remoteRunCleanupRecord
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil, err
	}
	if len(records) > maxRemoteRunCleanupRecords {
		return nil, fmt.Errorf("remote cleanup outbox exceeds the %d-entry limit", maxRemoteRunCleanupRecords)
	}
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Remote.ID) == "" || strings.TrimSpace(record.RemoteTestID) == "" {
			return nil, fmt.Errorf("remote cleanup outbox contains an incomplete record")
		}
		if _, duplicate := seen[record.ID]; duplicate {
			return nil, fmt.Errorf("remote cleanup outbox contains duplicate id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
	}
	return records, nil
}

func (s *Server) attemptRemoteRunCleanup(record remoteRunCleanupRecord) error {
	return s.attemptRemoteRunCleanupContext(context.Background(), record)
}

func (s *Server) attemptRemoteRunCleanupContext(parent context.Context, record remoteRunCleanupRecord) error {
	// The link is the durable authorization/ownership commit. A crash can leave
	// the pre-created cleanup intent behind after that commit; in that case only
	// remove the stale intent, never the now-authorized remote run.
	links, err := loadRemoteRunLinksFromStore(s.store)
	if err != nil {
		// A failed/ambiguous link commit followed by a failed read must not be
		// interpreted as proof that no authorization anchor exists.
		return fmt.Errorf("verify remote run authorization link before cleanup: %w", err)
	}
	if _, linked := findProjectScopedRemoteRunLink(links, record.Remote.ID, record.RemoteTestID); linked {
		return s.removeRemoteRunCleanup(record.ID)
	}
	timeout := libraryRemoteJSONTimeout()
	ctx, cancel := context.WithTimeout(ctxOrBackground(parent), timeout)
	defer cancel()
	path := "/api/v1/tests/" + url.PathEscape(strings.TrimSpace(record.RemoteTestID))
	if _, err := s.remoteJSONAccepted(ctx, record.Remote, http.MethodDelete, path, nil, nil, map[int]bool{http.StatusNotFound: true}); err != nil {
		return err
	}
	return s.removeRemoteRunCleanup(record.ID)
}

func (s *Server) reconcileRemoteRunCleanups() {
	s.reconcileRemoteRunCleanupsContext(context.Background())
}

func (s *Server) reconcileRemoteRunCleanupsContext(ctx context.Context) {
	records, err := s.loadRemoteRunCleanups()
	if err != nil {
		slog.Warn("failed to load remote run cleanup outbox", "err", err)
		return
	}
	jobs := make(chan remoteRunCleanupRecord)
	var workers sync.WaitGroup
	for i := 0; i < remoteRunCleanupWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				var record remoteRunCleanupRecord
				var ok bool
				select {
				case <-ctx.Done():
					return
				case record, ok = <-jobs:
					if !ok {
						return
					}
				}
				if err := s.attemptRemoteRunCleanupContext(ctx, record); err != nil && ctx.Err() == nil {
					slog.Warn("remote run cleanup remains pending", "remote_id", record.Remote.ID, "remote_test_id", record.RemoteTestID, "err", err)
				}
			}
		}()
	}
	for _, record := range records {
		select {
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return
		case jobs <- record:
		}
	}
	close(jobs)
	workers.Wait()
}

func (s *Server) startRemoteRunCleanupRetry() {
	if s == nil {
		return
	}
	s.remoteCleanupRetryMu.Lock()
	if s.remoteCleanupCancel != nil {
		s.remoteCleanupRetryMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.remoteCleanupCancel = cancel
	s.remoteCleanupDone = done
	s.remoteCleanupRetryMu.Unlock()

	go func() {
		defer close(done)
		s.runRemoteRunCleanupRetry(ctx, remoteRunCleanupRetryDelay)
		s.remoteCleanupRetryMu.Lock()
		if s.remoteCleanupDone == done {
			s.remoteCleanupCancel = nil
			s.remoteCleanupDone = nil
		}
		s.remoteCleanupRetryMu.Unlock()
	}()
}

// startPendingRemoteRunCleanupRetry resumes orphan cleanup as part of Start's
// owned lifecycle. NewServer deliberately performs no asynchronous work: a
// caller may construct a server for inspection or configuration and never run
// it, and that must not leak an HTTP retry worker against the durable store.
func (s *Server) startPendingRemoteRunCleanupRetry() {
	pending, err := s.loadRemoteRunCleanups()
	if err != nil {
		slog.Warn("failed to inspect remote run cleanup outbox", "error", err)
		return
	}
	if len(pending) > 0 {
		s.startRemoteRunCleanupRetry()
	}
}

func (s *Server) runRemoteRunCleanupRetry(ctx context.Context, retryDelay time.Duration) {
	if retryDelay <= 0 {
		retryDelay = remoteRunCleanupRetryDelay
	}
	for {
		s.reconcileRemoteRunCleanupsContext(ctx)
		if ctx.Err() != nil {
			return
		}
		pending, err := s.loadRemoteRunCleanups()
		if err == nil && len(pending) == 0 {
			return
		}
		if err != nil {
			slog.Warn("remote run cleanup retry could not inspect outbox", "err", err)
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Server) stopRemoteRunCleanupRetry(ctx context.Context) {
	if s == nil {
		return
	}
	s.remoteCleanupRetryMu.Lock()
	cancel := s.remoteCleanupCancel
	done := s.remoteCleanupDone
	s.remoteCleanupRetryMu.Unlock()
	if cancel == nil || done == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
