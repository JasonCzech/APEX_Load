package importartifact

import (
	"context"
	"github.com/jasonczech/apexload/pkg/importsession"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PublishClaimReceipt publishes a create-only receipt while holding the same
// cross-process lock used by claim acquisition and expiry recovery. A stale
// holder can never publish after a new claim epoch has replaced it.
func (s *EncryptedStore) PublishClaimReceipt(ctx context.Context, scope importsession.Scope, token string, receipt SessionDescriptor, payload []byte, maxBytes int64) error {
	if !validSessionID(scope.ID) {
		return ErrSessionClaimLost
	}
	unlock, err := s.lockSessionClaims()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	claimPath := filepath.Join(s.root, ".sessions", strings.TrimSpace(scope.ID)+".claim.json")
	currentLease := func() error {
		info, err := os.Stat(claimPath)
		if err != nil {
			return err
		}
		if time.Since(info.ModTime()) > sessionClaimLease {
			return ErrSessionClaimLost
		}
		return nil
	}
	if err := currentLease(); err != nil {
		return err
	}
	_, claimed, err := readSessionDescriptorFile(claimPath)
	if err != nil {
		return err
	}
	if err = validateSessionClaimDescriptor(claimed, scope.ID, scope.ProjectID, scope.Actor, token); err != nil {
		return err
	}
	if claimed.Profile != scope.Profile || receipt.ProjectID != scope.ProjectID || receipt.Actor != scope.Actor || !claimed.ExpiresAt.After(time.Now().UTC()) {
		return ErrSessionClaimLost
	}
	planned, err := s.PlanBytes(receipt.ProjectID, "session", payload, maxBytes)
	if err != nil {
		return err
	}
	receipt.BackendKey = planned.BackendKey
	receipt.SHA256 = planned.SHA256
	receipt.SizeBytes = planned.SizeBytes
	return s.saveSessionLocked(ctx, receipt, func() (*StoredObject, error) {
		object, err := s.PutBytes(ctx, receipt.ProjectID, "session", payload, maxBytes)
		if err != nil {
			return nil, err
		}
		if err = currentLease(); err != nil {
			return nil, err
		}
		return object, nil
	})
}
