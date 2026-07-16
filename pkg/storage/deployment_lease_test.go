package storage

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newDeploymentLeaseTestStores(t *testing.T) (*SQLiteStore, *SQLiteStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deployment-lease.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSQLiteStore(path)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	return first, second
}

func TestDeploymentLeaseBlocksActiveTestsUnlessAtomicallyApproved(t *testing.T) {
	store, _ := newDeploymentLeaseTestStores(t)
	now := time.Now().UTC()
	active := &TestRecord{ID: "active-run", Name: "active", Status: "RUNNING", ProjectID: DefaultProjectID, StartTime: now}
	if err := store.SaveTest(active); err != nil {
		t.Fatal(err)
	}
	if lease, count, err := store.AcquireDeploymentLease(DefaultDeploymentLeaseName, "owner-a", "hash-a", time.Minute, DeploymentLeaseContext{}, false); !errors.Is(err, ErrDeploymentLeaseActiveTests) || count != 1 || lease == nil {
		t.Fatalf("unapproved acquire = (%+v, %d, %v)", lease, count, err)
	}
	context := DeploymentLeaseContext{Release: "v1.2.3", Environment: "aks-production", ApprovalID: "CHG-123"}
	lease, count, err := store.AcquireDeploymentLease(DefaultDeploymentLeaseName, "owner-a", "hash-a", time.Minute, context, true)
	if err != nil || count != 1 || lease == nil || !lease.Active || lease.Context.ApprovalID != "CHG-123" {
		t.Fatalf("approved acquire = (%+v, %d, %v)", lease, count, err)
	}

	queued := &TestRecord{ID: "new-run", Name: "new", Status: "QUEUED", ProjectID: DefaultProjectID, StartTime: now}
	if err := store.SaveTest(queued); err != nil {
		t.Fatal(err)
	}
	queued.Status = "INITIALIZING"
	if err := store.SaveTestStartIfNoDeploymentLease(queued, DefaultDeploymentLeaseName, "", ""); !errors.Is(err, ErrDeploymentLeaseActive) {
		t.Fatalf("unprivileged start error = %v", err)
	}
	if err := store.SaveTestStartIfNoDeploymentLease(queued, DefaultDeploymentLeaseName, "owner-a", "wrong-hash"); !errors.Is(err, ErrDeploymentLeaseActive) {
		t.Fatalf("wrong-token start error = %v", err)
	}
	if err := store.SaveTestStartIfNoDeploymentLease(queued, DefaultDeploymentLeaseName, "owner-a", "hash-a"); err != nil {
		t.Fatalf("lease-authorized smoke start: %v", err)
	}
}

func TestDeploymentLeaseOwnershipTTLAndTokenConfidentiality(t *testing.T) {
	store, _ := newDeploymentLeaseTestStores(t)
	secretHash := "hashed-secret-only"
	lease, _, err := store.AcquireDeploymentLease(DefaultDeploymentLeaseName, "owner-a", secretHash, 20*time.Millisecond, DeploymentLeaseContext{ApprovalID: "CHG-1"}, false)
	if err != nil || lease == nil {
		t.Fatalf("acquire: %+v, %v", lease, err)
	}
	if lease.TokenHash != secretHash {
		t.Fatalf("stored token hash = %q", lease.TokenHash)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := store.RenewDeploymentLease(DefaultDeploymentLeaseName, "owner-a", secretHash, time.Minute); !errors.Is(err, ErrDeploymentLeaseExpired) {
		t.Fatalf("renew expired error = %v", err)
	}
	if _, err := store.ReleaseDeploymentLease(DefaultDeploymentLeaseName, "owner-a", secretHash); !errors.Is(err, ErrDeploymentLeaseExpired) {
		t.Fatalf("release expired error = %v", err)
	}
}

func TestDeploymentLeaseReleaseIsIdempotentForTheSameOwnershipProof(t *testing.T) {
	store, _ := newDeploymentLeaseTestStores(t)
	const (
		ownerID   = "owner-release-retry"
		tokenHash = "hashed-release-token"
	)
	context := DeploymentLeaseContext{Release: "v1.2.3", Environment: "production", RunID: "9001"}
	acquired, _, err := store.AcquireDeploymentLease(DefaultDeploymentLeaseName, ownerID, tokenHash, time.Minute, context, false)
	if err != nil || acquired == nil || !acquired.Active {
		t.Fatalf("acquire = (%+v, %v)", acquired, err)
	}

	first, err := store.ReleaseDeploymentLease(DefaultDeploymentLeaseName, ownerID, tokenHash)
	if err != nil || first == nil || first.Active {
		t.Fatalf("first release = (%+v, %v)", first, err)
	}
	second, err := store.ReleaseDeploymentLease(DefaultDeploymentLeaseName, ownerID, tokenHash)
	if err != nil || second == nil || second.Active {
		t.Fatalf("retry release = (%+v, %v)", second, err)
	}
	if second.OwnerID != ownerID || second.TokenHash != tokenHash || second.Context != context {
		t.Fatalf("retry-safe release lost its durable ownership marker: %+v", second)
	}
	if !second.ExpiresAt.Equal(second.UpdatedAt) {
		t.Fatalf("explicit release marker expires_at=%s updated_at=%s", second.ExpiresAt, second.UpdatedAt)
	}
}

func TestDeploymentLeaseAndTestStartAreSerializedAcrossStores(t *testing.T) {
	acquirer, starter := newDeploymentLeaseTestStores(t)
	record := &TestRecord{ID: "race-run", Name: "race", Status: "QUEUED", ProjectID: DefaultProjectID, StartTime: time.Now().UTC()}
	if err := starter.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	record.Status = "INITIALIZING"
	var acquireErr, startErr error
	var wg sync.WaitGroup
	ready := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-ready
		_, _, acquireErr = acquirer.AcquireDeploymentLease(DefaultDeploymentLeaseName, "owner-race", "hash-race", time.Minute, DeploymentLeaseContext{}, false)
	}()
	go func() {
		defer wg.Done()
		<-ready
		startErr = starter.SaveTestStartIfNoDeploymentLease(record, DefaultDeploymentLeaseName, "", "")
	}()
	close(ready)
	wg.Wait()
	if acquireErr == nil && startErr == nil {
		t.Fatal("deployment acquisition and a new test start both committed")
	}
	if acquireErr != nil && !errors.Is(acquireErr, ErrDeploymentLeaseActiveTests) {
		t.Fatalf("acquire error = %v", acquireErr)
	}
	if startErr != nil && !errors.Is(startErr, ErrDeploymentLeaseActive) {
		t.Fatalf("start error = %v", startErr)
	}
}
