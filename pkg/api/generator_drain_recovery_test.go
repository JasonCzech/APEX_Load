package api

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type drainRecoveryStore struct {
	SQLiteStore  *storage.SQLiteStore
	failRead     atomic.Bool
	failMetadata atomic.Bool
	reads        atomic.Int64
	afterRead    func()
}

func (s *drainRecoveryStore) GetSettingContext(ctx context.Context, key string) (string, error) {
	if key != generatorDrainStatesStoreKey {
		return s.SQLiteStore.GetSetting(key)
	}
	s.reads.Add(1)
	if s.failRead.Load() {
		return "", errors.New("settings unavailable")
	}
	raw, err := s.SQLiteStore.GetSetting(key)
	if s.afterRead != nil {
		s.afterRead()
	}
	return raw, err
}
func (s *drainRecoveryStore) ListGeneratorMetadata(limit, offset int) ([]*storage.GeneratorMetadata, error) {
	if s.failMetadata.Load() {
		return nil, errors.New("metadata unavailable")
	}
	return s.SQLiteStore.ListGeneratorMetadata(limit, offset)
}
func newDrainRecoveryServer(t *testing.T) (*Server, *drainRecoveryStore, *distributedExecutionBackend) {
	t.Helper()
	db, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "drains.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := &drainRecoveryStore{SQLiteStore: db}
	s := NewServer(ServerConfig{Store: sqliteOverlay(store), HAExecutionLeadership: true, GeneratorTargets: []string{"gen:9090"}})
	t.Cleanup(func() { s.leadership.relinquish(); db.Close() })
	b := s.runner.getExecutionBackend().(*distributedExecutionBackend)
	b.recordGeneratorIdentity("gen:9090", "gen-0")
	return s, store, b
}
func waitDrainRecoveryAdmission(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !s.leadership.IsAdmitted() {
		if time.Now().After(deadline) {
			t.Fatal("leadership did not finish recovery")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestGeneratorDrainRecoveryReloadsBeforeAdmission(t *testing.T) {
	s, store, b := newDrainRecoveryServer(t)
	b.ApplyGeneratorDrainStates(map[string]string{"gen-0": "draining", "removed": "cordoned"})
	b.SetTerminatingGeneratorPods([]KubePodRef{{Name: "gen-0", Terminating: true}})
	if err := store.SQLiteStore.SaveSetting(generatorDrainStatesStoreKey, `{"gen-0":"active","operator":"draining","old-cordon":"cordoned"}`); err != nil {
		t.Fatal(err)
	}
	for _, m := range []*storage.GeneratorMetadata{
		{GeneratorID: "offline", Cordoned: true}, {GeneratorID: "old-cordon", Cordoned: false}, {GeneratorID: "operator", Cordoned: false},
	} {
		if err := store.SQLiteStore.SaveGeneratorMetadata(m); err != nil {
			t.Fatal(err)
		}
	}
	s.leadership.tick()
	waitDrainRecoveryAdmission(t, s)
	expected := map[string]string{"offline": "cordoned", "operator": "draining"}
	if got := b.GeneratorDrainStates(); !reflect.DeepEqual(got, expected) {
		t.Fatalf("states=%v want %v", got, expected)
	}
	if b.GeneratorDrainStateByID("gen-0") != "draining" {
		t.Fatal("refresh removed temporary termination exclusion")
	}
	b.SetTerminatingGeneratorPods(nil)
	if b.GeneratorDrainStateByID("gen-0") != "active" {
		t.Fatal("stale startup drain returned")
	}
	// Reacquisition of the same process must also refresh.
	s.leadership.relinquish()
	if err := store.SQLiteStore.SaveSetting(generatorDrainStatesStoreKey, `{"gen-0":"cordoned"}`); err != nil {
		t.Fatal(err)
	}
	s.leadership.tick()
	waitDrainRecoveryAdmission(t, s)
	if b.GeneratorDrainStateByID("gen-0") != "cordoned" {
		t.Fatal("reacquisition missed newer cordon")
	}
}
func TestGeneratorDrainRecoveryFailureKeepsAdmissionClosed(t *testing.T) {
	for _, failure := range []string{"settings", "metadata", "invalid-json", "unknown-state", "null-state", "invalid-key"} {
		t.Run(failure, func(t *testing.T) {
			s, store, b := newDrainRecoveryServer(t)
			b.ApplyGeneratorDrainStates(map[string]string{"gen-0": "cordoned"})
			raw := `{}`
			switch failure {
			case "settings":
				store.failRead.Store(true)
			case "metadata":
				store.failMetadata.Store(true)
			case "invalid-json":
				raw = `[`
			case "unknown-state":
				raw = `{"gen-0":"unknown"}`
			case "null-state":
				raw = `{"gen-0":null}`
			case "invalid-key":
				raw = `{" gen-0":"draining"}`
			}
			if err := store.SQLiteStore.SaveSetting(generatorDrainStatesStoreKey, raw); err != nil {
				t.Fatal(err)
			}
			s.leadership.tick()
			deadline := time.Now().Add(3 * time.Second)
			for store.reads.Load() < 2 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if store.reads.Load() < 2 {
				t.Fatal("refresh did not retry")
			}
			if s.leadership.IsAdmitted() {
				t.Fatal("failed refresh admitted work")
			}
			if b.GeneratorDrainStateByID("gen-0") != "cordoned" {
				t.Fatal("failed refresh changed operator intent")
			}
			store.failRead.Store(false)
			store.failMetadata.Store(false)
			if err := store.SQLiteStore.SaveSetting(generatorDrainStatesStoreKey, `{}`); err != nil {
				t.Fatal(err)
			}
			waitDrainRecoveryAdmission(t, s)
			if b.GeneratorDrainStateByID("gen-0") != "active" {
				t.Fatal("repaired refresh did not apply")
			}
		})
	}
}
func TestGeneratorDrainRecoveryRejectsCanceledRead(t *testing.T) {
	s, store, b := newDrainRecoveryServer(t)
	// Use a canceled read directly: even an optional store implementation that
	// returns successful data after cancellation cannot clear existing intent.
	ctx, cancel := context.WithCancel(context.Background())
	store.afterRead = cancel
	if _, err := readGeneratorDrainStates(ctx, sqliteOverlay(store)); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	store.afterRead = nil
	b.ApplyGeneratorDrainStates(map[string]string{"gen-0": "cordoned"})
	if err := s.reloadGeneratorDrainStatesOnLeadershipAcquire(); err == nil {
		t.Fatal("follower installed a snapshot")
	}
	if b.GeneratorDrainStateByID("gen-0") != "cordoned" {
		t.Fatal("follower refresh mutated state")
	}
}
func TestGeneratorReconnectDoesNotPersistStandbySnapshot(t *testing.T) {
	s, store, b := newDrainRecoveryServer(t)
	b.ApplyGeneratorDrainStates(map[string]string{"stale": "draining"})
	latest := `{"newer":"draining"}`
	if err := store.SQLiteStore.SaveSetting(generatorDrainStatesStoreKey, latest); err != nil {
		t.Fatal(err)
	}
	if err := store.SQLiteStore.SaveGeneratorMetadata(&storage.GeneratorMetadata{GeneratorID: "gen-0", Cordoned: true}); err != nil {
		t.Fatal(err)
	}
	s.applyMetadataCordonOnReconnect(b, "gen-0")
	if b.GeneratorDrainStateByID("gen-0") != "cordoned" {
		t.Fatal("local metadata projection not updated")
	}
	raw, err := store.SQLiteStore.GetSetting(generatorDrainStatesStoreKey)
	if err != nil || raw != latest {
		t.Fatalf("reconnect overwrote newer durable state: %s err=%v", raw, err)
	}
}
