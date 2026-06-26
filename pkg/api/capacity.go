package api

// Capacity, quota, and fair-share scheduling: active test/VUser accounting,
// concurrency + reserved-capacity gating, per-project limits, pending-queue
// fair-share ranking, and capacity-advisory refresh. Extracted from runner.go;
// methods hang off *TestRunner and the "Locked" suffix denotes a caller-holds-mu
// contract.

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) countActiveTestsLocked() int {
	active := 0
	for _, test := range tr.tests {
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			active++
		}
	}
	return active
}

func (tr *TestRunner) countActiveTestsForProjectLocked(projectID string) int {
	projectID = normalizeProjectID(projectID)
	active := 0
	for _, test := range tr.tests {
		if normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			active++
		}
	}
	return active
}

func (tr *TestRunner) countActiveVUsersLocked() int {
	total := 0
	for _, test := range tr.tests {
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			total += test.TotalVUsers
		}
	}
	return total
}

func (tr *TestRunner) countActiveVUsersForProjectLocked(projectID string) int {
	projectID = normalizeProjectID(projectID)
	total := 0
	for _, test := range tr.tests {
		if normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			total += test.TotalVUsers
		}
	}
	return total
}

// testAssignedPoolID returns the fleet pool a test is pinned to (via its
// resolved assignment), or "" when the run is not pool-scoped.
func testAssignedPoolID(test *ManagedTest) string {
	if test == nil || test.Assignment == nil {
		return ""
	}
	return strings.TrimSpace(test.Assignment.PoolID)
}

// countActiveTestsOnPoolForProjectLocked / ...VUsers... count a project's live
// footprint on one fleet pool (matching the project-wide quota counters'
// RUNNING/INITIALIZING predicate, scoped by the test's assigned pool).
func (tr *TestRunner) countActiveTestsOnPoolForProjectLocked(poolID, projectID string) int {
	projectID = normalizeProjectID(projectID)
	count := 0
	for _, test := range tr.tests {
		if test == nil || testAssignedPoolID(test) != poolID || normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			count++
		}
	}
	return count
}

func (tr *TestRunner) countActiveVUsersOnPoolForProjectLocked(poolID, projectID string) int {
	projectID = normalizeProjectID(projectID)
	total := 0
	for _, test := range tr.tests {
		if test == nil || testAssignedPoolID(test) != poolID || normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		if test.Status == "RUNNING" || test.Status == "INITIALIZING" {
			total += test.TotalVUsers
		}
	}
	return total
}

// poolProjectQuota returns the project's quota for a specific fleet pool, if one
// is configured.
func (tr *TestRunner) poolProjectQuota(projectID, poolID string) (storage.PoolProjectQuota, bool) {
	if tr.projectStore == nil || poolID == "" {
		return storage.PoolProjectQuota{}, false
	}
	project, err := tr.projectStore.GetProject(normalizeProjectID(projectID))
	if err != nil || project == nil {
		return storage.PoolProjectQuota{}, false
	}
	quota, ok := project.Settings.PoolQuotas[poolID]
	return quota, ok
}

// evaluatePoolQuotaBlockLocked enforces a project's per-pool quota when a run is
// pinned to a fleet pool. Additive to the project-wide checks: a pool-assigned
// run must pass both. Excludes excludeTestID so re-evaluating a running test
// doesn't count it against itself.
func (tr *TestRunner) evaluatePoolQuotaBlockLocked(test *ManagedTest, excludeTestID string) (bool, string) {
	poolID := testAssignedPoolID(test)
	if poolID == "" {
		return false, ""
	}
	quota, ok := tr.poolProjectQuota(test.ProjectID, poolID)
	if !ok || (quota.MaxConcurrentTests <= 0 && quota.MaxVUsers <= 0) {
		return false, ""
	}
	activeTests := tr.countActiveTestsOnPoolForProjectLocked(poolID, test.ProjectID)
	activeVUsers := tr.countActiveVUsersOnPoolForProjectLocked(poolID, test.ProjectID)
	if excludeTestID != "" {
		if existing, ok := tr.tests[excludeTestID]; ok && (existing.Status == "RUNNING" || existing.Status == "INITIALIZING") &&
			testAssignedPoolID(existing) == poolID && normalizeProjectID(existing.ProjectID) == normalizeProjectID(test.ProjectID) {
			activeTests--
			activeVUsers -= existing.TotalVUsers
		}
	}
	if quota.MaxConcurrentTests > 0 && activeTests >= quota.MaxConcurrentTests {
		return true, fmt.Sprintf("waiting for a pool test slot on %q (%d max concurrent tests)", poolID, quota.MaxConcurrentTests)
	}
	if quota.MaxVUsers > 0 && activeVUsers+test.TotalVUsers > quota.MaxVUsers {
		return true, fmt.Sprintf("waiting for pool VUser capacity on %q (%d max vusers)", poolID, quota.MaxVUsers)
	}
	return false, ""
}

func (tr *TestRunner) validateConcurrencyLimitsLocked(projectID string) error {
	maxConcurrent := tr.settings.General.MaxConcurrentTests
	if tr.countActiveTestsLocked() >= maxConcurrent {
		return fmt.Errorf("max concurrent tests (%d) reached", maxConcurrent)
	}
	projectMaxConcurrent := tr.projectMaxConcurrentTests(projectID)
	if projectMaxConcurrent > 0 && tr.countActiveTestsForProjectLocked(projectID) >= projectMaxConcurrent {
		return fmt.Errorf("project max concurrent tests (%d) reached", projectMaxConcurrent)
	}
	return nil
}

func (tr *TestRunner) evaluateQuotaBlockLocked(test *ManagedTest, excludeTestID string) (bool, string) {
	if test == nil {
		return false, ""
	}
	globalActiveTests := tr.countActiveTestsLocked()
	projectActiveTests := tr.countActiveTestsForProjectLocked(test.ProjectID)
	globalActiveVUsers := tr.countActiveVUsersLocked()
	projectActiveVUsers := tr.countActiveVUsersForProjectLocked(test.ProjectID)
	if excludeTestID != "" {
		if existing, ok := tr.tests[excludeTestID]; ok && (existing.Status == "RUNNING" || existing.Status == "INITIALIZING") {
			globalActiveTests--
			projectActiveTests--
			globalActiveVUsers -= existing.TotalVUsers
			projectActiveVUsers -= existing.TotalVUsers
		}
	}
	maxConcurrent := tr.settings.General.MaxConcurrentTests
	if globalActiveTests >= maxConcurrent {
		return true, fmt.Sprintf("waiting for a global test slot (%d max concurrent tests)", maxConcurrent)
	}
	projectMaxConcurrent := tr.projectMaxConcurrentTests(test.ProjectID)
	if projectMaxConcurrent > 0 && projectActiveTests >= projectMaxConcurrent {
		return true, fmt.Sprintf("waiting for a project test slot (%d max concurrent tests)", projectMaxConcurrent)
	}
	projectMaxVUsers := tr.projectMaxVUsers(test.ProjectID)
	if projectMaxVUsers > 0 && projectActiveVUsers+test.TotalVUsers > projectMaxVUsers {
		return true, fmt.Sprintf("waiting for project VUser capacity (%d max vusers)", projectMaxVUsers)
	}
	if blocked, reason := tr.evaluatePoolQuotaBlockLocked(test, excludeTestID); blocked {
		return true, reason
	}
	if blocked, reason := tr.evaluateReservedCapacityBlockLocked(test, excludeTestID); blocked {
		return true, reason
	}
	if blocked, reason := tr.evaluatePoolReservedCapacityBlockLocked(test, excludeTestID); blocked {
		return true, reason
	}
	return false, ""
}

// evaluateReservedCapacityBlockLocked enforces reserved capacity pools at
// admission: generators reserved for a project are protected from other
// tenants. Each project may always consume up to its own reservation; beyond
// that, all projects draw from a bounded shared pool. A test is held when
// admitting it would push shared-pool usage past the shared capacity, so a
// non-reserving (or over-reserving) project cannot starve the teams that
// reserved capacity. Reservations also still drive fair-share queue priority.
func (tr *TestRunner) evaluateReservedCapacityBlockLocked(test *ManagedTest, excludeTestID string) (bool, string) {
	if test == nil {
		return false, ""
	}
	now := time.Now()
	activeSlots, err := tr.boundTimeslotsActiveAtLocked(now)
	if err != nil {
		// Fail closed: hold the test rather than admit it into capacity that a
		// bound reservation may be protecting but we could not read.
		return true, "waiting for reservation state to become available"
	}
	totalReserved, projectReserved := reservationTotalsWithTimeslots(tr.settings, normalizeProjectID(test.ProjectID), now, activeSlots)
	if totalReserved <= 0 {
		return false, "" // no reservations configured -> nothing to protect
	}
	effective := resolveCapacityAvailability(tr.settings).EffectiveAvailableGenerators
	if effective <= 0 {
		return false, "" // no capacity model -> don't gate on reservations
	}
	if totalReserved > effective {
		totalReserved = effective
	}
	shared := maxInt(0, effective-totalReserved)

	// This project's shared-pool contribution before and after admitting the test.
	projectActive := tr.countActiveRecommendedGeneratorsForProjectLocked(normalizeProjectID(test.ProjectID))
	if excludeTestID != "" {
		if existing, ok := tr.tests[excludeTestID]; ok && reservationActiveStatus(existing.Status) && normalizeProjectID(existing.ProjectID) == normalizeProjectID(test.ProjectID) {
			projectActive -= recommendedGeneratorCountForTest(existing)
		}
	}
	g := recommendedGeneratorCountForTest(test)
	sharedDelta := maxInt(0, projectActive+g-projectReserved) - maxInt(0, projectActive-projectReserved)

	sharedUsedTotal := tr.sharedGeneratorUsageLocked(excludeTestID, now, activeSlots)
	if sharedUsedTotal+sharedDelta > shared {
		return true, fmt.Sprintf("waiting for shared generator capacity (%d of %d generators reserved for other projects)", totalReserved, effective)
	}
	return false, ""
}

// sharedGeneratorUsageLocked sums each project's draw on the shared generator
// pool: a project's active generators above its own reservation.
func (tr *TestRunner) sharedGeneratorUsageLocked(excludeTestID string, at time.Time, activeSlots []*storage.CapacityReservation) int {
	byProject := make(map[string]int)
	for id, test := range tr.tests {
		if test == nil || id == excludeTestID || !reservationActiveStatus(test.Status) {
			continue
		}
		byProject[normalizeProjectID(test.ProjectID)] += recommendedGeneratorCountForTest(test)
	}
	total := 0
	for projectID, active := range byProject {
		_, reserved := reservationTotalsWithTimeslots(tr.settings, projectID, at, activeSlots)
		total += maxInt(0, active-reserved)
	}
	return total
}

func reservationActiveStatus(status string) bool {
	return status == "RUNNING" || status == "INITIALIZING" || status == "PAUSED"
}

func (tr *TestRunner) projectMaxConcurrentTests(projectID string) int {
	if tr.projectStore == nil {
		return 0
	}
	projectID = normalizeProjectID(projectID)
	project, err := tr.projectStore.GetProject(projectID)
	if err != nil || project == nil {
		return 0
	}
	if project.Settings.MaxConcurrentTests < 1 {
		return 0
	}
	return project.Settings.MaxConcurrentTests
}

func (tr *TestRunner) projectMaxVUsers(projectID string) int {
	if tr.projectStore == nil {
		return 0
	}
	projectID = normalizeProjectID(projectID)
	project, err := tr.projectStore.GetProject(projectID)
	if err != nil || project == nil {
		return 0
	}
	if project.Settings.MaxVUsers < 1 {
		return 0
	}
	return project.Settings.MaxVUsers
}

// projectMaxVUsersStrict is the fail-closed variant for governance gates: a real
// store error is surfaced (so the caller can block) instead of silently
// degrading to "no quota". A missing project or unset limit is a legitimate 0.
func (tr *TestRunner) projectMaxVUsersStrict(projectID string) (int, error) {
	if tr.projectStore == nil {
		return 0, nil
	}
	project, err := tr.projectStore.GetProject(normalizeProjectID(projectID))
	if err != nil {
		// A missing project is a legitimate "no quota"; only a real store failure
		// fails closed.
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	if project == nil || project.Settings.MaxVUsers < 1 {
		return 0, nil
	}
	return project.Settings.MaxVUsers, nil
}

func (tr *TestRunner) projectSettings(projectID string) (storage.ProjectSettings, bool) {
	if tr.projectStore == nil {
		return storage.ProjectSettings{}, false
	}
	project, err := tr.projectStore.GetProject(normalizeProjectID(projectID))
	if err != nil || project == nil {
		return storage.ProjectSettings{}, false
	}
	return project.Settings, true
}

// enforceTestQuotaLocked applies per-test hard caps (VUser ceiling, max
// duration) configured on the owning project. Unlike the aggregate concurrency
// quotas (which queue), these reject outright at creation so a single oversized
// test cannot be admitted. DRAFT tests are exempt — they are never executed.
func (tr *TestRunner) enforceTestQuotaLocked(projectID string, totalVUsers int, dur time.Duration, status string) error {
	if status == "DRAFT" {
		return nil
	}
	settings, ok := tr.projectSettings(projectID)
	if !ok {
		return nil
	}
	if settings.MaxVUsersPerTest > 0 && totalVUsers > settings.MaxVUsersPerTest {
		return fmt.Errorf("test exceeds project per-test VUser cap (%d requested, %d max)", totalVUsers, settings.MaxVUsersPerTest)
	}
	if settings.MaxTestDurationMinutes > 0 {
		maxDur := time.Duration(settings.MaxTestDurationMinutes) * time.Minute
		if dur > maxDur {
			return fmt.Errorf("test exceeds project max duration (%s requested, %s max)", dur, maxDur)
		}
	}
	return nil
}

func pendingAnchor(test *ManagedTest) time.Time {
	if test != nil && test.PendingSince != nil && !test.PendingSince.IsZero() {
		return *test.PendingSince
	}
	if test != nil {
		return test.CreatedAt
	}
	return time.Time{}
}

type pendingFairShareRank struct {
	reservationTier int
	activeTests     int
	activeVUsers    int
	anchor          time.Time
	createdAt       time.Time
	id              string
}

func (tr *TestRunner) sortPendingTestsForFairShareLocked(pending []*ManagedTest) {
	sort.Slice(pending, func(i, j int) bool {
		left := tr.pendingFairShareRankLocked(pending[i])
		right := tr.pendingFairShareRankLocked(pending[j])
		if left.reservationTier != right.reservationTier {
			return left.reservationTier < right.reservationTier
		}
		if left.activeTests != right.activeTests {
			return left.activeTests < right.activeTests
		}
		if left.activeVUsers != right.activeVUsers {
			return left.activeVUsers < right.activeVUsers
		}
		if !left.anchor.Equal(right.anchor) {
			return left.anchor.Before(right.anchor)
		}
		if !left.createdAt.Equal(right.createdAt) {
			return left.createdAt.Before(right.createdAt)
		}
		return left.id < right.id
	})
}

func (tr *TestRunner) pendingFairShareRankLocked(test *ManagedTest) pendingFairShareRank {
	if test == nil {
		return pendingFairShareRank{reservationTier: 1, id: ""}
	}
	projectID := normalizeProjectID(test.ProjectID)
	reservationTier := 1
	now := time.Now()
	// Fair-share ranking is queue priority, not an admission grant; on a lookup
	// error (already logged at the source) degrade to settings-only reservations
	// rather than block ordering.
	activeSlots, _ := tr.boundTimeslotsActiveAtLocked(now)
	_, projectReserved := reservationTotalsWithTimeslots(tr.settings, projectID, now, activeSlots)
	if projectReserved > 0 && tr.countActiveRecommendedGeneratorsForProjectLocked(projectID) < projectReserved {
		reservationTier = 0
	}
	return pendingFairShareRank{
		reservationTier: reservationTier,
		activeTests:     tr.countActiveTestsForProjectLocked(projectID),
		activeVUsers:    tr.countActiveVUsersForProjectLocked(projectID),
		anchor:          pendingAnchor(test),
		createdAt:       test.CreatedAt,
		id:              test.ID,
	}
}

func (tr *TestRunner) countActiveRecommendedGeneratorsForProjectLocked(projectID string) int {
	projectID = normalizeProjectID(projectID)
	total := 0
	for _, test := range tr.tests {
		if test == nil || normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		if test.Status != "RUNNING" && test.Status != "INITIALIZING" && test.Status != "PAUSED" {
			continue
		}
		total += recommendedGeneratorCountForTest(test)
	}
	return total
}

func recommendedGeneratorCountForTest(test *ManagedTest) int {
	if test == nil {
		return 0
	}
	if test.CapacityAdvisory != nil && test.CapacityAdvisory.RecommendedGenerators > 0 {
		return test.CapacityAdvisory.RecommendedGenerators
	}
	if test.TotalVUsers > 0 {
		return 1
	}
	return 0
}

func runLogicGoalSnapshot(script *dsl.Script) *RunLogicGoalSnapshot {
	if script == nil {
		return nil
	}
	goals := RunLogicGoalSnapshot{
		GlobalIterations: script.Config.RunLogic.GlobalIterations,
		MaxTransactions:  script.Config.RunLogic.MaxTransactions,
	}
	if goals.GlobalIterations <= 0 && goals.MaxTransactions <= 0 {
		return nil
	}
	return &goals
}

func (tr *TestRunner) refreshPendingQueueMetadataLocked() {
	pending := make([]*ManagedTest, 0)
	for _, test := range tr.tests {
		if test.Status != "PENDING" {
			test.PendingReason = ""
			test.QueuePosition = 0
			continue
		}
		if test.PendingSince == nil || test.PendingSince.IsZero() {
			pendingSince := test.CreatedAt
			test.PendingSince = &pendingSince
		}
		pending = append(pending, test)
	}
	tr.sortPendingTestsForFairShareLocked(pending)
	for index, test := range pending {
		test.QueuePosition = index + 1
		blocked, reason := tr.evaluateQuotaBlockLocked(test, "")
		if blocked {
			test.PendingReason = reason
		} else {
			test.PendingReason = "ready to start when the queue advances"
		}
	}
}

func buildCapacityShortfallStartError(id string, advisory *CapacityAdvisory, opts StartTestOptions) error {
	if advisory == nil || !advisory.RequiresOverride || opts.OverrideCapacityShortfall {
		return nil
	}
	cloned := cloneCapacityAdvisory(advisory)
	return &CapacityShortfallError{
		TestID:   id,
		Advisory: cloned,
		Message:  fmt.Sprintf("capacity advisory recommends %d load generators but only %d are currently available", cloned.RecommendedGenerators, cloned.AvailableGenerators),
	}
}

// buildStartGovernanceError enforces, at the start chokepoint, every advisory
// gate that the launch-preflight endpoint enforces. It is called wherever a
// test transitions toward RUNNING so that POST /tests/{id}/start, the scheduler,
// and the pending-promotion loop all apply the same governance the preflight
// path reports. The browser-density gate is intentionally not bypassable by
// OverrideCapacityShortfall: the only way to clear it is the env-driven
// benchmark-evidence + override path baked into the advisory.
func buildStartGovernanceError(id string, advisory *CapacityAdvisory, opts StartTestOptions) error {
	if err := buildCapacityShortfallStartError(id, advisory, opts); err != nil {
		return err
	}
	return buildBrowserDensityGateStartError(id, advisory)
}

func buildBrowserDensityGateStartError(id string, advisory *CapacityAdvisory) error {
	if !browserDensityReleaseGateBlocks(advisory) {
		return nil
	}
	return &BrowserDensityGateError{
		TestID:   id,
		Advisory: cloneCapacityAdvisory(advisory),
		Message:  browserDensityReleaseGateMessage(advisory),
	}
}

func (tr *TestRunner) refreshCapacityAdvisoryLocked(test *ManagedTest) {
	if test == nil {
		return
	}
	groups := tr.hydrateCapacityGroupsLocked(test.Groups)
	test.CapacityAdvisory = tr.estimateCapacityAdvisoryForAssignment(test.ProjectID, groups, test.TotalVUsers, test.Duration, test.ResourceBaseline, test.RuntimeSettings, test.Assignment)
}
