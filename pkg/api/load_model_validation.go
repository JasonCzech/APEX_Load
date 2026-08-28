package api

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type LoadModelValidationReport struct {
	Status                   string                    `json:"status"`
	StatusCounts             map[string]int            `json:"status_counts"`
	Items                    []LoadModelValidationItem `json:"items"`
	TotalVUsers              int                       `json:"total_vusers"`
	PeakTargetTPS            float64                   `json:"peak_target_tps,omitempty"`
	CapacityAdvisory         *CapacityAdvisory         `json:"capacity_advisory,omitempty"`
	AssignmentRecommendation *AssignmentRecommendation `json:"assignment_recommendation,omitempty"`
	RuntimeSettingsReview    *RuntimeSettingsReview    `json:"runtime_settings_review,omitempty"`
}

type LoadModelValidationItem struct {
	Dimension     string  `json:"dimension"`
	Group         string  `json:"group,omitempty"`
	Status        string  `json:"status"`
	Summary       string  `json:"summary"`
	RequiredRows  int     `json:"required_rows,omitempty"`
	AvailableRows int     `json:"available_rows,omitempty"`
	TargetVUsers  int     `json:"target_vusers,omitempty"`
	TargetTPS     float64 `json:"target_tps,omitempty"`
}

func (tr *TestRunner) ValidateScenarioLoadModel(req CreateScenarioRequest) (*LoadModelValidationReport, error) {
	// Apply the same canonical binding defaults and duplicate/enum checks as a
	// scenario save before any pool lookup. This also gives the ownership gate a
	// normalized pool ID/alias set and prevents validation from becoming a row
	// read primitive for another project.
	bindings, err := normalizeScenarioDataPoolBindings("draft-load-model-validation", req.DataPools)
	if err != nil {
		return nil, err
	}
	req.DataPools = bindings

	testReq := createTestRequestFromScenarioDraft(req)
	preview, snapshot, buildRevision, err := tr.buildManagedTestFromSnapshot(testReq, "draft-load-model-validation", "DRAFT")
	if err != nil {
		return nil, err
	}

	recommendation := snapshot.buildAssignmentRecommendation(preview)
	report := &LoadModelValidationReport{
		Items:                    make([]LoadModelValidationItem, 0, 8),
		TotalVUsers:              preview.TotalVUsers,
		CapacityAdvisory:         cloneCapacityAdvisory(preview.CapacityAdvisory),
		AssignmentRecommendation: recommendation,
		RuntimeSettingsReview:    cloneRuntimeSettingsReview(preview.RuntimeSettingsReview),
	}

	report.Items = append(report.Items, validateModeledDemand(preview.Groups)...)
	report.Items = append(report.Items, validateRuntimeSettingsReview(preview.RuntimeSettingsReview)...)
	report.Items = append(report.Items, validateRendezvousDemand(preview.Groups)...)
	report.Items = append(report.Items, validateCapacityAdvisory(preview.CapacityAdvisory)...)
	report.Items = append(report.Items, validateBrowserCanaryDemand(preview.CapacityAdvisory)...)
	report.Items = append(report.Items, validateAssignmentPlacement(preview, recommendation)...)
	snapshot.mu.RLock()
	report.Items = append(report.Items, snapshot.validateGroupPlacementLocked(preview)...)
	poolStore := snapshot.dataPoolStore
	snapshot.mu.RUnlock()
	if !tr.managedTestBuildFenceCurrent(buildRevision) {
		return nil, fmt.Errorf("controller content changed while load-model validation was being compiled; retry")
	}

	// Pool ownership and row reads are durable I/O and may wait on a remote SQL
	// transaction. The compiled preview above is request-local, so release the
	// global runner lock before touching the store while preserving the security
	// rule that ownership is checked before any row is read.
	if err := validateDataPoolProjects(poolStore, preview.ProjectID, bindings); err != nil {
		return nil, err
	}
	dataSourceItems, peakTPS := validateLoadModelDataSources(poolStore, req.DataPools, preview.Groups)
	report.PeakTargetTPS = peakTPS
	report.Items = append(report.Items, dataSourceItems...)
	if !tr.managedTestBuildFenceCurrent(buildRevision) {
		return nil, fmt.Errorf("controller content changed while load-model validation was being evaluated; retry")
	}

	finalizeLoadModelValidationReport(report)
	return report, nil
}

func createTestRequestFromScenarioDraft(req CreateScenarioRequest) CreateTestRequest {
	return CreateTestRequest{
		Name:             req.Name,
		Duration:         req.Duration,
		StartDelay:       req.StartDelay,
		ScenarioMode:     req.ScenarioMode,
		ScriptShakeout:   req.ScriptShakeout,
		ResourceBaseline: req.ResourceBaseline,
		Assignment:       cloneGeneratorAssignmentSettings(req.Assignment),
		GoalConfig:       cloneGoalConfig(req.GoalConfig),
		TrafficProfile:   cloneTrafficProfile(req.TrafficProfile),
		RuntimeSettings:  cloneRuntimeSettings(req.RuntimeSettings),
		Groups:           append([]CreateGroupEntry(nil), req.Groups...),
	}
}

func validateModeledDemand(groups []TestGroup) []LoadModelValidationItem {
	items := make([]LoadModelValidationItem, 0, len(groups))
	for _, group := range groups {
		maxStageVUsers := 0
		maxStageTPS := 0.0
		for _, stage := range group.Stages {
			if stage.TargetVUsers > maxStageVUsers {
				maxStageVUsers = stage.TargetVUsers
			}
			if stage.TargetTPS != nil && *stage.TargetTPS > maxStageTPS {
				maxStageTPS = *stage.TargetTPS
			}
		}
		if len(group.Stages) == 0 {
			items = append(items, LoadModelValidationItem{
				Dimension:    "modeled_demand",
				Group:        group.Name,
				Status:       "ready",
				Summary:      fmt.Sprintf("Closed workload uses %d VUser%s.", group.VUsers, pluralSuffix(group.VUsers)),
				TargetVUsers: group.VUsers,
			})
			continue
		}
		summary := fmt.Sprintf("Staged workload preserves %d segment%s with peak %d VUser%s.", len(group.Stages), pluralSuffix(len(group.Stages)), maxStageVUsers, pluralSuffix(maxStageVUsers))
		if maxStageTPS > 0 {
			summary = fmt.Sprintf("%s Peak target throughput is %.2f TPS.", summary, maxStageTPS)
		}
		items = append(items, LoadModelValidationItem{
			Dimension:    "modeled_demand",
			Group:        group.Name,
			Status:       "ready",
			Summary:      summary,
			TargetVUsers: maxStageVUsers,
			TargetTPS:    roundCapacityValue(maxStageTPS),
		})
	}
	return items
}

func validateRuntimeSettingsReview(review *RuntimeSettingsReview) []LoadModelValidationItem {
	if review == nil {
		return []LoadModelValidationItem{{
			Dimension: "runtime_settings",
			Status:    "ready",
			Summary:   "No non-default runtime settings require review.",
		}}
	}
	status := "ready"
	switch review.Status {
	case "blocked":
		status = "blocked"
	case "needs_review":
		status = "needs_review"
	}
	summary := review.Summary
	if strings.TrimSpace(summary) == "" {
		summary = "Runtime settings are executable for the selected scripts and overrides."
	}
	return []LoadModelValidationItem{{
		Dimension: "runtime_settings",
		Status:    status,
		Summary:   summary,
	}}
}

func validateRendezvousDemand(groups []TestGroup) []LoadModelValidationItem {
	items := make([]LoadModelValidationItem, 0)
	for _, group := range groups {
		if group.script == nil {
			continue
		}
		count := 0
		for _, action := range group.script.Actions {
			if strings.EqualFold(strings.TrimSpace(action.Type), "rendezvous") {
				count++
			}
		}
		if count == 0 {
			continue
		}
		items = append(items, LoadModelValidationItem{
			Dimension:    "rendezvous",
			Group:        group.Name,
			Status:       "ready",
			Summary:      fmt.Sprintf("Script contains %d rendezvous point%s for synchronized VUser release.", count, pluralSuffix(count)),
			TargetVUsers: group.VUsers,
		})
	}
	return items
}

func validateCapacityAdvisory(advisory *CapacityAdvisory) []LoadModelValidationItem {
	if advisory == nil {
		return []LoadModelValidationItem{{Dimension: "capacity", Status: "needs_review", Summary: "Capacity advisory could not be computed for this scenario draft."}}
	}
	status := "ready"
	summary := fmt.Sprintf("Recommended %d generator%s with %d available.", advisory.RecommendedGenerators, pluralSuffix(advisory.RecommendedGenerators), advisory.AvailableGenerators)
	if advisory.RequiresOverride {
		status = "blocked"
		summary = fmt.Sprintf("Capacity shortfall of %d generator%s requires override before execution.", advisory.ShortfallGenerators, pluralSuffix(advisory.ShortfallGenerators))
	}
	return []LoadModelValidationItem{{Dimension: "capacity", Status: status, Summary: summary}}
}

func validateBrowserCanaryDemand(advisory *CapacityAdvisory) []LoadModelValidationItem {
	if advisory == nil || advisory.BrowserVUsers == 0 {
		return nil
	}
	status := "ready"
	availableBrowserSlots := advisory.AvailableGenerators * advisory.MaxBrowserVUsersPerGenerator
	summary := fmt.Sprintf("Browser canary demand uses %d VUser%s across a %d VUser/generator density envelope.", advisory.BrowserVUsers, pluralSuffix(advisory.BrowserVUsers), advisory.MaxBrowserVUsersPerGenerator)
	if advisory.MaxBrowserVUsersPerGenerator == 0 {
		status = "needs_review"
		summary = "Browser canary density could not be resolved; verify generator host class before execution."
	} else if advisory.BrowserVUsers > availableBrowserSlots {
		status = "blocked"
		summary = fmt.Sprintf("Browser canary demand needs %d browser slot%s but current placement exposes %d.", advisory.BrowserVUsers, pluralSuffix(advisory.BrowserVUsers), availableBrowserSlots)
	}
	return []LoadModelValidationItem{{Dimension: "browser_canary", Status: status, Summary: summary, TargetVUsers: advisory.BrowserVUsers}}
}

func validateAssignmentPlacement(test *ManagedTest, recommendation *AssignmentRecommendation) []LoadModelValidationItem {
	if recommendation == nil {
		return nil
	}
	status := "ready"
	summary := recommendation.Summary
	if strings.TrimSpace(summary) == "" {
		summary = "Generator placement can be resolved automatically."
	}
	if len(recommendation.Warnings) > 0 {
		status = "needs_review"
		summary = strings.Join(recommendation.Warnings, " ")
	}
	if test != nil && test.CapacityAdvisory != nil && test.CapacityAdvisory.RequiresOverride {
		status = "blocked"
	}
	return []LoadModelValidationItem{{Dimension: "generator_placement", Status: status, Summary: summary}}
}

// validateGroupPlacementLocked surfaces per-group placement eligibility in the
// scenario-builder load-model report using the SAME evaluation as launch preflight
// (M1), so a group pinned to an incompatible/ineligible host is reported "blocked"
// here instead of silently showing "ready" until launch preflight blocks it.
func (tr *TestRunner) validateGroupPlacementLocked(test *ManagedTest) []LoadModelValidationItem {
	conflicts := tr.evaluateGeneratorPlacementConflictsLocked(test)
	if len(conflicts) == 0 {
		return nil
	}
	items := make([]LoadModelValidationItem, 0, len(conflicts))
	for _, conflict := range conflicts {
		items = append(items, LoadModelValidationItem{
			Dimension: "generator_placement",
			Group:     conflict.Group,
			Status:    "blocked",
			Summary:   conflict.Message,
		})
	}
	return items
}

func validateLoadModelDataSources(poolStore dataPoolPersistence, bindings []storage.ScenarioDataPoolBinding, groups []TestGroup) ([]LoadModelValidationItem, float64) {
	items := make([]LoadModelValidationItem, 0)
	sharedSources, poolItems, poolRowCounts := validateLoadModelDataPoolBindings(poolStore, bindings)
	items = append(items, poolItems...)

	peakTPS := 0.0
	for _, group := range groups {
		for _, stage := range group.Stages {
			if stage.TargetTPS != nil {
				peakTPS = math.Max(peakTPS, *stage.TargetTPS)
			}
		}
		if group.script == nil {
			continue
		}
		scriptCopy := *group.script
		if len(sharedSources) > 0 {
			scriptCopy.DataSources = append(cloneDataSources(scriptCopy.DataSources), sharedSources...)
			items = append(items, dataPoolStrategyItems(
				group.Name,
				dataPoolStrategyVUsers(group),
				scriptCopy.Config.RunLogic.Iterations,
				scriptCopy.Config.RunLogic.GlobalIterations,
				sharedSources,
				poolRowCounts,
			)...)
		}
		maxVUsers := dataPoolStrategyVUsers(group)
		scriptCopy.GoalConfig = cloneDSLGoalConfig(scriptCopy.GoalConfig)
		if scriptCopy.GoalConfig == nil {
			scriptCopy.GoalConfig = &dsl.GoalConfig{}
		}
		if scriptCopy.GoalConfig.MaxVUsers == nil || *scriptCopy.GoalConfig.MaxVUsers < maxVUsers {
			scriptCopy.GoalConfig.MaxVUsers = &maxVUsers
		}
		report := buildDataSourceFitReport(&scriptCopy)
		if report == nil {
			continue
		}
		for _, item := range report.Items {
			items = append(items, LoadModelValidationItem{
				Dimension:     "data_source_fit",
				Group:         group.Name,
				Status:        item.Status,
				Summary:       item.Summary,
				RequiredRows:  item.RequiredRows,
				AvailableRows: item.AvailableRows,
			})
		}
	}
	if len(bindings) == 0 && !hasDataSourceFitItem(items) {
		items = append(items, LoadModelValidationItem{Dimension: "data_pool_rows", Status: "ready", Summary: "No shared data pools are bound to this scenario."})
	}
	return items, roundCapacityValue(peakTPS)
}

func dataPoolStrategyVUsers(group TestGroup) int {
	vusers := group.VUsers
	for _, stage := range group.Stages {
		if stage.TargetVUsers > vusers {
			vusers = stage.TargetVUsers
		}
	}
	return vusers
}

func validateLoadModelDataPoolBindings(poolStore dataPoolPersistence, bindings []storage.ScenarioDataPoolBinding) ([]dsl.DataSource, []LoadModelValidationItem, map[string]int) {
	if len(bindings) == 0 {
		return nil, nil, nil
	}
	sources := make([]dsl.DataSource, 0, len(bindings))
	items := make([]LoadModelValidationItem, 0, len(bindings))
	rowCounts := make(map[string]int, len(bindings))
	if poolStore == nil {
		return nil, []LoadModelValidationItem{{Dimension: "data_pool_rows", Status: "blocked", Summary: "Data pool storage is not configured for this controller."}}, nil
	}
	for _, binding := range bindings {
		alias := strings.TrimSpace(binding.Alias)
		if alias == "" {
			alias = strings.TrimSpace(binding.PoolID)
		}
		pool, err := poolStore.GetDataPool(binding.PoolID)
		if err != nil {
			items = append(items, LoadModelValidationItem{Dimension: "data_pool_rows", Group: alias, Status: "blocked", Summary: fmt.Sprintf("Could not load data pool: %v", err)})
			continue
		}
		if archivedErr := errDataPoolArchived(pool); archivedErr != nil {
			items = append(items, LoadModelValidationItem{Dimension: "data_pool_rows", Group: alias, Status: "blocked", Summary: "Data pool is archived; restore it before running this scenario."})
			continue
		}
		rows, err := poolStore.ListDataPoolRows(binding.PoolID)
		if err != nil {
			items = append(items, LoadModelValidationItem{Dimension: "data_pool_rows", Group: alias, Status: "blocked", Summary: fmt.Sprintf("Could not load data pool rows: %v", err)})
			continue
		}
		if len(rows) == 0 {
			items = append(items, LoadModelValidationItem{Dimension: "data_pool_rows", Group: alias, Status: "blocked", Summary: "Data pool has no rows available for scenario execution."})
			continue
		}
		activeRows := activeDataPoolRows(rows)
		if len(activeRows) == 0 {
			items = append(items, LoadModelValidationItem{Dimension: "data_pool_rows", Group: alias, Status: "blocked", Summary: "Data pool has no active rows available for scenario execution; inactive rows are excluded from runs."})
			continue
		}
		inlineData, columns, err := inlineCSVFromDataPoolRows(activeRows)
		if err != nil {
			items = append(items, LoadModelValidationItem{Dimension: "data_pool_rows", Group: alias, Status: "blocked", Summary: fmt.Sprintf("Could not prepare data pool rows: %v", err), AvailableRows: len(activeRows)})
			continue
		}
		strategy := binding.SelectionStrategy
		if strategy == "" {
			strategy = "sequential"
		}
		sources = append(sources, dsl.DataSource{Name: alias, Type: "csv", InlineData: inlineData, Strategy: strategy, FirstRowHeader: true, Columns: columns})
		rowCounts[alias] = len(activeRows)
		items = append(items, LoadModelValidationItem{Dimension: "data_pool_rows", Group: alias, Status: "ready", Summary: fmt.Sprintf("Data pool exposes %d active row%s for load-model validation.", len(activeRows), pluralSuffix(len(activeRows))), AvailableRows: len(activeRows)})
	}
	return sources, items, rowCounts
}

// dataPoolStrategyItems surfaces selection-strategy shortfalls the engine
// degrades silently today: unique assigns duplicate rows once VUsers exceed the
// pool, and pop stops yielding rows entirely after exhaustion.
func dataPoolStrategyItems(groupName string, vusers, iterations, globalIterations int, sources []dsl.DataSource, rowCounts map[string]int) []LoadModelValidationItem {
	if len(sources) == 0 || len(rowCounts) == 0 {
		return nil
	}
	items := make([]LoadModelValidationItem, 0)
	for _, source := range sources {
		available, known := rowCounts[source.Name]
		if !known {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(source.Strategy)) {
		case "unique":
			if vusers > available {
				items = append(items, LoadModelValidationItem{
					Dimension:     "data_pool_rows",
					Group:         groupName,
					Status:        "blocked",
					Summary:       fmt.Sprintf("Data pool %q uses unique row selection but provides %d active row%s for %d VUsers; VUsers beyond the row count receive duplicate rows.", source.Name, available, pluralSuffix(available), vusers),
					RequiredRows:  vusers,
					AvailableRows: available,
				})
			}
		case "pop":
			if demand, bounded := boundedDataPoolPopDemand(vusers, iterations, globalIterations); bounded {
				if demand > available {
					items = append(items, LoadModelValidationItem{
						Dimension:     "data_pool_rows",
						Group:         groupName,
						Status:        "needs_review",
						Summary:       fmt.Sprintf("Data pool %q consumes rows once (pop) and the load model draws %d rows against %d active row%s; VUsers stop receiving rows after exhaustion.", source.Name, demand, available, pluralSuffix(available)),
						RequiredRows:  demand,
						AvailableRows: available,
					})
				}
			} else {
				items = append(items, LoadModelValidationItem{
					Dimension:     "data_pool_rows",
					Group:         groupName,
					Status:        "needs_review",
					Summary:       fmt.Sprintf("Data pool %q consumes rows once (pop) under a duration-based load model; the pool exhausts after %d active row%s and VUsers stop receiving rows.", source.Name, available, pluralSuffix(available)),
					AvailableRows: available,
				})
			}
		}
	}
	return items
}

// boundedDataPoolPopDemand returns the conservative maximum number of rows the
// engine can consume when either the per-VUser iteration count or shared global
// iteration goal makes the run finite. Every VUser eagerly loads its first row
// before acquiring a global-iteration reservation; later rows load before
// iterations 2..N. Consequently a global cap is not simply min(VUsers×per-user,
// global): global=1 can still pop one initial row per started VUser.
// Saturation keeps diagnostics correct for hostile/legacy inputs that bypass
// normal DSL limits.
func boundedDataPoolPopDemand(vusers, iterations, globalIterations int) (int, bool) {
	if iterations <= 0 && globalIterations <= 0 {
		return 0, false
	}
	if vusers <= 0 {
		return 0, true
	}
	if globalIterations <= 0 {
		return saturatingDataPoolDemandProduct(vusers, iterations), true
	}
	if iterations <= 0 {
		return saturatingDataPoolDemandAdd(vusers, globalIterations-1), true
	}

	totalIterations := saturatingDataPoolDemandProduct(vusers, iterations)
	if globalIterations < totalIterations {
		totalIterations = globalIterations
	}
	// Concentrating the globally bounded iterations onto as few VUsers as the
	// per-user cap permits maximizes later-iteration row loads.
	activeVUsers := totalIterations / iterations
	if totalIterations%iterations != 0 {
		activeVUsers++
	}
	extraRows := totalIterations - activeVUsers
	return saturatingDataPoolDemandAdd(vusers, extraRows), true
}

func saturatingDataPoolDemandProduct(left, right int) int {
	if left <= 0 || right <= 0 {
		return 0
	}
	if left > math.MaxInt/right {
		return math.MaxInt
	}
	return left * right
}

func saturatingDataPoolDemandAdd(left, right int) int {
	if left <= 0 {
		return max(0, right)
	}
	if right <= 0 {
		return left
	}
	if left > math.MaxInt-right {
		return math.MaxInt
	}
	return left + right
}

func cloneDSLGoalConfig(in *dsl.GoalConfig) *dsl.GoalConfig {
	if in == nil {
		return nil
	}
	out := *in
	out.TransactionRules = append([]dsl.TransactionSLARule(nil), in.TransactionRules...)
	return &out
}

func hasDataSourceFitItem(items []LoadModelValidationItem) bool {
	for _, item := range items {
		if item.Dimension == "data_source_fit" {
			return true
		}
	}
	return false
}

func finalizeLoadModelValidationReport(report *LoadModelValidationReport) {
	counts := map[string]int{"ready": 0, "needs_review": 0, "blocked": 0}
	for _, item := range report.Items {
		status := strings.TrimSpace(item.Status)
		if status == "" {
			status = "needs_review"
		}
		counts[status]++
	}
	report.StatusCounts = counts
	report.Status = "ready"
	if counts["blocked"] > 0 {
		report.Status = "blocked"
	} else if counts["needs_review"] > 0 {
		report.Status = "needs_review"
	}
	sort.SliceStable(report.Items, func(i, j int) bool {
		if report.Items[i].Dimension == report.Items[j].Dimension {
			return report.Items[i].Group < report.Items[j].Group
		}
		return report.Items[i].Dimension < report.Items[j].Dimension
	})
}
