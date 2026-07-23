package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func resetPersistentJavaCapacityForTest(t *testing.T) {
	t.Helper()
	persistentJavaCapacity.Lock()
	defer persistentJavaCapacity.Unlock()
	if persistentJavaCapacity.reserved != 0 {
		t.Fatalf("persistent Java capacity leaked from another run: %d", persistentJavaCapacity.reserved)
	}
	if len(persistentJavaCapacity.reservations) != 0 {
		t.Fatalf("persistent Java reservations leaked from another run: %d", len(persistentJavaCapacity.reservations))
	}
	if persistentJavaCapacity.legacyRuns != 0 {
		t.Fatalf("legacy Java runs leaked from another run: %d", persistentJavaCapacity.legacyRuns)
	}
	persistentJavaCapacity.limit = 0
	persistentJavaCapacity.reservations = make(map[*javaVuserCapacityReservation]struct{})
}

func TestPersistentJavaCapacityReservationsAreProcessWide(t *testing.T) {
	resetPersistentJavaCapacityForTest(t)
	t.Cleanup(func() { resetPersistentJavaCapacityForTest(t) })

	first, err := reserveJavaVuserCapacity(2, 3)
	if err != nil {
		t.Fatalf("reserve first run: %v", err)
	}
	defer first.Release()
	if _, err := reserveJavaVuserCapacity(2, 3); err == nil {
		t.Fatal("second run exceeded the shared process limit")
	}
	second, err := reserveJavaVuserCapacity(1, 5)
	if err != nil {
		t.Fatalf("reserve remaining shared slot: %v", err)
	}
	second.Release()

	zero, err := reserveJavaVuserCapacity(0, 3)
	if err != nil {
		t.Fatalf("reserve zero-start group: %v", err)
	}
	defer zero.Release()
	if err := zero.Resize(1); err != nil {
		t.Fatalf("grow zero-start reservation: %v", err)
	}
}

func TestPersistentAndLegacyJavaRunsCannotShareProcessSlots(t *testing.T) {
	resetPersistentJavaCapacityForTest(t)
	t.Cleanup(func() { resetPersistentJavaCapacityForTest(t) })

	legacy, err := reserveLegacyJavaVuserRun()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reserveJavaVuserCapacity(1, 5); err == nil {
		t.Fatal("persistent reservation was admitted while a legacy run could acquire process slots")
	}
	legacy.Release()

	persistent, err := reserveJavaVuserCapacity(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reserveLegacyJavaVuserRun(); err == nil {
		t.Fatal("legacy run was admitted while a zero-start persistent lifecycle run owned the process-slot mode")
	}
	persistent.Release()
}

func TestMultiGroupRejectsMixedJavaLifecycleModes(t *testing.T) {
	persistent := &dsl.Script{
		Name:     "persistent",
		Protocol: "java_vuser",
		Actions:  []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "action"}}},
	}
	legacy := &dsl.Script{
		Name:     "legacy",
		Protocol: "java_vuser",
		Actions:  []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "full_lifecycle"}}},
	}
	if _, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups: []GroupConfig{
			{Name: "persistent", Script: persistent, VUsers: 1},
			{Name: "legacy", Script: legacy, VUsers: 1},
		},
	}); err == nil {
		t.Fatal("mixed persistent and legacy Java groups were admitted to shared process slots")
	}
}

func TestEngineConstructorsRejectMixedJavaLifecycleModesWithinScript(t *testing.T) {
	mixed := &dsl.Script{
		Name:     "mixed",
		Protocol: "java_vuser",
		Init: []dsl.Action{{
			Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "init"},
		}},
		Actions: []dsl.Action{{
			Type: "java_vuser_run", Target: "Actions", Params: map[string]any{"method": "full_lifecycle"},
		}},
	}
	if _, err := New(Config{Script: mixed, VUsers: 1, Duration: time.Second}); err == nil {
		t.Fatal("Engine admitted a script that mixes persistent and legacy lifecycle calls")
	}
	if _, err := NewMultiGroup(MultiGroupConfig{
		Duration: time.Second,
		Groups:   []GroupConfig{{Name: "mixed", Script: mixed, VUsers: 1}},
	}); err == nil {
		t.Fatal("MultiGroupEngine admitted a script that mixes persistent and legacy lifecycle calls")
	}
}

func TestZeroStartReservationTightensProcessWideLimit(t *testing.T) {
	resetPersistentJavaCapacityForTest(t)
	t.Cleanup(func() { resetPersistentJavaCapacityForTest(t) })

	active, err := reserveJavaVuserCapacity(2, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Release()
	zero, err := reserveJavaVuserCapacity(0, 2)
	if err != nil {
		t.Fatalf("reserve zero-start runtime: %v", err)
	}
	if err := active.Resize(3); err == nil {
		t.Fatal("active run grew beyond a tighter limit observed by a zero-start run")
	}
	zero.Release()
	if err := active.Resize(3); err != nil {
		t.Fatalf("active run did not recover capacity after tighter zero-start reservation ended: %v", err)
	}
}

func TestPersistentJavaScaleUsesTighterObservedRuntimeLimit(t *testing.T) {
	resetPersistentJavaCapacityForTest(t)
	t.Cleanup(func() { resetPersistentJavaCapacityForTest(t) })

	reservation, err := reserveJavaVuserCapacity(2, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	if err := reservation.resizeWithinLimit(3, 2); err == nil {
		t.Fatal("scale-up ignored the runtime's reduced capacity")
	}
	if reservation.amount != 2 {
		t.Fatalf("failed scale changed reservation to %d, want 2", reservation.amount)
	}
}

func TestPersistentJavaCapacityReleasesOnlyAfterLiveVUserTeardown(t *testing.T) {
	resetPersistentJavaCapacityForTest(t)
	t.Cleanup(func() { resetPersistentJavaCapacityForTest(t) })

	script := &dsl.Script{
		Protocol: "java_vuser",
		Actions:  []dsl.Action{{Type: "java_vuser_run", Params: map[string]any{"method": "action"}}},
	}
	reservation, err := reserveJavaVuserCapacity(5, 5)
	if err != nil {
		t.Fatal(err)
	}
	mge := &MultiGroupEngine{
		groups:               []GroupConfig{{Name: "java", Script: script, VUsers: 3}},
		vuserGroups:          map[int]string{0: "java", 1: "java", 2: "java", 3: "java", 4: "java"},
		vuserChanged:         make(chan struct{}),
		javaVuserReservation: reservation,
	}
	if err := mge.reconcileJavaVuserCapacityLocked(); err != nil {
		t.Fatal(err)
	}
	if reservation.amount != 5 {
		t.Fatalf("reservation shrank before old VUsers reaped: %d", reservation.amount)
	}

	delete(mge.vuserGroups, 3)
	delete(mge.vuserGroups, 4)
	if err := mge.reconcileJavaVuserCapacityLocked(); err != nil {
		t.Fatal(err)
	}
	if reservation.amount != 3 {
		t.Fatalf("reservation after teardown = %d, want 3", reservation.amount)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := mge.WaitForGroupVUsers(waitCtx, "java", 3); err != nil {
		t.Fatalf("WaitForGroupVUsers: %v", err)
	}
	reservation.Release()
	mge.javaVuserReservation = nil
}
