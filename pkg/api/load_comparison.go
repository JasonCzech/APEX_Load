package api

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	LoadComparisonScopeTransaction = "transaction"
	LoadComparisonScopeGroup       = "group"
	maxLoadComparisonBuckets       = 8
)

type LoadComparisonResponse struct {
	ApplicationID string                        `json:"application_id"`
	Scope         string                        `json:"scope"`
	SelectedKey   string                        `json:"selected_key,omitempty"`
	Options       []LoadComparisonOption        `json:"options"`
	LoadLevels    []LoadComparisonLoadLevel     `json:"load_levels"`
	Releases      []LoadComparisonReleaseSeries `json:"releases"`
}

type LoadComparisonOption struct {
	Key              string `json:"key"`
	Label            string `json:"label"`
	Scope            string `json:"scope"`
	SourceKind       string `json:"source_kind"`
	ReleaseCount     int    `json:"release_count"`
	RunCount         int    `json:"run_count"`
	TransactionCount int64  `json:"transaction_count"`
	SampleCount      int    `json:"sample_count"`
}

type LoadComparisonLoadLevel struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	MinVUsers   int    `json:"min_vusers"`
	MaxVUsers   int    `json:"max_vusers"`
	Exact       bool   `json:"exact"`
	SampleCount int    `json:"sample_count"`
}

type LoadComparisonReleaseSeries struct {
	ReleaseID        string                `json:"release_id"`
	ReleaseName      string                `json:"release_name"`
	RunCount         int                   `json:"run_count"`
	TransactionCount int64                 `json:"transaction_count"`
	Points           []LoadComparisonPoint `json:"points"`
}

type LoadComparisonPoint struct {
	LoadLevelID   string  `json:"load_level_id"`
	LoadLabel     string  `json:"load_label"`
	TPS           float64 `json:"tps"`
	AvgResponseMs float64 `json:"avg_response_ms"`
	Transactions  int64   `json:"transactions"`
	Errors        int64   `json:"errors"`
	ErrorPct      float64 `json:"error_pct"`
	RunCount      int     `json:"run_count"`
	SampleCount   int     `json:"sample_count"`
}

type loadComparisonRun struct {
	Test             *ManagedTest
	ReleaseID        string
	ReleaseName      string
	ReleaseCreatedAt time.Time
	Transactions     []TransactionEvent
	Summaries        []IntervalSummaryRecord
}

type loadComparisonOptionTracker struct {
	Key              string
	Label            string
	Scope            string
	SourceKind       string
	ReleaseIDs       map[string]struct{}
	RunIDs           map[string]struct{}
	TransactionCount int64
	SampleCount      int
}

type loadComparisonReleaseAccumulator struct {
	ReleaseID        string
	ReleaseName      string
	ReleaseCreatedAt time.Time
	RunIDs           map[string]struct{}
	ExactLoads       map[int]*loadComparisonMetricAccumulator
}

type loadComparisonMetricAccumulator struct {
	TPSNumerator   float64
	TPSSeconds     float64
	ResponseTotal  float64
	ResponseWeight float64
	Transactions   int64
	Errors         int64
	SampleCount    int
	RunIDs         map[string]struct{}
}

type transactionIntervalLoadKey struct {
	Index int
	Load  int
}

type transactionIntervalAccumulator struct {
	Transactions  int64
	Errors        int64
	DurationTotal float64
}

func normalizeLoadComparisonScope(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", LoadComparisonScopeTransaction:
		return LoadComparisonScopeTransaction, true
	case LoadComparisonScopeGroup:
		return LoadComparisonScopeGroup, true
	default:
		return "", false
	}
}

func buildLoadComparison(applicationID string, scope string, selectedKey string, runs []loadComparisonRun) LoadComparisonResponse {
	normalizedScope, ok := normalizeLoadComparisonScope(scope)
	if !ok {
		normalizedScope = LoadComparisonScopeTransaction
	}
	response := LoadComparisonResponse{
		ApplicationID: applicationID,
		Scope:         normalizedScope,
	}
	options := loadComparisonOptions(normalizedScope, runs)
	response.Options = options
	selectedKey = strings.TrimSpace(selectedKey)
	if selectedKey == "" || !loadComparisonOptionExists(options, selectedKey) {
		selectedKey = defaultLoadComparisonOptionKey(options)
	}
	response.SelectedKey = selectedKey
	if selectedKey == "" {
		return response
	}

	releases, loadSamples := accumulateLoadComparison(normalizedScope, selectedKey, runs)
	response.LoadLevels = buildLoadComparisonLoadLevels(loadSamples)
	response.Releases = finalizeLoadComparisonReleases(releases, response.LoadLevels)
	return response
}

func loadComparisonOptions(scope string, runs []loadComparisonRun) []LoadComparisonOption {
	trackers := make(map[string]*loadComparisonOptionTracker)
	for _, run := range runs {
		if run.Test == nil || strings.TrimSpace(run.ReleaseID) == "" {
			continue
		}
		switch scope {
		case LoadComparisonScopeGroup:
			collectGroupLoadComparisonOptions(trackers, run)
		default:
			collectTransactionLoadComparisonOptions(trackers, run)
		}
	}

	options := make([]LoadComparisonOption, 0, len(trackers))
	for _, tracker := range trackers {
		options = append(options, LoadComparisonOption{
			Key:              tracker.Key,
			Label:            tracker.Label,
			Scope:            tracker.Scope,
			SourceKind:       tracker.SourceKind,
			ReleaseCount:     len(tracker.ReleaseIDs),
			RunCount:         len(tracker.RunIDs),
			TransactionCount: tracker.TransactionCount,
			SampleCount:      tracker.SampleCount,
		})
	}
	sort.SliceStable(options, func(i, j int) bool {
		if options[i].TransactionCount != options[j].TransactionCount {
			return options[i].TransactionCount > options[j].TransactionCount
		}
		if options[i].SampleCount != options[j].SampleCount {
			return options[i].SampleCount > options[j].SampleCount
		}
		return strings.ToLower(options[i].Label) < strings.ToLower(options[j].Label)
	})
	return options
}

func collectGroupLoadComparisonOptions(trackers map[string]*loadComparisonOptionTracker, run loadComparisonRun) {
	for _, summary := range run.Summaries {
		for name, group := range summary.Groups {
			key := strings.TrimSpace(name)
			if group != nil && strings.TrimSpace(group.Name) != "" {
				key = strings.TrimSpace(group.Name)
			}
			if key == "" {
				continue
			}
			tracker := loadComparisonOptionTrackerFor(trackers, key, key, LoadComparisonScopeGroup, "group")
			tracker.ReleaseIDs[run.ReleaseID] = struct{}{}
			tracker.RunIDs[run.Test.ID] = struct{}{}
			tracker.SampleCount++
			if group != nil && group.Transactions > 0 {
				tracker.TransactionCount += group.Transactions
			}
		}
	}
}

func collectTransactionLoadComparisonOptions(trackers map[string]*loadComparisonOptionTracker, run loadComparisonRun) {
	for _, tx := range run.Transactions {
		key, sourceKind := loadComparisonTransactionIdentity(tx)
		if key == "" {
			continue
		}
		tracker := loadComparisonOptionTrackerFor(trackers, key, key, LoadComparisonScopeTransaction, sourceKind)
		tracker.ReleaseIDs[run.ReleaseID] = struct{}{}
		tracker.RunIDs[run.Test.ID] = struct{}{}
		tracker.TransactionCount++
		tracker.SampleCount++
	}
}

func loadComparisonOptionTrackerFor(trackers map[string]*loadComparisonOptionTracker, key string, label string, scope string, sourceKind string) *loadComparisonOptionTracker {
	tracker := trackers[key]
	if tracker == nil {
		tracker = &loadComparisonOptionTracker{
			Key:        key,
			Label:      label,
			Scope:      scope,
			SourceKind: sourceKind,
			ReleaseIDs: make(map[string]struct{}),
			RunIDs:     make(map[string]struct{}),
		}
		trackers[key] = tracker
	}
	if tracker.SourceKind != sourceKind && tracker.SourceKind != "source_transaction" {
		tracker.SourceKind = sourceKind
	}
	return tracker
}

func loadComparisonOptionExists(options []LoadComparisonOption, key string) bool {
	for _, option := range options {
		if option.Key == key {
			return true
		}
	}
	return false
}

func defaultLoadComparisonOptionKey(options []LoadComparisonOption) string {
	if len(options) == 0 {
		return ""
	}
	return options[0].Key
}

func accumulateLoadComparison(scope string, selectedKey string, runs []loadComparisonRun) ([]*loadComparisonReleaseAccumulator, map[int]int) {
	releasesByID := make(map[string]*loadComparisonReleaseAccumulator)
	loadSamples := make(map[int]int)
	for _, run := range runs {
		if run.Test == nil || strings.TrimSpace(run.ReleaseID) == "" {
			continue
		}
		release := loadComparisonReleaseAccumulatorFor(releasesByID, run)
		switch scope {
		case LoadComparisonScopeGroup:
			accumulateGroupLoadComparison(release, loadSamples, selectedKey, run)
		default:
			accumulateTransactionLoadComparison(release, loadSamples, selectedKey, run)
		}
	}
	releases := make([]*loadComparisonReleaseAccumulator, 0, len(releasesByID))
	for _, release := range releasesByID {
		if len(release.ExactLoads) > 0 {
			releases = append(releases, release)
		}
	}
	sort.SliceStable(releases, func(i, j int) bool {
		if !releases[i].ReleaseCreatedAt.Equal(releases[j].ReleaseCreatedAt) {
			if releases[i].ReleaseCreatedAt.IsZero() {
				return false
			}
			if releases[j].ReleaseCreatedAt.IsZero() {
				return true
			}
			return releases[i].ReleaseCreatedAt.Before(releases[j].ReleaseCreatedAt)
		}
		if releases[i].ReleaseName != releases[j].ReleaseName {
			return strings.ToLower(releases[i].ReleaseName) < strings.ToLower(releases[j].ReleaseName)
		}
		return releases[i].ReleaseID < releases[j].ReleaseID
	})
	return releases, loadSamples
}

func loadComparisonReleaseAccumulatorFor(releases map[string]*loadComparisonReleaseAccumulator, run loadComparisonRun) *loadComparisonReleaseAccumulator {
	release := releases[run.ReleaseID]
	if release == nil {
		release = &loadComparisonReleaseAccumulator{
			ReleaseID:        run.ReleaseID,
			ReleaseName:      firstNonEmpty(run.ReleaseName, run.ReleaseID),
			ReleaseCreatedAt: run.ReleaseCreatedAt,
			RunIDs:           make(map[string]struct{}),
			ExactLoads:       make(map[int]*loadComparisonMetricAccumulator),
		}
		releases[run.ReleaseID] = release
	}
	if release.ReleaseName == "" && run.ReleaseName != "" {
		release.ReleaseName = run.ReleaseName
	}
	if release.ReleaseCreatedAt.IsZero() && !run.ReleaseCreatedAt.IsZero() {
		release.ReleaseCreatedAt = run.ReleaseCreatedAt
	}
	release.RunIDs[run.Test.ID] = struct{}{}
	return release
}

func accumulateGroupLoadComparison(release *loadComparisonReleaseAccumulator, loadSamples map[int]int, selectedKey string, run loadComparisonRun) {
	for index, summary := range run.Summaries {
		group := loadComparisonGroupForSummary(summary, selectedKey)
		if group == nil {
			continue
		}
		load := group.VUsers
		if load <= 0 {
			continue
		}
		seconds := archiveSummaryBucketSeconds(run.Summaries, index)
		if seconds <= 0 {
			seconds = 1
		}
		acc := release.loadAccumulator(load)
		acc.SampleCount++
		acc.RunIDs[run.Test.ID] = struct{}{}
		acc.TPSNumerator += group.TPS * seconds
		acc.TPSSeconds += seconds
		responseWeight := float64(group.Transactions)
		if responseWeight <= 0 {
			responseWeight = 1
		}
		acc.ResponseTotal += group.AvgResponseMs * responseWeight
		acc.ResponseWeight += responseWeight
		acc.Transactions += group.Transactions
		acc.Errors += group.Errors
		loadSamples[load]++
	}
}

func accumulateTransactionLoadComparison(release *loadComparisonReleaseAccumulator, loadSamples map[int]int, selectedKey string, run loadComparisonRun) {
	if len(run.Summaries) == 0 {
		return
	}
	intervals := make(map[transactionIntervalLoadKey]*transactionIntervalAccumulator)
	for _, tx := range run.Transactions {
		key, _ := loadComparisonTransactionIdentity(tx)
		if key != selectedKey {
			continue
		}
		index := archiveSummaryBucketIndex(run.Summaries, tx.Timestamp)
		if index < 0 || index >= len(run.Summaries) {
			continue
		}
		load := transactionLoadForSummary(tx, run.Summaries[index])
		if load <= 0 {
			continue
		}
		intervalKey := transactionIntervalLoadKey{Index: index, Load: load}
		interval := intervals[intervalKey]
		if interval == nil {
			interval = &transactionIntervalAccumulator{}
			intervals[intervalKey] = interval
		}
		interval.Transactions++
		interval.DurationTotal += tx.DurationMs
		if !tx.Success {
			interval.Errors++
		}
	}

	for intervalKey, interval := range intervals {
		if interval.Transactions == 0 {
			continue
		}
		seconds := archiveSummaryBucketSeconds(run.Summaries, intervalKey.Index)
		if seconds <= 0 {
			seconds = 1
		}
		acc := release.loadAccumulator(intervalKey.Load)
		acc.SampleCount++
		acc.RunIDs[run.Test.ID] = struct{}{}
		acc.TPSNumerator += float64(interval.Transactions)
		acc.TPSSeconds += seconds
		acc.ResponseTotal += interval.DurationTotal
		acc.ResponseWeight += float64(interval.Transactions)
		acc.Transactions += interval.Transactions
		acc.Errors += interval.Errors
		loadSamples[intervalKey.Load]++
	}
}

func (release *loadComparisonReleaseAccumulator) loadAccumulator(load int) *loadComparisonMetricAccumulator {
	acc := release.ExactLoads[load]
	if acc == nil {
		acc = &loadComparisonMetricAccumulator{RunIDs: make(map[string]struct{})}
		release.ExactLoads[load] = acc
	}
	return acc
}

func transactionLoadForSummary(tx TransactionEvent, summary IntervalSummaryRecord) int {
	if tx.Group != "" {
		if group := loadComparisonGroupForSummary(summary, tx.Group); group != nil && group.VUsers > 0 {
			return group.VUsers
		}
	}
	return int(summary.ActiveVUsers)
}

func loadComparisonGroupForSummary(summary IntervalSummaryRecord, key string) *GroupLiveMetrics {
	key = strings.TrimSpace(key)
	if key == "" || len(summary.Groups) == 0 {
		return nil
	}
	if group := summary.Groups[key]; group != nil {
		return group
	}
	for name, group := range summary.Groups {
		if strings.EqualFold(strings.TrimSpace(name), key) {
			return group
		}
		if group != nil && strings.EqualFold(strings.TrimSpace(group.Name), key) {
			return group
		}
	}
	return nil
}

func loadComparisonTransactionIdentity(tx TransactionEvent) (string, string) {
	if sourceTransaction := strings.TrimSpace(tx.SourceTransaction); sourceTransaction != "" {
		return sourceTransaction, "source_transaction"
	}
	if actionName := strings.TrimSpace(tx.ActionName); actionName != "" {
		return actionName, "action"
	}
	return "", ""
}

func buildLoadComparisonLoadLevels(loadSamples map[int]int) []LoadComparisonLoadLevel {
	if len(loadSamples) == 0 {
		return nil
	}
	loads := make([]int, 0, len(loadSamples))
	for load := range loadSamples {
		if load > 0 {
			loads = append(loads, load)
		}
	}
	sort.Ints(loads)
	if len(loads) == 0 {
		return nil
	}
	if len(loads) <= 10 {
		levels := make([]LoadComparisonLoadLevel, 0, len(loads))
		for _, load := range loads {
			levels = append(levels, LoadComparisonLoadLevel{
				ID:          loadComparisonBucketID(load, load),
				Label:       formatLoadComparisonVUserLabel(load, load),
				MinVUsers:   load,
				MaxVUsers:   load,
				Exact:       true,
				SampleCount: loadSamples[load],
			})
		}
		return levels
	}

	bucketCount := minInt(maxLoadComparisonBuckets, len(loads))
	minLoad := loads[0]
	maxLoad := loads[len(loads)-1]
	loadRange := maxLoad - minLoad + 1
	levels := make([]LoadComparisonLoadLevel, 0, bucketCount)
	for index := 0; index < bucketCount; index++ {
		minVUsers := minLoad + int(math.Floor(float64(index*loadRange)/float64(bucketCount)))
		maxVUsers := maxLoad
		if index < bucketCount-1 {
			maxVUsers = minLoad + int(math.Floor(float64((index+1)*loadRange)/float64(bucketCount))) - 1
		}
		samples := 0
		for _, load := range loads {
			if load >= minVUsers && load <= maxVUsers {
				samples += loadSamples[load]
			}
		}
		levels = append(levels, LoadComparisonLoadLevel{
			ID:          loadComparisonBucketID(minVUsers, maxVUsers),
			Label:       formatLoadComparisonVUserLabel(minVUsers, maxVUsers),
			MinVUsers:   minVUsers,
			MaxVUsers:   maxVUsers,
			Exact:       false,
			SampleCount: samples,
		})
	}
	return levels
}

func finalizeLoadComparisonReleases(releases []*loadComparisonReleaseAccumulator, levels []LoadComparisonLoadLevel) []LoadComparisonReleaseSeries {
	if len(levels) == 0 {
		return nil
	}
	series := make([]LoadComparisonReleaseSeries, 0, len(releases))
	for _, release := range releases {
		byLevel := make(map[string]*loadComparisonMetricAccumulator)
		for load, exactAcc := range release.ExactLoads {
			level, ok := loadComparisonLevelForLoad(load, levels)
			if !ok {
				continue
			}
			levelAcc := byLevel[level.ID]
			if levelAcc == nil {
				levelAcc = &loadComparisonMetricAccumulator{RunIDs: make(map[string]struct{})}
				byLevel[level.ID] = levelAcc
			}
			levelAcc.merge(exactAcc)
		}
		points := make([]LoadComparisonPoint, 0, len(levels))
		var transactions int64
		pointRunIDs := make(map[string]struct{})
		for _, level := range levels {
			acc := byLevel[level.ID]
			if acc == nil || acc.SampleCount == 0 {
				continue
			}
			point := acc.point(level)
			transactions += point.Transactions
			for runID := range acc.RunIDs {
				pointRunIDs[runID] = struct{}{}
			}
			points = append(points, point)
		}
		if len(points) == 0 {
			continue
		}
		series = append(series, LoadComparisonReleaseSeries{
			ReleaseID:        release.ReleaseID,
			ReleaseName:      firstNonEmpty(release.ReleaseName, release.ReleaseID),
			RunCount:         len(pointRunIDs),
			TransactionCount: transactions,
			Points:           points,
		})
	}
	return series
}

func (acc *loadComparisonMetricAccumulator) merge(other *loadComparisonMetricAccumulator) {
	if other == nil {
		return
	}
	acc.TPSNumerator += other.TPSNumerator
	acc.TPSSeconds += other.TPSSeconds
	acc.ResponseTotal += other.ResponseTotal
	acc.ResponseWeight += other.ResponseWeight
	acc.Transactions += other.Transactions
	acc.Errors += other.Errors
	acc.SampleCount += other.SampleCount
	if acc.RunIDs == nil {
		acc.RunIDs = make(map[string]struct{})
	}
	for runID := range other.RunIDs {
		acc.RunIDs[runID] = struct{}{}
	}
}

func (acc *loadComparisonMetricAccumulator) point(level LoadComparisonLoadLevel) LoadComparisonPoint {
	point := LoadComparisonPoint{
		LoadLevelID:  level.ID,
		LoadLabel:    level.Label,
		Transactions: acc.Transactions,
		Errors:       acc.Errors,
		RunCount:     len(acc.RunIDs),
		SampleCount:  acc.SampleCount,
	}
	if acc.TPSSeconds > 0 {
		point.TPS = acc.TPSNumerator / acc.TPSSeconds
	}
	if acc.ResponseWeight > 0 {
		point.AvgResponseMs = acc.ResponseTotal / acc.ResponseWeight
	}
	if acc.Transactions > 0 {
		point.ErrorPct = float64(acc.Errors) / float64(acc.Transactions) * 100
	}
	return point
}

func loadComparisonLevelForLoad(load int, levels []LoadComparisonLoadLevel) (LoadComparisonLoadLevel, bool) {
	for _, level := range levels {
		if level.Exact {
			if load == level.MinVUsers {
				return level, true
			}
			continue
		}
		if load >= level.MinVUsers && load <= level.MaxVUsers {
			return level, true
		}
	}
	return LoadComparisonLoadLevel{}, false
}

func loadComparisonBucketID(minVUsers int, maxVUsers int) string {
	if minVUsers == maxVUsers {
		return "vusers-" + strconv.Itoa(minVUsers)
	}
	return "vusers-" + strconv.Itoa(minVUsers) + "-" + strconv.Itoa(maxVUsers)
}

func formatLoadComparisonVUserLabel(minVUsers int, maxVUsers int) string {
	suffix := "VUsers"
	if minVUsers == maxVUsers && minVUsers == 1 {
		suffix = "VUser"
	}
	if minVUsers == maxVUsers {
		return strconv.Itoa(minVUsers) + " " + suffix
	}
	return strconv.Itoa(minVUsers) + "-" + strconv.Itoa(maxVUsers) + " VUsers"
}
