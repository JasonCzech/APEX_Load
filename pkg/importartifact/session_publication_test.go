package importartifact

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/importsession"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionPublicationFencesOldClaimAndIsCreateOnly(t *testing.T) {
	store := newEncryptedArtifactTestStore(t)
	manager, err := importsession.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d := SessionDescriptor{ID: "batch", Profile: "batch", ProjectID: "p", Actor: "actor", ExpiresAt: time.Now().Add(time.Hour)}
	if err = manager.Save(ctx, d, []byte(`{"revision":1}`), 1024); err != nil {
		t.Fatal(err)
	}
	scope := importsession.Scope{ID: d.ID, Profile: d.Profile, ProjectID: d.ProjectID, Actor: d.Actor}
	first, err := manager.Claim(ctx, scope, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Release(first.Claim); err != nil {
		t.Fatal(err)
	}
	second, err := manager.Claim(ctx, scope, 1024)
	if err != nil {
		t.Fatal(err)
	}
	receipt := d
	receipt.ID = "receipt"
	receipt.Profile = "receipt"
	claimPath := filepath.Join(store.root, ".sessions", d.ID+".claim.json")
	old := time.Now().Add(-sessionClaimLease - time.Second)
	if err := os.Chtimes(claimPath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := manager.PublishReceipt(ctx, second.Claim, receipt, []byte(`{}`), 1024); !errors.Is(err, ErrSessionClaimLost) {
		t.Fatalf("expired lease published: %v", err)
	}
	now := time.Now()
	if err := os.Chtimes(claimPath, now, now); err != nil {
		t.Fatal(err)
	}
	if err = manager.PublishReceipt(ctx, first.Claim, receipt, []byte(`{}`), 1024); !errors.Is(err, ErrSessionClaimLost) {
		t.Fatalf("stale publication: %v", err)
	}
	if err = manager.PublishReceipt(ctx, second.Claim, receipt, []byte(`{"winner":2}`), 1024); err != nil {
		t.Fatal(err)
	}
	if err = manager.PublishReceipt(ctx, second.Claim, receipt, []byte(`{"winner":3}`), 1024); err == nil {
		t.Fatal("receipt overwritten")
	}
	payload, _, err := manager.Open(ctx, importsession.Scope{ID: receipt.ID, Profile: receipt.Profile, ProjectID: receipt.ProjectID, Actor: receipt.Actor}, 1024)
	if err != nil || string(payload) != `{"winner":2}` {
		t.Fatalf("receipt changed: %s %v", payload, err)
	}
}
