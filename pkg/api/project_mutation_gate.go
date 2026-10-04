package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const deleteProjectRoutePattern = "DELETE /api/v1/projects/{id}"

type projectMutationAdmissionContextKey struct{}

// projectMutationCoordinator serializes a project deletion only against
// mutations for that same project. Entries are reference counted so a long-lived
// controller does not retain one lock for every project it has ever seen.
type projectMutationCoordinator struct {
	mu         sync.Mutex
	entries    map[string]*projectMutationLock
	tombstones map[string]struct{}
	durable    projectMutationLeaseStore
}

type projectMutationLeaseStore interface {
	TryAcquireSchedulerLease(name, ownerID string, now time.Time, ttl time.Duration) (bool, error)
	ReleaseSchedulerLease(name, ownerID string) error
}

const (
	secretReferenceMutationLeaseTTL  = 5 * time.Minute
	secretReferenceMutationWait      = 5 * time.Second
	secretReferenceMutationPollEvery = 25 * time.Millisecond
)

type projectMutationLock struct {
	mu   sync.RWMutex
	refs int
}

func (c *projectMutationCoordinator) acquire(projectID string, exclusive bool) func() {
	projectID = normalizeProjectID(projectID)
	if projectID == "" {
		return func() {}
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]*projectMutationLock)
	}
	entry := c.entries[projectID]
	if entry == nil {
		entry = &projectMutationLock{}
		c.entries[projectID] = entry
	}
	entry.refs++
	c.mu.Unlock()

	if exclusive {
		entry.mu.Lock()
	} else {
		entry.mu.RLock()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if exclusive {
				entry.mu.Unlock()
			} else {
				entry.mu.RUnlock()
			}
			c.mu.Lock()
			entry.refs--
			if entry.refs == 0 && c.entries[projectID] == entry {
				delete(c.entries, projectID)
			}
			c.mu.Unlock()
		})
	}
}

func (c *projectMutationCoordinator) configureDurableLease(store any) {
	if c == nil {
		return
	}
	leaseStore, _ := store.(projectMutationLeaseStore)
	c.mu.Lock()
	c.durable = leaseStore
	c.mu.Unlock()
}

func (c *projectMutationCoordinator) acquireDurable(projectID string) (func(), error) {
	projectID = normalizeProjectID(projectID)
	releaseLocal := c.acquire(projectID, true)
	if projectID == "" {
		return releaseLocal, nil
	}
	c.mu.Lock()
	store := c.durable
	c.mu.Unlock()
	if store == nil {
		return releaseLocal, nil
	}
	sum := sha256.Sum256([]byte(projectID))
	leaseName := "secret-reference-mutation/" + hex.EncodeToString(sum[:])
	ownerID := uuid.NewString()
	deadline := time.Now().Add(secretReferenceMutationWait)
	for {
		acquired, err := store.TryAcquireSchedulerLease(leaseName, ownerID, time.Now().UTC(), secretReferenceMutationLeaseTTL)
		if err != nil {
			releaseLocal()
			return func() {}, fmt.Errorf("acquire durable secret-reference mutation fence: %w", err)
		}
		if acquired {
			stopRenewal := make(chan struct{})
			renewalDone := make(chan struct{})
			go func() {
				defer close(renewalDone)
				ticker := time.NewTicker(secretReferenceMutationLeaseTTL / 3)
				defer ticker.Stop()
				for {
					select {
					case <-stopRenewal:
						return
					case now := <-ticker.C:
						renewed, renewErr := store.TryAcquireSchedulerLease(leaseName, ownerID, now.UTC(), secretReferenceMutationLeaseTTL)
						if renewErr != nil || !renewed {
							slog.Error("renew durable secret-reference mutation fence", "project_id", projectID, "error", renewErr)
						}
					}
				}
			}()
			var once sync.Once
			return func() {
				once.Do(func() {
					close(stopRenewal)
					<-renewalDone
					if err := store.ReleaseSchedulerLease(leaseName, ownerID); err != nil {
						slog.Error("release durable secret-reference mutation fence", "project_id", projectID, "error", err)
					}
					releaseLocal()
				})
			}, nil
		}
		if !time.Now().Before(deadline) {
			releaseLocal()
			return func() {}, fmt.Errorf("project secret references are busy; retry the mutation")
		}
		time.Sleep(secretReferenceMutationPollEvery)
	}
}

// markDeleted records a successful durable project deletion while its
// exclusive project gate is still held. Tombstones intentionally live for the
// controller process lifetime: a mutation may have resolved its project before
// the delete acquired the gate and then be descheduled until after all active
// lock references have drained.
func (c *projectMutationCoordinator) markDeleted(projectID string) {
	projectID = normalizeProjectID(projectID)
	if projectID == "" {
		return
	}
	c.mu.Lock()
	if c.tombstones == nil {
		c.tombstones = make(map[string]struct{})
	}
	c.tombstones[projectID] = struct{}{}
	c.mu.Unlock()
}

func (c *projectMutationCoordinator) wasDeleted(projectID string) bool {
	projectID = normalizeProjectID(projectID)
	if projectID == "" {
		return false
	}
	c.mu.Lock()
	_, deleted := c.tombstones[projectID]
	c.mu.Unlock()
	return deleted
}

// projectMutationAdmission runs after authorization has resolved and bound the
// request's trusted project identity. Slow request-body reads therefore happen
// before the lock is acquired, and unrelated projects never share a lock.
func (s *Server) projectMutationAdmission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutatingHTTPMethod(r.Method) || s.isProjectDeleteRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		projectID, scoped := mutationProjectFromRequest(r)
		if !scoped {
			next.ServeHTTP(w, r)
			return
		}
		release := s.projectMutations.acquire(projectID, false)
		defer release()
		// Authorization resolves the project before this gate is acquired. A
		// project delete that was already waiting may therefore commit while this
		// request is queued. Revalidate the coordinator tombstone under the shared
		// gate so the stale mutation cannot recreate project-scoped state. This is
		// deliberately narrower than an unconditional storage lookup: lightweight
		// and default-project backends preserve their existing handler validation
		// semantics, while every deletion completed by this controller is fenced.
		if s.projectMutations.wasDeleted(projectID) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) projectMutationCoordinatedAuth(auth func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		coordinated := auth(s.projectMutationAdmission(s.objectActivityMiddleware(next)))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			coordinated.ServeHTTP(w, withProjectMutationAdmissionRequest(r))
		})
	}
}

func withProjectMutationAdmissionRequest(r *http.Request) *http.Request {
	if r == nil {
		return nil
	}
	return r.WithContext(context.WithValue(r.Context(), projectMutationAdmissionContextKey{}, true))
}

func projectMutationAdmissionRequested(r *http.Request) bool {
	return r != nil && r.Context().Value(projectMutationAdmissionContextKey{}) == true
}

func isMutatingHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *Server) isProjectDeleteRequest(r *http.Request) bool {
	if s == nil || s.mux == nil || r == nil || r.Method != http.MethodDelete {
		return false
	}
	_, pattern := s.mux.Handler(r)
	return pattern == deleteProjectRoutePattern
}
