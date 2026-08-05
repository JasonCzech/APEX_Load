package api

import (
	"bytes"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/internal/filelock"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestProjectDeletionReleasesProjectGateBeforeBrunoArtifactCleanup(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const projectID = "project-artifact-cleanup-lock"
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{
		ID: projectID, Name: "Artifact cleanup lock", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	artifactRoot := t.TempDir()
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key: bytes.Repeat([]byte{0x4d}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := importartifact.NewEncryptedStore(importartifact.StoreOptions{
		RootDir: artifactRoot,
		Wrapper: wrapper,
	})
	if err != nil {
		t.Fatal(err)
	}

	lockDir := filepath.Join(artifactRoot, ".project-tombstones")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	unlockArtifact, err := filelock.Acquire(filepath.Join(lockDir, ".session-claims.lock"))
	if err != nil {
		t.Fatal(err)
	}
	var unlockOnce sync.Once
	releaseArtifact := func() { unlockOnce.Do(func() { _ = unlockArtifact() }) }
	t.Cleanup(releaseArtifact)

	server := NewServer(ServerConfig{Store: store})
	server.brunoArtifacts = artifacts
	response := httptest.NewRecorder()
	deleteDone := make(chan struct{})
	go func() {
		server.mux.ServeHTTP(response, httptest.NewRequest(
			http.MethodDelete,
			"/api/v1/projects/"+projectID,
			nil,
		))
		close(deleteDone)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		_, lookupErr := store.GetProject(projectID)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			break
		}
		if lookupErr != nil {
			releaseArtifact()
			t.Fatalf("load project during deletion: %v", lookupErr)
		}
		if time.Now().After(deadline) {
			releaseArtifact()
			t.Fatal("project graph was not deleted before the cleanup deadline")
		}
		time.Sleep(time.Millisecond)
	}

	mutationAdmitted := make(chan struct{})
	go func() {
		release := server.projectMutations.acquire(projectID, false)
		close(mutationAdmitted)
		release()
	}()
	select {
	case <-mutationAdmitted:
		// The request admission layer will reject this project using its durable
		// tombstone; the lock itself must not remain hostage to artifact I/O.
	case <-time.After(500 * time.Millisecond):
		releaseArtifact()
		t.Fatal("project gate remained held during Bruno artifact cleanup")
	}

	releaseArtifact()
	select {
	case <-deleteDone:
	case <-time.After(3 * time.Second):
		t.Fatal("project deletion did not finish after artifact cleanup resumed")
	}
	if response.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
}
