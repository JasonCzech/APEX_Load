package storage

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDataPoolWritesRequireDurableParents(t *testing.T) {
	store := newProjectEntityCASStore(t)
	if err := store.SaveDataPool(nil); err == nil {
		t.Fatal("SaveDataPool(nil) succeeded")
	}
	if err := store.SaveDataPool(&SharedDataPool{ID: "missing-project-pool", ProjectID: "missing-project", Name: "missing"}); err == nil {
		t.Fatal("SaveDataPool accepted a missing project")
	}
	if err := store.SaveDataPoolRow(nil); err == nil {
		t.Fatal("SaveDataPoolRow(nil) succeeded")
	}
	if err := store.SaveDataPoolRow(&SharedDataPoolRow{ID: "missing-pool-row", PoolID: "missing-pool"}); err == nil {
		t.Fatal("SaveDataPoolRow accepted a missing pool")
	}
}

func TestConcurrentDataPoolCreateAndProjectDeleteCannotLeaveOrphan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-pool-race.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(first): %v", err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(second): %v", err)
	}
	defer second.Close()
	now := time.Now().UTC()
	if err := first.SaveProject(&Project{ID: "pool-race-project", Name: "Race", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs <- first.SaveDataPool(&SharedDataPool{ID: "pool-race", ProjectID: "pool-race-project", Name: "Race"})
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- second.DeleteProject("pool-race-project")
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err == nil {
			continue
		}
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "not found") && !strings.Contains(message, "locked") && !strings.Contains(message, "busy") {
			t.Fatalf("concurrent project/data-pool mutation error: %v", err)
		}
	}

	pool, poolErr := first.GetDataPool("pool-race")
	project, projectErr := first.GetProject("pool-race-project")
	if poolErr == nil && pool != nil && (projectErr != nil || project == nil) {
		t.Fatalf("orphan data pool survived concurrent create/delete: pool=%#v project=%#v projectErr=%v", pool, project, projectErr)
	}
	if poolErr != nil && !strings.Contains(strings.ToLower(poolErr.Error()), "no rows") && poolErr != sql.ErrNoRows {
		t.Fatalf("GetDataPool: %v", poolErr)
	}
}

func TestConcurrentDataPoolRowCreateAndPoolDeleteCannotLeaveOrphan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pool-row-race.db")
	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(first): %v", err)
	}
	defer first.Close()
	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore(second): %v", err)
	}
	defer second.Close()
	if err := first.SaveDataPool(&SharedDataPool{ID: "row-race-pool", ProjectID: DefaultProjectID, Name: "Race"}); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs <- first.SaveDataPoolRow(&SharedDataPoolRow{ID: "row-race", PoolID: "row-race-pool", Data: map[string]string{"value": "kept"}})
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- second.DeleteDataPool("row-race-pool")
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err == nil {
			continue
		}
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "not found") && !strings.Contains(message, "locked") && !strings.Contains(message, "busy") {
			t.Fatalf("concurrent pool/row mutation error: %v", err)
		}
	}

	row, rowErr := first.GetDataPoolRowFromPool("row-race-pool", "row-race")
	pool, poolErr := first.GetDataPool("row-race-pool")
	if rowErr == nil && row != nil && (poolErr != nil || pool == nil) {
		t.Fatalf("orphan data-pool row survived concurrent create/delete: row=%#v pool=%#v poolErr=%v", row, pool, poolErr)
	}
}
