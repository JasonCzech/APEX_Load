package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// This is a shared-directory process-loss simulation, not a CSI/node-loss test.
func TestArchiveSharedStagingSurvivesProcessLoss(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL", "")
	db := filepath.Join(t.TempDir(), "shared.db")
	store, err := storage.NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run, record := createArchiveFinalizationTest(t, store, "shared-staging-process-loss", root)
	job := &storage.ArchiveFinalizationJob{TestID: run.ID, ProjectID: run.ProjectID, ArchiveRecord: *record, PersistArtifacts: true}
	if err := store.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_TEST_STAGING_DB", db)
	t.Setenv("APEXLOAD_TEST_STAGING_RUN", run.ID)
	child := func(mode string) (*exec.Cmd, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArchiveSharedStagingProcessHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "APEXLOAD_TEST_STAGING_MODE="+mode)
		return cmd, cancel
	}
	holder, cancel := child("interrupted-owner")
	defer cancel()
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	holder.Stderr = &stderr
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = holder.Process.Kill()
			_ = holder.Wait()
		}
		if t.Failed() {
			t.Log(stderr.String())
		}
	}()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "source-locked" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("child did not acquire source lock")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("source owner did not start")
	}
	pending, err := store.GetArchiveFinalizationJob(run.ID)
	if err != nil || pending.PreparedManifestJSON == "" {
		t.Fatalf("missing prepared journal: %v", err)
	}
	before, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(resolveArchiveRoot(), "_source_locks", sha256BytesHex([]byte(run.ID))+".lock")
	inode, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = NewTestRunner(1, NewHub(), store).processArchiveFinalizationJob(ctx, pending)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent publication bypassed owner: %v", err)
	}
	after, err := os.ReadFile(record.ManifestPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("blocked contender mutated source")
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := holder.Wait(); err == nil {
		t.Fatal("crashed process unexpectedly exited successfully")
	}
	waited = true
	for i := 0; i < 2; i++ {
		recovered, stop := child("recover")
		out, err := recovered.CombinedOutput()
		stop()
		if err != nil {
			t.Fatalf("recovery process %d: %v\n%s", i, err, out)
		}
		catalog, err := store.GetRunArchive(run.ID)
		if err != nil || catalog == nil {
			t.Fatalf("catalog missing: %v", err)
		}
		manifest, err := os.ReadFile(catalog.ManifestPath)
		if err != nil || sha256BytesHex(manifest) != catalog.ManifestSHA256 {
			t.Fatal("recovered catalog is not bound to saved bytes")
		}
		if i == 0 {
			before = manifest
		} else if !bytes.Equal(before, manifest) {
			t.Fatal("completed replay mutated source")
		}
	}
	if _, err := store.GetArchiveFinalizationJob(run.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("publication intent remains: %v", err)
	}
	current, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(inode, current) {
		t.Fatal("source lock inode was replaced")
	}
	row, err := store.GetTest(run.ID)
	if err != nil || row.Status != run.Status || row.EndTime == nil || !row.EndTime.Equal(*run.EndedAt) {
		t.Fatal("recovery changed terminal outcome")
	}
}

func TestArchiveSharedStagingProcessHelper(t *testing.T) {
	mode := os.Getenv("APEXLOAD_TEST_STAGING_MODE")
	if mode == "" {
		return
	}
	store, err := storage.NewSQLiteStore(os.Getenv("APEXLOAD_TEST_STAGING_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := os.Getenv("APEXLOAD_TEST_STAGING_RUN")
	job, err := store.GetArchiveFinalizationJob(id)
	if errors.Is(err, sql.ErrNoRows) && mode == "recover" {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if mode == "recover" {
		if err := NewTestRunner(1, NewHub(), store).processArchiveFinalizationJob(context.Background(), job); err != nil {
			t.Fatal(err)
		}
		return
	}
	if mode != "interrupted-owner" {
		t.Fatal("invalid helper mode")
	}
	failing := &failingArchiveCatalogStore{SQLiteStore: store, failCatalog: true}
	if err := NewTestRunner(1, NewHub(), failing).processArchiveFinalizationJob(context.Background(), job); err == nil {
		t.Fatal("catalog interruption not injected")
	}
	release, err := lockArchivePublicationSource(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fmt.Println("source-locked")
	<-time.After(time.Minute)
	t.Fatal("parent did not stop interrupted owner")
}
