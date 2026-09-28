package api

// Capacity, quota, and fair-share scheduling: active test/VUser accounting,
// concurrency + reserved-capacity gating, per-project limits, pending-queue
// fair-share ranking, and capacity-advisory refresh. Extracted from runner.go;
// methods hang off *TestRunner and the "Locked" suffix denotes a caller-holds-mu
// contract.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) countActiveTestsLocked() int {
	active := 0
	for _, test := range tr.tests {
		if testReservesRuntimeCapacity(test) {
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
		if testReservesRuntimeCapacity(test) {
			active++
		}
	}
	return active
}

func (tr *TestRunner) countActiveVUsersLocked() int {
	total := 0
	for _, test := range tr.tests {
		if testReservesRuntimeCapacity(test) {
			total = saturatingAddNonNegative(total, managedTestCapacityVUsers(test))
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
		if testReservesRuntimeCapacity(test) {
			total = saturatingAddNonNegative(total, managedTestCapacityVUsers(test))
		}
	}
	return total
}

func testReservesRuntimeCapacity(test *ManagedTest) bool {
	if test == nil {
		return false
	}
	switch test.Status {
	case "SHAKEOUT", "STARTING", "INITIALIZING", "RUNNING", "PAUSED", "STOPPING":
		return true
	default:
		return false
	}
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
		if testReservesRuntimeCapacity(test) {
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
		if testReservesRuntimeCapacity(test) {
			total = saturatingAddNonNegative(total, managedTestCapacityVUsers(test))
		}
	}
	return total
}

func (tr *TestRunner) evaluatePoolQuotaBlockWithQuotaLocked(test *ManagedTest, excludeTestID string, quota storage.PoolProjectQuota, ok bool) (bool, string) {
	poolID := testAssignedPoolID(test)
	if poolID == "" {
		return false, ""
	}
	if !ok || (quota.MaxConcurrentTests <= 0 && quota.MaxVUsers <= 0) {
		return false, ""
	}
	activeTests := tr.countActiveTestsOnPoolForProjectLocked(poolID, test.ProjectID)
	activeVUsers := tr.countActiveVUsersOnPoolForProjectLocked(poolID, test.ProjectID)
	if excludeTestID != "" {
		if existing, ok := tr.tests[excludeTestID]; ok && testReservesRuntimeCapacity(existing) &&
			testAssignedPoolID(existing) == poolID && normalizeProjectID(existing.ProjectID) == normalizeProjectID(test.ProjectID) {
			activeTests--
			activeVUsers -= managedTestCapacityVUsers(existing)
		}
	}
	if quota.MaxConcurrentTests > 0 && activeTests >= quota.MaxConcurrentTests {
		return true, fmt.Sprintf("waiting for a pool test slot on %q (%d max concurrent tests)", poolID, quota.MaxConcurrentTests)
	}
	if quota.MaxVUsers > 0 && nonNegativeSumExceedsLimit(activeVUsers, managedTestCapacityVUsers(test), quota.MaxVUsers) {
		return true, fmt.Sprintf("waiting for pool VUser capacity on %q (%d max vusers)", poolID, quota.MaxVUsers)
	}
	return false, ""
}

// projectQuotaSnapshot is the immutable subset of project settings consumed by
// admission. Keeping the per-pool value (rather than the settings map) makes a
// snapshot safe to carry across the unlocked storage-read / locked-install
// boundary without retaining mutable store-owned maps.
type projectQuotaSnapshot struct {
	maxConcurrentTests int
	maxVUsers          int
	maxVUsersPerTest   int
	maxTestDuration    int
	redactionRules     []engine.RedactionRule
	poolQuota          storage.PoolProjectQuota
	hasPoolQuota       bool
	features           storage.ProjectFeatureFlags
}

func projectQuotaSnapshotFromProject(project *storage.Project, poolID string) projectQuotaSnapshot {
	if project == nil {
		return projectQuotaSnapshot{}
	}
	snapshot := projectQuotaSnapshot{
		maxConcurrentTests: project.Settings.MaxConcurrentTests,
		maxVUsers:          project.Settings.MaxVUsers,
		maxVUsersPerTest:   project.Settings.MaxVUsersPerTest,
		maxTestDuration:    project.Settings.MaxTestDurationMinutes,
		redactionRules:     cloneRedactionRules(project.Settings.RedactionRules),
		features:           project.Settings.Features,
	}
	if poolID != "" {
		snapshot.poolQuota, snapshot.hasPoolQuota = project.Settings.PoolQuotas[poolID]
	}
	return snapshot
}

type projectQuotaEvidence struct {
	projectID       string
	poolID          string
	quota           projectQuotaSnapshot
	updatedAt       time.Time
	storeConfigured bool
	projectResolved bool
	loaded          bool
	err             error
}

func loadProjectQuotaEvidence(store projectPersistence, projectID, poolID string) projectQuotaEvidence {
	projectID = normalizeProjectID(projectID)
	poolID = strings.TrimSpace(poolID)
	evidence := projectQuotaEvidence{
		projectID: projectID, poolID: poolID, storeConfigured: store != nil,
		projectResolved: store == nil, loaded: true,
	}
	if store == nil {
		return evidence
	}
	project, err := store.GetProject(projectID)
	if err != nil {
		evidence.err = err
		return evidence
	}
	// A few lightweight compatibility stores historically represent an
	// unconfigured project catalog as (nil, nil). Treat that as an available
	// zero-quota projection; production SQL reports sql.ErrNoRows and therefore
	// remains fail-closed for a genuinely deleted/missing durable project.
	evidence.projectResolved = true
	evidence.quota = projectQuotaSnapshotFromProject(project, poolID)
	if project != nil {
		evidence.updatedAt = project.UpdatedAt
	}
	return evidence
}

func (e projectQuotaEvidence) quotaFor(test *ManagedTest) (projectQuotaSnapshot, bool) {
	if test == nil {
		return projectQuotaSnapshot{}, true
	}
	return e.quota, e.loaded && e.err == nil && (!e.storeConfigured || e.projectResolved) && e.projectID == normalizeProjectID(test.ProjectID) && e.poolID == testAssignedPoolID(test)
}

func (e projectQuotaEvidence) quotaForProject(projectID string) (projectQuotaSnapshot, bool) {
	return e.quota, e.loaded && e.err == nil && (!e.storeConfigured || e.projectResolved) && e.projectID == normalizeProjectID(projectID)
}

func (tr *TestRunner) evaluateQuotaBlockWithTimeslotsAndProjectQuotaLocked(test *ManagedTest, excludeTestID string, now time.Time, activeSlots []*storage.CapacityReservation, reservationErr error, quota projectQuotaSnapshot) (bool, string) {
	if test == nil {
		return false, ""
	}
	if recoverySpareVUsers(test) > 0 || tr.settings.CapacityPlanning.PodReservationsEnabled || (test.Assignment != nil && test.Assignment.SharingMode == "dedicated") {
		if _, warnings := tr.previewPodPlacementLocked(test); len(warnings) > 0 {
			return true, "waiting for peak pod capacity: " + warnings[0]
		}
	}
	globalActiveTests := tr.countActiveTestsLocked()
	projectActiveTests := tr.countActiveTestsForProjectLocked(test.ProjectID)
	globalActiveVUsers := tr.countActiveVUsersLocked()
	projectActiveVUsers := tr.countActiveVUsersForProjectLocked(test.ProjectID)
	if excludeTestID != "" {
		if existing, ok := tr.tests[excludeTestID]; ok && testReservesRuntimeCapacity(existing) {
			globalActiveTests--
			projectActiveTests--
			globalActiveVUsers -= managedTestCapacityVUsers(existing)
			projectActiveVUsers -= managedTestCapacityVUsers(existing)
		}
	}
	maxConcurrent := tr.settings.General.MaxConcurrentTests
	if maxConcurrent > 0 && globalActiveTests >= maxConcurrent {
		return true, fmt.Sprintf("waiting for a global test slot (%d max concurrent tests)", maxConcurrent)
	}
	projectMaxConcurrent := quota.maxConcurrentTests
	if projectMaxConcurrent > 0 && projectActiveTests >= projectMaxConcurrent {
		return true, fmt.Sprintf("waiting for a project test slot (%d max concurrent tests)", projectMaxConcurrent)
	}
	projectMaxVUsers := quota.maxVUsers
	if projectMaxVUsers > 0 && nonNegativeSumExceedsLimit(projectActiveVUsers, managedTestCapacityVUsers(test), projectMaxVUsers) && !hasApprovedExecutionPlanQuotaBurst(test) {
		return true, fmt.Sprintf("waiting for project VUser capacity (%d max vusers)", projectMaxVUsers)
	}
	if blocked, reason := tr.evaluatePoolQuotaBlockWithQuotaLocked(test, excludeTestID, quota.poolQuota, quota.hasPoolQuota); blocked {
		return true, reason
	}
	if blocked, reason := tr.evaluateReservedCapacityBlockWithTimeslotsLocked(test, excludeTestID, now, activeSlots, reservationErr); blocked {
		return true, reason
	}
	if blocked, reason := tr.evaluatePoolReservedCapacityBlockWithTimeslotsLocked(test, excludeTestID, now, activeSlots, reservationErr); blocked {
		return true, reason
	}
	return false, ""
}

func (tr *TestRunner) evaluateQuotaBlockWithLaunchEvidenceLocked(test *ManagedTest, excludeTestID string, now time.Time, evidence launchWindowEvidence) (bool, string) {
	quota, ok := evidence.projectQuota.quotaFor(test)
	if !ok {
		return true, "waiting for project quota evidence to be refreshed"
	}
	activeSlots, reservationErr := boundTimeslotsActiveAtFromEvidence(evidence, now)
	return tr.evaluateQuotaBlockWithTimeslotsAndProjectQuotaLocked(test, excludeTestID, now, activeSlots, reservationErr, quota)
}

func hasApprovedExecutionPlanQuotaBurst(test *ManagedTest) bool {
	if test == nil || test.ExecutionPlanApproval == nil {
		return false
	}
	approval := test.ExecutionPlanApproval
	return approval.ID != "" &&
		approval.PlanID != "" &&
		approval.PlanContentHash != "" &&
		approval.Policy == "quota_burst" &&
		approval.Status == "approved" &&
		approval.ApprovedPeakVUsers > 0 &&
		managedTestCapacityVUsers(test) <= approval.ApprovedPeakVUsers
}

func approvedExecutionPlanPeakForTest(test *ManagedTest) int {
	if test == nil {
		return 0
	}
	peak := trafficProfileCapacityVUsers(test.TrafficProfile, test.Groups, test.Duration, test.GoalConfig)
	if peak <= 0 {
		peak = test.TotalVUsers
	}
	if test.GoalConfig != nil && test.GoalConfig.MaxVUsers != nil && *test.GoalConfig.MaxVUsers > peak {
		peak = *test.GoalConfig.MaxVUsers
	}
	return peak
}

// evaluateReservedCapacityBlockLocked enforces reserved capacity pools at
// admission: generators reserved for a project are protected from other
// tenants. Each project may always consume up to its own reservation; beyond
// that, all projects draw from a bounded shared pool. A test is held when
// admitting it would push shared-pool usage past the shared capacity, so a
// non-reserving (or over-reserving) project cannot starve the teams that
// reserved capacity. Reservations also still drive fair-share queue priority.
func (tr *TestRunner) evaluateReservedCapacityBlockWithTimeslotsLocked(test *ManagedTest, excludeTestID string, now time.Time, activeSlots []*storage.CapacityReservation, reservationErr error) (bool, string) {
	if test == nil {
		return false, ""
	}
	// The shared (non-pool) reservation gate governs only non-pool runs. A pool-
	// pinned run draws from its pool's member generators and is governed by the
	// pool reservation gate (evaluatePoolReservedCapacityBlockLocked); charging it
	// against the shared envelope here would double-count its draw.
	if testAssignedPoolID(test) != "" {
		return false, ""
	}
	if reservationErr != nil {
		// Fail closed: hold the test rather than admit it into capacity that a
		// bound reservation may be protecting but we could not read.
		return true, "waiting for reservation state to become available"
	}
	totalReserved, _ := reservationTotalsWithTimeslots(tr.settings, normalizeProjectID(test.ProjectID), now, activeSlots)
	if totalReserved <= 0 {
		return false, "" // no global reservations configured -> nothing to protect
	}
	effective := tr.resolveCapacityAvailabilityAt(now).EffectiveAvailableGenerators
	if effective <= 0 {
		return false, "" // no capacity model -> don't gate on reservations
	}
	if totalReserved > effective {
		totalReserved = effective
	}
	shared := maxInt(0, effective-totalReserved)

	// A non-pool run may offset only its project's GLOBAL (non-pool) reservations
	// against the shared envelope. It must NOT be credited by a pool-scoped
	// reservation — that was the double-spend (RN-1): an idle pool reservation
	// shrank a non-pool run's shared draw, admitting it against capacity the pool
	// gate was separately protecting.
	projectReserved := projectNonPoolReservedAt(tr.settings, normalizeProjectID(test.ProjectID), now, activeSlots)

	// This project's shared-pool contribution before and after admitting the test.
	projectActive := tr.countActiveRecommendedGeneratorsForProjectLocked(normalizeProjectID(test.ProjectID))
	if excludeTestID != "" {
		if existing, ok := tr.tests[excludeTestID]; ok && reservationActiveStatus(existing.Status) && normalizeProjectID(existing.ProjectID) == normalizeProjectID(test.ProjectID) {
			projectActive -= recommendedGeneratorCountForTest(existing)
		}
	}
	g := recommendedGeneratorCountForTest(test)
	projectAfter := saturatingAddNonNegative(projectActive, g)
	sharedDelta := maxInt(0, projectAfter-projectReserved) - maxInt(0, projectActive-projectReserved)

	sharedUsedTotal := tr.sharedGeneratorUsageLocked(excludeTestID, now, activeSlots)
	if sharedDelta > 0 && nonNegativeSumExceedsLimit(sharedUsedTotal, sharedDelta, shared) {
		return true, fmt.Sprintf("waiting for shared generator capacity (%d of %d generators reserved for other projects)", totalReserved, effective)
	}
	return false, ""
}

// projectNonPoolReservedAt sums a project's active global (non-pool) reserved
// generators: settings reservations and bound timeslots that carry no PoolID.
func projectNonPoolReservedAt(settings SystemSettings, projectID string, now time.Time, activeSlots []*storage.CapacityReservation) int {
	projectID = normalizeProjectID(projectID)
	if projectID == "" {
		return 0
	}
	total := 0
	for _, reservation := range normalizeCapacityReservations(settings.CapacityPlanning.Reservations) {
		if strings.TrimSpace(reservation.PoolID) != "" || reservation.ReservedGenerators <= 0 {
			continue
		}
		if reservation.ProjectID == projectID && reservationActiveAt(reservation, now) {
			total = saturatingAddNonNegative(total, reservation.ReservedGenerators)
		}
	}
	for _, ts := range activeSlots {
		if timeslotPoolID(ts) != "" || normalizeProjectID(ts.ProjectID) != projectID {
			continue
		}
		if g := timeslotReservedGenerators(settings, ts); g > 0 {
			total = saturatingAddNonNegative(total, g)
		}
	}
	return total
}

func (tr *TestRunner) resolveCapacityAvailabilityAt(now time.Time) CapacityAvailabilitySnapshot {
	if tr == nil {
		return resolveCapacityAvailabilityAt(SystemSettings{}, now)
	}
	return resolveCapacityAvailabilityWithLiveAt(tr.settings, tr.cachedLiveAvailableGeneratorCount(now), now)
}

func (tr *TestRunner) resolveCapacityAvailabilityForProjectAt(projectID string, now time.Time) CapacityAvailabilitySnapshot {
	if tr == nil {
		return resolveCapacityAvailabilityForProjectAt(SystemSettings{}, projectID, now)
	}
	return resolveCapacityAvailabilityForProjectWithLiveAt(tr.settings, projectID, tr.cachedLiveAvailableGeneratorCount(now), now)
}

func (tr *TestRunner) liveAvailableGeneratorCount(ctx context.Context) *int {
	if tr == nil || tr.settings.CapacityPlanning.Availability.Source != capacityAvailabilityConfiguredPlusLive {
		return nil
	}
	backend, ok := tr.execution.(inventoryExecutionBackend)
	if !ok {
		return nil
	}
	entries := backend.SystemInventory(ctx)
	return liveAvailableGeneratorCountFromEntries(entries)
}

func (tr *TestRunner) setCachedGeneratorInventory(entries []GeneratorInventoryEntry, now time.Time) {
	if tr == nil {
		return
	}
	tr.liveInventoryMu.Lock()
	defer tr.liveInventoryMu.Unlock()
	tr.liveInventory = cloneGeneratorInventoryEntries(entries)
	tr.liveInventoryAt = now.UTC()
}

// cachedLiveAvailableGeneratorCount returns the live-available generator count
// from the cached fleet inventory, or nil when the source isn't
// configured_plus_live or the cache is stale/empty. It MUST stay cheap and
// lock-free of any network I/O: capacity gates call it while holding tr.mu
// (evaluateQuotaBlockLocked / evaluateReservedCapacityBlockLocked), so a
// synchronous fleet probe here would stall the whole control plane. The cache is
// kept warm out-of-band by the background inventory-refresh cadence (see
// Server.startGeneratorInventoryRefreshCadence), so it stays fresh even on a
// headless controller with no dashboard traffic.
func (tr *TestRunner) cachedLiveAvailableGeneratorCount(now time.Time) *int {
	if tr == nil || tr.settings.CapacityPlanning.Availability.Source != capacityAvailabilityConfiguredPlusLive {
		return nil
	}
	tr.liveInventoryMu.RLock()
	entries := cloneGeneratorInventoryEntries(tr.liveInventory)
	capturedAt := tr.liveInventoryAt
	tr.liveInventoryMu.RUnlock()
	if len(entries) == 0 || capturedAt.IsZero() {
		return nil
	}
	if age := now.Sub(capturedAt); age < 0 || age > defaultGeneratorSnapshotTTL {
		return nil
	}
	return liveAvailableGeneratorCountFromEntries(entries)
}

func liveAvailableGeneratorCountFromEntries(entries []GeneratorInventoryEntry) *int {
	count := 0
	for _, entry := range entries {
		status := strings.ToLower(strings.TrimSpace(entry.Status))
		drainState := strings.ToLower(strings.TrimSpace(entry.DrainState))
		if drainState == "" {
			drainState = "active"
		}
		if drainState != "active" {
			continue
		}
		switch status {
		case "connected", "healthy", "ready":
			if entry.AvailableCapacity > 0 || entry.MaxVUsers == 0 {
				count++
			}
		}
	}
	return &count
}

// sharedGeneratorUsageLocked sums each project's draw on the shared generator
// pool: a project's active generators above its own reservation.
func (tr *TestRunner) sharedGeneratorUsageLocked(excludeTestID string, at time.Time, activeSlots []*storage.CapacityReservation) int {
	byProject := make(map[string]int)
	for id, test := range tr.tests {
		if test == nil || id == excludeTestID || !reservationActiveStatus(test.Status) {
			continue
		}
		// Pool-pinned runs draw from their pool's members, not the shared (non-pool)
		// envelope, so they don't count toward shared usage (the pool gate accounts
		// for them). Crediting their generators against a pool reservation here was
		// the aggregate side of the RN-1 double-spend.
		if testAssignedPoolID(test) != "" {
			continue
		}
		projectID := normalizeProjectID(test.ProjectID)
		byProject[projectID] = saturatingAddNonNegative(byProject[projectID], recommendedGeneratorCountForTest(test))
	}
	total := 0
	for projectID, active := range byProject {
		reserved := projectNonPoolReservedAt(tr.settings, projectID, at, activeSlots)
		total = saturatingAddNonNegative(total, maxInt(0, active-reserved))
	}
	return total
}

func reservationActiveStatus(status string) bool {
	return status == "RUNNING" || status == "INITIALIZING" || status == "PAUSED" || status == "STOPPING"
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

func enforceTestQuotaWithProjectQuota(quota projectQuotaSnapshot, totalVUsers int, dur time.Duration, status string) error {
	if status == "DRAFT" {
		return nil
	}
	if quota.maxVUsersPerTest > 0 && totalVUsers > quota.maxVUsersPerTest {
		return fmt.Errorf("test exceeds project per-test VUser cap (%d requested, %d max)", totalVUsers, quota.maxVUsersPerTest)
	}
	if quota.maxTestDuration > 0 {
		maxDur, ok := checkedDuration(quota.maxTestDuration, time.Minute)
		if !ok {
			return fmt.Errorf("project max test duration is invalid")
		}
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

type pendingQueueMetadataState struct {
	test                  *ManagedTest
	status                string
	projectID             string
	poolID                string
	totalVUsers           int
	recommendedGenerators int
	pendingSinceUnixNano  int64
	createdAtUnixNano     int64
	approvalID            string
	approvalPlanID        string
	approvalContentHash   string
	approvalStatus        string
	approvalPolicy        string
	approvedPeakVUsers    int
}

// loadPendingProjectQuotaEvidence resolves each distinct pending project once,
// outside tr.mu, then materializes per-test scalar evidence. Project persistence
// currently has no cross-controller revision probe, so the complete in-memory
// queue snapshot is revalidated before install and the next refresh reconciles a
// concurrent project-settings edit.
func loadPendingProjectQuotaEvidence(store projectPersistence, state map[string]pendingQueueMetadataState) map[*ManagedTest]projectQuotaEvidence {
	evidence := make(map[*ManagedTest]projectQuotaEvidence)
	type projectLoadResult struct {
		project *storage.Project
		err     error
	}
	projects := make(map[string]projectLoadResult)
	loaded := make(map[string]struct{})
	for _, entry := range state {
		if entry.test == nil || entry.status != "PENDING" {
			continue
		}
		projectID := normalizeProjectID(entry.projectID)
		if _, ok := loaded[projectID]; !ok {
			var result projectLoadResult
			if store != nil {
				result.project, result.err = store.GetProject(projectID)
			}
			projects[projectID] = result
			loaded[projectID] = struct{}{}
		}
		result := projects[projectID]
		updatedAt := time.Time{}
		if result.project != nil {
			updatedAt = result.project.UpdatedAt
		}
		evidence[entry.test] = projectQuotaEvidence{
			projectID:       projectID,
			poolID:          entry.poolID,
			quota:           projectQuotaSnapshotFromProject(result.project, entry.poolID),
			updatedAt:       updatedAt,
			storeConfigured: store != nil,
			projectResolved: store == nil || result.project != nil,
			loaded:          true,
			err:             result.err,
		}
	}
	return evidence
}

func (tr *TestRunner) sortPendingTestsForFairShareWithTimeslotsLocked(pending []*ManagedTest, now time.Time, activeSlots []*storage.CapacityReservation) {
	ranks := make(map[*ManagedTest]pendingFairShareRank, len(pending))
	for _, test := range pending {
		ranks[test] = tr.pendingFairShareRankWithTimeslotsLocked(test, now, activeSlots)
	}
	sort.Slice(pending, func(i, j int) bool {
		left := ranks[pending[i]]
		right := ranks[pending[j]]
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

func (tr *TestRunner) pendingFairShareRankWithTimeslotsLocked(test *ManagedTest, now time.Time, activeSlots []*storage.CapacityReservation) pendingFairShareRank {
	if test == nil {
		return pendingFairShareRank{reservationTier: 1, id: ""}
	}
	projectID := normalizeProjectID(test.ProjectID)
	reservationTier := 1
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
		if !testReservesRuntimeCapacity(test) {
			continue
		}
		total = saturatingAddNonNegative(total, recommendedGeneratorCountForTest(test))
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

func (tr *TestRunner) pendingQueueMetadataStateLocked() map[string]pendingQueueMetadataState {
	state := make(map[string]pendingQueueMetadataState, len(tr.tests))
	for id, test := range tr.tests {
		if test == nil {
			state[id] = pendingQueueMetadataState{}
			continue
		}
		entry := pendingQueueMetadataState{
			test: test, status: test.Status, projectID: normalizeProjectID(test.ProjectID),
			poolID: testAssignedPoolID(test), totalVUsers: managedTestCapacityVUsers(test),
			createdAtUnixNano: test.CreatedAt.UnixNano(),
		}
		if test.PendingSince != nil {
			entry.pendingSinceUnixNano = test.PendingSince.UnixNano()
		}
		if test.CapacityAdvisory != nil {
			entry.recommendedGenerators = test.CapacityAdvisory.RecommendedGenerators
		}
		if test.ExecutionPlanApproval != nil {
			entry.approvalID = test.ExecutionPlanApproval.ID
			entry.approvalPlanID = test.ExecutionPlanApproval.PlanID
			entry.approvalContentHash = test.ExecutionPlanApproval.PlanContentHash
			entry.approvalStatus = test.ExecutionPlanApproval.Status
			entry.approvalPolicy = test.ExecutionPlanApproval.Policy
			entry.approvedPeakVUsers = test.ExecutionPlanApproval.ApprovedPeakVUsers
		}
		state[id] = entry
	}
	return state
}

func samePendingQueueMetadataState(left, right map[string]pendingQueueMetadataState) bool {
	if len(left) != len(right) {
		return false
	}
	for id, expected := range left {
		if current, ok := right[id]; !ok || current != expected {
			return false
		}
	}
	return true
}

// refreshPendingQueueMetadata loads durable reservation evidence without tr.mu,
// then installs metadata only if the complete queue/capacity input snapshot is
// unchanged. A bounded retry prevents a stalled store from applying evidence to
// a different queue generation while keeping read/reconcile paths non-blocking.
func (tr *TestRunner) refreshPendingQueueMetadata() {
	if tr == nil {
		return
	}
	for attempt := 0; attempt < 2; attempt++ {
		tr.mu.RLock()
		expected := tr.pendingQueueMetadataStateLocked()
		projectStore := tr.projectStore
		hasPending := false
		for _, entry := range expected {
			if entry.status == "PENDING" {
				hasPending = true
				break
			}
		}
		tr.mu.RUnlock()

		now := time.Now()
		var activeSlots []*storage.CapacityReservation
		var reservationErr error
		if hasPending {
			activeSlots, reservationErr = tr.loadBoundTimeslotsActiveAt(now)
		}
		projectQuotas := loadPendingProjectQuotaEvidence(projectStore, expected)

		tr.mu.Lock()
		if !samePendingQueueMetadataState(expected, tr.pendingQueueMetadataStateLocked()) {
			tr.mu.Unlock()
			continue
		}
		tr.refreshPendingQueueMetadataWithTimeslotsLocked(now, activeSlots, reservationErr, projectQuotas)
		tr.mu.Unlock()
		return
	}
	slog.Debug("pending queue changed continuously while metadata evidence was loaded; leaving the previous projection for the next refresh")
}

func (tr *TestRunner) refreshPendingQueueMetadataWithTimeslotsLocked(now time.Time, activeSlots []*storage.CapacityReservation, reservationErr error, projectQuotas map[*ManagedTest]projectQuotaEvidence) {
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
	tr.sortPendingTestsForFairShareWithTimeslotsLocked(pending, now, activeSlots)
	for index, test := range pending {
		test.QueuePosition = index + 1
		projectQuota, quotaOK := projectQuotas[test].quotaFor(test)
		blocked := !quotaOK
		reason := "waiting for project quota evidence to become available"
		if quotaOK {
			blocked, reason = tr.evaluateQuotaBlockWithTimeslotsAndProjectQuotaLocked(test, "", now, activeSlots, reservationErr, projectQuota)
		}
		if blocked {
			test.PendingReason = reason
		} else if !pendingPromotionReasonMustRemainVisible(test.PendingReason) {
			test.PendingReason = "ready to start when the queue advances"
		}
	}
}

// pendingPromotionReasonMustRemainVisible protects the last durable admission
// failure from the queue-position projection above. Promotion gates run after
// the ordinary quota check, so replacing their reason with "ready" makes a
// Java runner/capacity outage (and other correctable promotion failures) look
// launchable even though the next promotion attempt will reject it again.
func pendingPromotionReasonMustRemainVisible(reason string) bool {
	return strings.HasPrefix(strings.TrimSpace(reason), "pending test promotion ")
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
	capacityVUsers := trafficProfileCapacityVUsers(test.TrafficProfile, groups, test.Duration, test.GoalConfig)
	if capacityVUsers <= 0 {
		capacityVUsers = test.TotalVUsers
	}
	test.CapacityAdvisory = tr.estimateCapacityAdvisoryForAssignment(test.ProjectID, groups, capacityVUsers, test.Duration, test.ResourceBaseline, test.RuntimeSettings, test.Assignment)
}
