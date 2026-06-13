package api

// Human-readable reference keys (refkeys): per-environment monotonic sequence
// allocation for scripts/scenarios/runs, an in-memory key→id index, and
// one-time backfill of refkeys onto pre-existing records. Extracted from
// runner.go; all methods hang off *TestRunner and run under its lock (the
// "Locked" suffix denotes a caller-holds-mu contract).

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/refkey"
)

func refSeqSettingKey(envCode string, typ string) string {
	return fmt.Sprintf("refseq:%s:%s", envCode, typ)
}

func refRunSeqSettingKey(envCode string, day string) string {
	return fmt.Sprintf("refseq:%s:%s:%s", envCode, refkey.TypeRun, day)
}

func (tr *TestRunner) initRefKeys() {
	tr.envCode = refkey.EnvCode(buildinfo.Environment())
	tr.refRunSeqByDay = make(map[string]int)
	tr.refKeyIndex = make(map[string]string)

	tr.refScriptSeq = tr.loadRefSeqSettingLocked(refSeqSettingKey(tr.envCode, refkey.TypeScript))
	tr.refScenarioSeq = tr.loadRefSeqSettingLocked(refSeqSettingKey(tr.envCode, refkey.TypeScenario))

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

func (tr *TestRunner) saveRefSeqSettingLocked(key string, value int) {
	if tr.settingsStore == nil {
		return
	}
	if err := tr.settingsStore.SaveSetting(key, strconv.Itoa(value)); err != nil {
		slog.Warn("failed to persist reference-key sequence", "key", key, "value", value, "error", err)
	}
}

func (tr *TestRunner) nextScriptSeqLocked() int {
	tr.refScriptSeq++
	tr.saveRefSeqSettingLocked(refSeqSettingKey(tr.envCode, refkey.TypeScript), tr.refScriptSeq)
	return tr.refScriptSeq
}

func (tr *TestRunner) nextScenarioSeqLocked() int {
	tr.refScenarioSeq++
	tr.saveRefSeqSettingLocked(refSeqSettingKey(tr.envCode, refkey.TypeScenario), tr.refScenarioSeq)
	return tr.refScenarioSeq
}

func (tr *TestRunner) nextRunSeqLocked(now time.Time) int {
	day := refRunDay(now)
	current := tr.refRunSeqByDay[day]
	if current == 0 {
		current = tr.loadRefSeqSettingLocked(refRunSeqSettingKey(tr.envCode, day))
	}
	current++
	tr.refRunSeqByDay[day] = current
	tr.saveRefSeqSettingLocked(refRunSeqSettingKey(tr.envCode, day), current)
	return current
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
		if parts.Seq > tr.refRunSeqByDay[day] {
			tr.refRunSeqByDay[day] = parts.Seq
			tr.saveRefSeqSettingLocked(refRunSeqSettingKey(tr.envCode, day), parts.Seq)
		}
	}
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
		stored.RefKey = refkey.Script(tr.envCode, tr.nextScriptSeqLocked(), version)
		if stored.Script != nil {
			stored.Script.RefKey = stored.RefKey
			tr.saveStoredScript(stored.Script)
		}
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
		stored.RefKey = refkey.Scenario(tr.envCode, tr.nextScenarioSeqLocked())
		if tr.scenarioStore != nil {
			_ = tr.scenarioStore.SaveScenario(storedScenarioToStorage(stored), storedScenarioRefsToStorage(stored))
		}
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
		test.RefKey = refkey.Run(tr.envCode, createdAt, tr.nextRunSeqLocked(createdAt))
		tr.saveManagedTestRecord(test)
		tr.indexRefKeyLocked(test.RefKey, test.ID)
	}
}

func (tr *TestRunner) backfillArchiveRefKeysLocked() {
	for testID, archive := range tr.archives {
		if archive == nil || strings.TrimSpace(archive.RefKey) != "" {
			continue
		}
		test, ok := tr.tests[testID]
		if !ok || strings.TrimSpace(test.RefKey) == "" {
			continue
		}
		archive.RefKey = test.RefKey
		if test.Archive == archive {
			test.Archive.RefKey = archive.RefKey
		}
		_ = tr.saveRunArchiveRecord(archive)
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
