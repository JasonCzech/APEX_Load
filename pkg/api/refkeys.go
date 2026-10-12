package api

// Human-readable reference keys (refkeys): per-environment monotonic sequence
// allocation for scripts/scenarios/runs, an in-memory key→id index, and
// one-time backfill of refkeys onto pre-existing records. Extracted from
// runner.go. Index and script/scenario helpers retain the caller-holds-mu
// contract denoted by "Locked". Run sequence reservation is the exception: it
// uses a shared dedicated allocator so settings I/O never runs under tr.mu.

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/refkey"
)

type testRefKeyPersistence interface {
	EnsureTestRefKey(id, proposedRefKey string) (string, error)
}

type scriptRefKeyPersistence interface {
	EnsureScriptRefKey(id, proposedRefKey string) (string, time.Time, error)
}

type scenarioRefKeyPersistence interface {
	EnsureScenarioRefKey(id, proposedRefKey string) (string, time.Time, error)
}

type archiveRefKeyPersistence interface {
	EnsureRunArchiveRefKey(testID, proposedRefKey string) (string, error)
}

func refSeqSettingKey(envCode string, typ string) string {
	return fmt.Sprintf("refseq:%s:%s", envCode, typ)
}

func refRunSeqSettingKey(envCode string, day string) string {
	return fmt.Sprintf("refseq:%s:%s:%s", envCode, refkey.TypeRun, day)
}

func (tr *TestRunner) initRefKeys() {
	tr.envCode = refkey.EnvCode(buildinfo.Environment())
	tr.runRefSeqAllocator = newRunReferenceSequenceAllocator(tr.envCode, tr.settingsStore)
	tr.refKeyIndex = make(map[string]string)

	tr.refScriptSeq = tr.loadRefSeqSettingLocked(refSeqSettingKey(tr.envCode, refkey.TypeScript))
	tr.refScenarioSeq = tr.loadRefSeqSettingLocked(refSeqSettingKey(tr.envCode, refkey.TypeScenario))
	tr.scriptRefSeqAllocator = newArtifactReferenceSequenceAllocator(
		refSeqSettingKey(tr.envCode, refkey.TypeScript), tr.refScriptSeq, tr.settingsStore,
	)
	tr.scenarioRefSeqAllocator = newArtifactReferenceSequenceAllocator(
		refSeqSettingKey(tr.envCode, refkey.TypeScenario), tr.refScenarioSeq, tr.settingsStore,
	)

	for _, script := range tr.scripts {
		if script == nil {
			continue
		}
		tr.observeAndIndexRefKeyLocked(script.RefKey, script.ID)
	}
	for _, scenario := range tr.scenarios {
		if scenario == nil {
			continue
		}
		tr.observeAndIndexRefKeyLocked(scenario.RefKey, scenario.ID)
	}
	for _, test := range tr.tests {
		if test == nil {
			continue
		}
		tr.observeAndIndexRefKeyLocked(test.RefKey, test.ID)
	}

	tr.backfillScriptRefKeysLocked()
	tr.backfillScenarioRefKeysLocked()
	tr.backfillTestRefKeysLocked()
	tr.backfillArchiveRefKeysLocked()
}

func (tr *TestRunner) loadRefSeqSettingLocked(key string) int {
	if tr.settingsStore == nil {
		return 0
	}
	raw, err := tr.settingsStore.GetSetting(key)
	if err != nil {
		slog.Warn("failed to load reference-key sequence", "key", key, "error", err)
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (tr *TestRunner) saveRefSeqSettingLocked(key string, value int) error {
	if tr.settingsStore == nil {
		return nil
	}
	// Runs with tr.mu held, so the settings write is published to the panic
	// backstop's in-flight gauge; see beginLockHeldDurableWrite.
	tr.beginLockHeldDurableWrite()
	defer tr.endLockHeldDurableWrite()
	if err := tr.settingsStore.SaveSetting(key, strconv.Itoa(value)); err != nil {
		return err
	}
	return nil
}

type atomicReferenceSequenceStore interface {
	NextSettingSequence(key string) (int, error)
}

type atomicReferenceSequenceFloorStore interface {
	EnsureSettingSequenceAtLeast(key string, minimum int) (int, error)
}

// artifactReferenceSequenceAllocator keeps script/scenario sequence storage I/O
// independent of TestRunner.mu. Its mutex preserves the old single-controller
// serialization for compatibility stores that do not implement atomic sequence
// allocation; production stores still provide cross-replica uniqueness.
type artifactReferenceSequenceAllocator struct {
	mu      sync.Mutex
	key     string
	store   settingsPersistence
	current int
}

func newArtifactReferenceSequenceAllocator(key string, current int, store settingsPersistence) *artifactReferenceSequenceAllocator {
	return &artifactReferenceSequenceAllocator{key: key, current: current, store: store}
}

func (allocator *artifactReferenceSequenceAllocator) next() (int, error) {
	if allocator == nil {
		return 0, fmt.Errorf("artifact reference sequence allocator is not initialized")
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	if store, ok := allocator.store.(atomicReferenceSequenceStore); ok {
		if allocator.current > 0 {
			if floorStore, floorOK := allocator.store.(atomicReferenceSequenceFloorStore); floorOK {
				floor, err := floorStore.EnsureSettingSequenceAtLeast(allocator.key, allocator.current)
				if err != nil {
					return 0, err
				}
				if floor > allocator.current {
					allocator.current = floor
				}
			}
		}
		next, err := store.NextSettingSequence(allocator.key)
		for err == nil && next <= allocator.current {
			next, err = store.NextSettingSequence(allocator.key)
		}
		if err == nil {
			allocator.current = next
		}
		return next, err
	}
	current := allocator.current
	if allocator.store != nil {
		raw, err := allocator.store.GetSetting(allocator.key)
		if err != nil {
			return 0, err
		}
		if persisted, parseErr := strconv.Atoi(strings.TrimSpace(raw)); parseErr == nil && persisted > current {
			current = persisted
		}
	}
	next := current + 1
	if allocator.store != nil {
		if err := allocator.store.SaveSetting(allocator.key, strconv.Itoa(next)); err != nil {
			return 0, err
		}
	}
	allocator.current = next
	return next, nil
}

func (allocator *artifactReferenceSequenceAllocator) observe(sequence int) {
	if allocator == nil || sequence <= 0 {
		return
	}
	allocator.mu.Lock()
	if sequence > allocator.current {
		allocator.current = sequence
	}
	allocator.mu.Unlock()
}

func (allocator *artifactReferenceSequenceAllocator) bindStore(store settingsPersistence) {
	if allocator == nil {
		return
	}
	allocator.mu.Lock()
	allocator.store = store
	allocator.mu.Unlock()
}

// runReferenceSequenceAllocator is shared by the live runner and every
// request-scoped build snapshot. Its mutex is intentionally independent of
// TestRunner.mu: settings-backed allocation may perform I/O, while nil and
// non-atomic stores still need one single-controller monotonic sequence.
type runReferenceSequenceAllocator struct {
	mu      sync.Mutex
	envCode string
	store   settingsPersistence
	byDay   map[string]int
}

func newRunReferenceSequenceAllocator(envCode string, store settingsPersistence) *runReferenceSequenceAllocator {
	return &runReferenceSequenceAllocator{envCode: envCode, store: store, byDay: make(map[string]int)}
}

func (allocator *runReferenceSequenceAllocator) next(now time.Time) (int, error) {
	if allocator == nil {
		return 0, fmt.Errorf("run reference sequence allocator is not initialized")
	}
	day := refRunDay(now)
	key := refRunSeqSettingKey(allocator.envCode, day)
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	if store, ok := allocator.store.(atomicReferenceSequenceStore); ok {
		current := allocator.byDay[day]
		if current > 0 {
			if floorStore, floorOK := allocator.store.(atomicReferenceSequenceFloorStore); floorOK {
				floor, err := floorStore.EnsureSettingSequenceAtLeast(key, current)
				if err != nil {
					return 0, err
				}
				if floor > current {
					current = floor
					allocator.byDay[day] = floor
				}
			}
		}
		next, err := store.NextSettingSequence(key)
		// Compatibility atomic stores may not expose the efficient ensure-floor
		// primitive. Advance until the returned durable value clears every
		// observed local ref instead of lowering the shared setting with Save.
		for err == nil && next <= current {
			next, err = store.NextSettingSequence(key)
		}
		if err == nil {
			allocator.byDay[day] = next
		}
		return next, err
	}
	current := allocator.byDay[day]
	if allocator.store != nil {
		raw, err := allocator.store.GetSetting(key)
		if err != nil {
			return 0, fmt.Errorf("load run reference sequence %q: %w", key, err)
		}
		if persisted, parseErr := strconv.Atoi(strings.TrimSpace(raw)); parseErr == nil && persisted > current {
			current = persisted
		}
	}
	next := current + 1
	if allocator.store != nil {
		if err := allocator.store.SaveSetting(key, strconv.Itoa(next)); err != nil {
			return 0, err
		}
	}
	allocator.byDay[day] = next
	return next, nil
}

func (allocator *runReferenceSequenceAllocator) observe(day string, sequence int) {
	if allocator == nil || sequence <= 0 {
		return
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	if sequence <= allocator.byDay[day] {
		return
	}
	allocator.byDay[day] = sequence
}

// allocateRefSequenceLocked is the compatibility/bootstrap fallback for callers
// without a dedicated artifact allocator. New authoring paths use the allocator
// directly outside tr.mu; this fallback remains counted because it runs locked.
func (tr *TestRunner) allocateRefSequenceLocked(key string, current int) (int, error) {
	tr.beginLockHeldDurableWrite()
	defer tr.endLockHeldDurableWrite()
	if store, ok := tr.settingsStore.(atomicReferenceSequenceStore); ok {
		return store.NextSettingSequence(key)
	}
	next := current + 1
	if tr.settingsStore != nil {
		persisted := tr.loadRefSeqSettingLocked(key)
		if persisted >= next {
			next = persisted + 1
		}
		if err := tr.saveRefSeqSettingLocked(key, next); err != nil {
			return 0, err
		}
	}
	return next, nil
}

func (tr *TestRunner) nextScriptSeqLocked() (int, error) {
	var next int
	var err error
	if tr.scriptRefSeqAllocator != nil {
		tr.scriptRefSeqAllocator.bindStore(tr.settingsStore)
		next, err = tr.scriptRefSeqAllocator.next()
	} else {
		next, err = tr.allocateRefSequenceLocked(refSeqSettingKey(tr.envCode, refkey.TypeScript), tr.refScriptSeq)
	}
	if err == nil {
		tr.refScriptSeq = next
	}
	return next, err
}

func (tr *TestRunner) nextScenarioSeqLocked() (int, error) {
	var next int
	var err error
	if tr.scenarioRefSeqAllocator != nil {
		tr.scenarioRefSeqAllocator.bindStore(tr.settingsStore)
		next, err = tr.scenarioRefSeqAllocator.next()
	} else {
		next, err = tr.allocateRefSequenceLocked(refSeqSettingKey(tr.envCode, refkey.TypeScenario), tr.refScenarioSeq)
	}
	if err == nil {
		tr.refScenarioSeq = next
	}
	return next, err
}

func (tr *TestRunner) nextScriptSeqOutsideRunnerLock() (int, error) {
	tr.scriptRefSeqAllocator.bindStore(tr.settingsStore)
	next, err := tr.scriptRefSeqAllocator.next()
	if err != nil {
		return 0, err
	}
	tr.mu.Lock()
	if next > tr.refScriptSeq {
		tr.refScriptSeq = next
	}
	tr.mu.Unlock()
	return next, nil
}

func (tr *TestRunner) nextScenarioSeqOutsideRunnerLock() (int, error) {
	tr.scenarioRefSeqAllocator.bindStore(tr.settingsStore)
	next, err := tr.scenarioRefSeqAllocator.next()
	if err != nil {
		return 0, err
	}
	tr.mu.Lock()
	if next > tr.refScenarioSeq {
		tr.refScenarioSeq = next
	}
	tr.mu.Unlock()
	return next, nil
}

func (tr *TestRunner) nextRunSeqLocked(now time.Time) (int, error) {
	if tr.runRefSeqAllocator == nil {
		tr.runRefSeqAllocator = newRunReferenceSequenceAllocator(tr.envCode, tr.settingsStore)
	}
	return tr.runRefSeqAllocator.next(now)
}

func refRunDay(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.Format(refRunDateLayout)
}

func refKeyTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}

func normalizeRefKeyCandidate(key string) string {
	return strings.ToUpper(strings.TrimSpace(key))
}

func (tr *TestRunner) observeAndIndexRefKeyLocked(key string, id string) {
	normalized := normalizeRefKeyCandidate(key)
	if normalized == "" || id == "" {
		return
	}
	parts, ok := refkey.Parse(normalized)
	if !ok {
		return
	}
	if tr.refKeyIndex == nil {
		tr.refKeyIndex = make(map[string]string)
	}
	if _, exists := tr.refKeyIndex[normalized]; !exists {
		tr.refKeyIndex[normalized] = id
	}
	if parts.Env != tr.envCode {
		return
	}
	switch parts.Type {
	case refkey.TypeScript:
		if parts.Seq > tr.refScriptSeq {
			tr.refScriptSeq = parts.Seq
			tr.saveRefSeqSettingLocked(refSeqSettingKey(tr.envCode, refkey.TypeScript), tr.refScriptSeq)
		}
	case refkey.TypeScenario:
		if parts.Seq > tr.refScenarioSeq {
			tr.refScenarioSeq = parts.Seq
			tr.saveRefSeqSettingLocked(refSeqSettingKey(tr.envCode, refkey.TypeScenario), tr.refScenarioSeq)
		}
	case refkey.TypeRun:
		day := parts.Date.Format(refRunDateLayout)
		if tr.runRefSeqAllocator == nil {
			tr.runRefSeqAllocator = newRunReferenceSequenceAllocator(tr.envCode, tr.settingsStore)
		}
		tr.runRefSeqAllocator.observe(day, parts.Seq)
	}
}

// scriptRefKeyHeldElsewhereLocked reports whether another object already holds
// key, as a copy of a stored script's definition would bring its original's.
// A script key names its script by environment and sequence (the -vN suffix
// is that script's revision), so a script holding the sequence at any
// revision holds the key.
func (tr *TestRunner) scriptRefKeyHeldElsewhereLocked(key string, id string) bool {
	normalized := normalizeRefKeyCandidate(key)
	if owner, held := tr.refKeyIndex[normalized]; held && owner != id {
		return true
	}
	offered, ok := refkey.Parse(normalized)
	if !ok || offered.Type != refkey.TypeScript {
		return false
	}
	for scriptID, stored := range tr.scripts {
		if scriptID == id || stored == nil {
			continue
		}
		parts, ok := refkey.Parse(normalizeRefKeyCandidate(stored.RefKey))
		if ok && parts.Type == refkey.TypeScript && parts.Env == offered.Env && parts.Seq == offered.Seq {
			return true
		}
	}
	return false
}

// prepareNewScriptRefKeyLocked gives a script being created its reference key:
// one it brings is kept while no other script holds it, otherwise it gets the
// next in sequence. Callers hold tr.mu.
func (tr *TestRunner) prepareNewScriptRefKeyLocked(script *dsl.Script) error {
	if strings.TrimSpace(script.RefKey) != "" && !tr.scriptRefKeyHeldElsewhereLocked(script.RefKey, script.ID) {
		return nil
	}
	return tr.assignNextScriptRefKeyLocked(script)
}

// assignNextScriptRefKeyLocked gives a new script the next reference key.
func (tr *TestRunner) assignNextScriptRefKeyLocked(script *dsl.Script) error {
	sequence, err := tr.nextScriptSeqLocked()
	if err != nil {
		return fmt.Errorf("allocate script reference key: %w", err)
	}
	script.RefKey = refkey.Script(tr.envCode, sequence, 1)
	return nil
}

func (tr *TestRunner) indexRefKeyLocked(key string, id string) {
	normalized := normalizeRefKeyCandidate(key)
	if normalized == "" || id == "" || !refkey.Looks(normalized) {
		return
	}
	if tr.refKeyIndex == nil {
		tr.refKeyIndex = make(map[string]string)
	}
	tr.refKeyIndex[normalized] = id
}

func (tr *TestRunner) removeRefKeyLocked(key string) {
	normalized := normalizeRefKeyCandidate(key)
	if normalized == "" || tr.refKeyIndex == nil {
		return
	}
	delete(tr.refKeyIndex, normalized)
}

func (tr *TestRunner) resolveRefKeyLocked(id string) string {
	normalized := normalizeRefKeyCandidate(id)
	if normalized == "" || !refkey.Looks(normalized) {
		return id
	}
	if tr.refKeyIndex == nil {
		return id
	}
	if uuid, ok := tr.refKeyIndex[normalized]; ok {
		return uuid
	}
	return id
}

func (tr *TestRunner) backfillScriptRefKeysLocked() {
	tr.beginLockHeldDurableWrite()
	defer tr.endLockHeldDurableWrite()
	scripts := make([]*StoredScript, 0, len(tr.scripts))
	for _, script := range tr.scripts {
		if script != nil && strings.TrimSpace(script.RefKey) == "" {
			scripts = append(scripts, script)
		}
	}
	sort.Slice(scripts, func(i, j int) bool {
		return storedScriptBackfillBefore(scripts[i], scripts[j])
	})
	for _, stored := range scripts {
		version := 1
		if stored.Script != nil {
			if strings.TrimSpace(stored.Script.Version) == "" {
				stored.Script.Version = "1"
			}
			version = scriptRefVersion(stored.Script.Version)
		} else if strings.TrimSpace(stored.Version) != "" {
			version = scriptRefVersion(stored.Version)
		}
		sequence, err := tr.nextScriptSeqLocked()
		if err != nil {
			slog.Warn("failed to allocate script reference key", "script_id", stored.ID, "error", err)
			continue
		}
		proposedRefKey := refkey.Script(tr.envCode, sequence, version)
		durableRefKey := proposedRefKey
		if stored.Script != nil {
			if store, ok := tr.scriptStore.(scriptRefKeyPersistence); ok {
				var updatedAt time.Time
				durableRefKey, updatedAt, err = store.EnsureScriptRefKey(stored.ID, proposedRefKey)
				if err == nil {
					stored.Script.UpdatedAt = updatedAt
				}
			} else {
				stored.Script.RefKey = proposedRefKey
				err = tr.saveStoredScriptWithImportTrust(
					stored.Script,
					stored.Script,
					false,
					"",
				)
			}
			if err != nil {
				slog.Warn("failed to persist script reference key", "script_id", stored.ID, "error", err)
				stored.Script.RefKey = ""
				continue
			}
			stored.Script.RefKey = durableRefKey
		}
		stored.RefKey = durableRefKey
		tr.indexRefKeyLocked(stored.RefKey, stored.ID)
	}
}

func storedScriptBackfillBefore(left *StoredScript, right *StoredScript) bool {
	if left == nil || right == nil {
		return right != nil
	}
	leftTime := left.CreatedAt
	if left.Script != nil && !left.Script.CreatedAt.IsZero() {
		leftTime = left.Script.CreatedAt
	}
	rightTime := right.CreatedAt
	if right.Script != nil && !right.Script.CreatedAt.IsZero() {
		rightTime = right.Script.CreatedAt
	}
	if !leftTime.Equal(rightTime) {
		return leftTime.Before(rightTime)
	}
	return left.ID < right.ID
}

func (tr *TestRunner) backfillScenarioRefKeysLocked() {
	tr.beginLockHeldDurableWrite()
	defer tr.endLockHeldDurableWrite()
	scenarios := make([]*StoredScenario, 0, len(tr.scenarios))
	for _, scenario := range tr.scenarios {
		if scenario != nil && strings.TrimSpace(scenario.RefKey) == "" {
			scenarios = append(scenarios, scenario)
		}
	}
	sort.Slice(scenarios, func(i, j int) bool {
		left, right := scenarios[i], scenarios[j]
		if left == nil || right == nil {
			return right != nil
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.ID < right.ID
	})
	for _, stored := range scenarios {
		sequence, err := tr.nextScenarioSeqLocked()
		if err != nil {
			slog.Warn("failed to allocate scenario reference key", "scenario_id", stored.ID, "error", err)
			continue
		}
		proposedRefKey := refkey.Scenario(tr.envCode, sequence)
		durableRefKey := proposedRefKey
		if tr.scenarioStore != nil {
			if store, ok := tr.scenarioStore.(scenarioRefKeyPersistence); ok {
				var updatedAt time.Time
				durableRefKey, updatedAt, err = store.EnsureScenarioRefKey(stored.ID, proposedRefKey)
				if err == nil {
					stored.UpdatedAt = updatedAt
				}
			} else {
				toSave := storedScenarioToStorage(stored)
				toSave.RefKey = proposedRefKey
				err = tr.scenarioStore.SaveScenario(toSave, storedScenarioRefsToStorage(stored))
				if err == nil {
					stored.UpdatedAt = toSave.UpdatedAt
				}
			}
			if err != nil {
				slog.Warn("failed to persist scenario reference key", "scenario_id", stored.ID, "error", err)
				continue
			}
		}
		stored.RefKey = durableRefKey
		tr.indexRefKeyLocked(stored.RefKey, stored.ID)
	}
}

func (tr *TestRunner) backfillTestRefKeysLocked() {
	tests := make([]*ManagedTest, 0, len(tr.tests))
	for _, test := range tr.tests {
		if test != nil && strings.TrimSpace(test.RefKey) == "" && test.Status != "DRAFT" {
			tests = append(tests, test)
		}
	}
	sort.Slice(tests, func(i, j int) bool {
		left, right := tests[i], tests[j]
		if left == nil || right == nil {
			return right != nil
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.ID < right.ID
	})
	for _, test := range tests {
		createdAt := refKeyTime(test.CreatedAt)
		sequence, err := tr.nextRunSeqLocked(createdAt)
		if err != nil {
			slog.Warn("failed to allocate test reference key", "test_id", test.ID, "error", err)
			continue
		}
		proposedRefKey := refkey.Run(tr.envCode, createdAt, sequence)
		durableRefKey := proposedRefKey
		if store, ok := tr.testStore.(testRefKeyPersistence); ok {
			// This backfill runs with tr.mu held, so the store call is published to
			// the panic backstop's in-flight gauge; see trackDurableWrite.
			err = tr.trackDurableWrite(func() error {
				var writeErr error
				durableRefKey, writeErr = store.EnsureTestRefKey(test.ID, proposedRefKey)
				return writeErr
			})
			test.RefKey = durableRefKey
		} else {
			test.RefKey = proposedRefKey
			err = tr.saveManagedTestRecord(test)
		}
		if err != nil {
			slog.Warn("failed to persist test reference key", "test_id", test.ID, "error", err)
			test.RefKey = ""
			continue
		}
		tr.observeAndIndexRefKeyLocked(test.RefKey, test.ID)
	}
}

func (tr *TestRunner) backfillArchiveRefKeysLocked() {
	tr.beginLockHeldDurableWrite()
	defer tr.endLockHeldDurableWrite()
	for testID, archive := range tr.archives {
		if archive == nil || strings.TrimSpace(archive.RefKey) != "" {
			continue
		}
		test, ok := tr.tests[testID]
		if !ok || strings.TrimSpace(test.RefKey) == "" {
			continue
		}
		proposedRefKey := test.RefKey
		durableRefKey := proposedRefKey
		var err error
		if store, ok := tr.archiveStore.(archiveRefKeyPersistence); ok {
			durableRefKey, err = store.EnsureRunArchiveRefKey(testID, proposedRefKey)
		} else {
			archive.RefKey = proposedRefKey
			err = tr.saveRunArchiveRecord(archive)
		}
		if err != nil {
			slog.Warn("failed to persist archive reference key", "test_id", testID, "error", err)
			archive.RefKey = ""
			if test.Archive == archive {
				test.Archive.RefKey = ""
			}
			continue
		}
		archive.RefKey = durableRefKey
		if test.Archive == archive {
			test.Archive.RefKey = durableRefKey
		}
	}
}

func scriptRefVersion(version string) int {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return 1
	}
	for i, r := range trimmed {
		if r < '0' || r > '9' {
			if i == 0 {
				return 1
			}
			n, err := strconv.Atoi(trimmed[:i])
			if err != nil || n < 1 {
				return 1
			}
			return n
		}
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n < 1 {
		return 1
	}
	return n
}
