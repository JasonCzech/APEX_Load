package api

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
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

type runArchiveReplicaHoldStore interface {
	HoldRunArchiveReplicaFloor(string, string, string, string, string, int, string, string) (bool, error)
}

type archiveReplicaHold struct {
	fleet, namespace, workloadUID string
	floor                         int
}

func archiveReplicaHoldRequired() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(generatorAutoscalingOwnerEnvVar)), "keda")
}

func (tr *TestRunner) archiveReplicaHoldForTargets(targets []string) (*archiveReplicaHold, error) {
	fleet := strings.TrimSpace(os.Getenv("APEXLOAD_FLEET_ID"))
	if !autoscalingFleetIDPattern.MatchString(fleet) || strings.Contains(strings.ToLower(fleet), "change-me") {
		if archiveReplicaHoldRequired() {
			return nil, errors.New("KEDA archive source hold requires a valid fleet identity")
		}
		return nil, nil
	}
	entries, at := tr.cachedGeneratorInventorySnapshot()
	if at.IsZero() || len(entries) == 0 {
		if archiveReplicaHoldRequired() {
			return nil, errors.New("KEDA archive source hold requires fresh generator inventory")
		}
		return nil, nil
	}
	byTarget := make(map[string]GeneratorInventoryEntry, len(entries))
	autoscaled := false
	for _, entry := range entries {
		if _, exists := byTarget[entry.Target]; exists {
			return nil, errors.New("archive replica hold requires unambiguous generator inventory")
		}
		byTarget[entry.Target] = entry
		if p := entry.PodPlacement; p != nil && p.scaleLimit.WorkloadUID != "" {
			autoscaled = true
		}
	}
	if !autoscaled {
		if archiveReplicaHoldRequired() {
			return nil, errors.New("KEDA archive source hold requires identified autoscaling workload")
		}
		return nil, nil
	}
	var hold archiveReplicaHold
	hold.fleet = fleet
	for _, target := range targets {
		p := byTarget[target].PodPlacement
		if p == nil || !p.Ready || p.Terminating || p.UID == "" || p.Name == "" {
			return nil, fmt.Errorf("archive source %q lacks current Kubernetes placement", target)
		}
		limit := p.scaleLimit
		if limit.WorkloadUID == "" || limit.Namespace != p.Namespace || limit.MaximumReplicas <= 0 || limit.ObservedAt.IsZero() || limit.ObservedAt.After(time.Now()) || time.Since(limit.ObservedAt) > defaultGeneratorSnapshotTTL {
			return nil, fmt.Errorf("archive source %q lacks current autoscaling workload proof", target)
		}
		ordinal, err := strconv.Atoi(strings.TrimPrefix(p.Name, limit.Workload+"-"))
		if err != nil || ordinal < 0 || ordinal >= limit.MaximumReplicas || p.Name != limit.Workload+"-"+strconv.Itoa(ordinal) {
			return nil, fmt.Errorf("archive source %q has invalid StatefulSet ordinal", target)
		}
		if hold.workloadUID != "" && (hold.workloadUID != limit.WorkloadUID || hold.namespace != limit.Namespace) {
			return nil, errors.New("archive sources span autoscaling workloads")
		}
		hold.namespace, hold.workloadUID = limit.Namespace, limit.WorkloadUID
		hold.floor = max(hold.floor, ordinal+1)
	}
	return &hold, nil
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
		if archiveReplicaHoldRequired() {
			return errors.New("KEDA archive source hold requires durable collection and supervised assignment")
		}
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
	holdStore, canHold := tr.testStore.(runArchiveReplicaHoldStore)
	if !canHold && archiveReplicaHoldRequired() {
		return errors.New("KEDA archive source hold requires durable storage support")
	}
	var hold *archiveReplicaHold
	if canHold {
		hold, err = tr.archiveReplicaHoldForTargets(targets)
		if err != nil {
			return err
		}
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
	var savedIntent *storage.RunArchiveCollectionIntent
	err = tr.trackDurableWrite(func() error {
		var err error
		savedIntent, saved, err = store.PrepareRunArchiveCollection(id, project, created, sources, lease, owner)
		return err
	})
	if err != nil {
		return fmt.Errorf("persist archive collection intent: %w", err)
	}
	if !saved {
		return errors.New("archive collection intent was fenced or the run is no longer active")
	}
	if hold != nil {
		if savedIntent == nil || savedIntent.IntentToken == "" {
			return errors.New("archive collection did not return its durable intent token")
		}
		var retained bool
		err = tr.trackDurableWrite(func() error {
			var err error
			retained, err = holdStore.HoldRunArchiveReplicaFloor(id, savedIntent.IntentToken, hold.fleet, hold.namespace, hold.workloadUID, hold.floor, lease, owner)
			return err
		})
		if err != nil {
			return fmt.Errorf("persist archive replica hold: %w", err)
		}
		if !retained {
			return errors.New("archive replica hold was fenced or its collection intent closed")
		}
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
