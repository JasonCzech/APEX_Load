package api

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

type runArchiveCollectionStore interface {
	PrepareRunArchiveCollection(string, string, time.Time, map[string]string, string, string) (*storage.RunArchiveCollectionIntent, bool, error)
	GetRunArchiveCollection(string) (*storage.RunArchiveCollectionIntent, error)
	ListRunArchiveCollections(int, time.Time) ([]*storage.RunArchiveCollectionIntent, error)
	ClaimRunArchiveCollectionAttempt(string, string, int, string, string) (bool, error)
	ExhaustRunArchiveCollection(string, string, int, string, string) (bool, error)
}

func (tr *TestRunner) archiveCollectionStore() runArchiveCollectionStore {
	if tr == nil {
		return nil
	}
	store, _ := tr.testStore.(runArchiveCollectionStore)
	return store
}

func (tr *TestRunner) runArchiveCollectionIntent(test *ManagedTest) (*storage.RunArchiveCollectionIntent, error) {
	store := tr.archiveCollectionStore()
	if store == nil || test == nil {
		return nil, nil
	}
	tr.mu.RLock()
	id, project, created := test.ID, test.ProjectID, test.CreatedAt
	tr.mu.RUnlock()
	job, err := store.GetRunArchiveCollection(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if job == nil || normalizeProjectID(job.ProjectID) != normalizeProjectID(project) || !archiveRunTimestampMatches(job.StartTime, created, tr.testStore) {
		return nil, errors.New("archive collection belongs to a different run incarnation")
	}
	return job, nil
}

// Record expected source identity before dispatch. The digest is tied to the
// original assignment on each target, so a later adoption token cannot rebind it.
func (tr *TestRunner) prepareRunArchiveSources(test *ManagedTest, targets []string, generation uint64, token []byte) error {
	store := tr.archiveCollectionStore()
	if store == nil || len(token) == 0 {
		return nil
	}
	if test == nil {
		return errors.New("archive collection test is required")
	}
	tr.mu.RLock()
	id, project, created := test.ID, test.ProjectID, test.CreatedAt
	tr.mu.RUnlock()
	job, err := tr.runArchiveCollectionIntent(test)
	if err != nil {
		return err
	}
	sources := make(map[string]string, len(targets))
	for _, target := range targets {
		sources[target] = generatorrpc.ArchiveSourceIdentity(id, token, target)
		if sources[target] == "" {
			return errors.New("archive collection requires a valid supervised target assignment")
		}
		if job != nil && job.Sources[target] != "" {
			sources[target] = job.Sources[target]
		}
	}
	if len(sources) == 0 {
		return nil
	}
	lease, owner := "", ""
	if tr.durableExecutionFence {
		current, holder, _, err := tr.generatorExecutionClaim()
		if err != nil || current != generation {
			return errNotExecutionLeader
		}
		lease, owner = executionLeaseName, holder
	}
	var saved bool
	err = tr.trackDurableWrite(func() error {
		var err error
		_, saved, err = store.PrepareRunArchiveCollection(id, project, created, sources, lease, owner)
		return err
	})
	if err != nil {
		return fmt.Errorf("persist archive collection intent: %w", err)
	}
	if !saved {
		return errors.New("archive collection intent was fenced or the run is no longer active")
	}
	return nil
}

// Extend only an already-bound run during evacuation. A run started by an older
// controller has no durable original source identities; creating a partial map
// here would incorrectly exclude its original generators from collection.
func (tr *TestRunner) extendRunArchiveSources(test *ManagedTest, targets []string, generation uint64, token []byte) error {
	intent, err := tr.runArchiveCollectionIntent(test)
	if err != nil {
		return err
	}
	if intent == nil {
		return nil
	}
	if len(token) == 0 {
		return errors.New("bound archive collection requires the supervised assignment token")
	}
	return tr.prepareRunArchiveSources(test, targets, generation, token)
}

func (tr *TestRunner) archiveSourceTargetForRun(test *ManagedTest, target string) (string, error) {
	intent, err := tr.runArchiveCollectionIntent(test)
	if err != nil || intent == nil {
		return "", err
	}
	if intent.Sources[target] == "" {
		return "", errors.New("archive target has no durable source identity")
	}
	return target, nil
}

func (tr *TestRunner) bindRunArchiveSourcePlans(test *ManagedTest, plans []distributedTargetAssignmentPlan) error {
	intent, err := tr.runArchiveCollectionIntent(test)
	if err != nil || intent == nil {
		return err
	}
	for i := range plans {
		if intent.Sources[plans[i].target] == "" {
			return errors.New("archive target has no durable source identity")
		}
		plans[i].archiveSourceTarget = plans[i].target
	}
	return nil
}
