package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// SharedAssetUsageResponse says what uses each of a project's shared assets,
// keyed by the asset's id, so Shared assets can show every row's users in one
// read. Only the kinds asked for (include) are present. Secrets keep their own
// usage routes, which page and carry a revision.
type SharedAssetUsageResponse struct {
	ProjectID string `json:"project_id"`
	// DataPools lists the scenarios that bind each pool; a bound pool cannot be deleted.
	DataPools *map[string][]SharedAssetScenarioUse `json:"data_pools,omitempty"`
	// LiveTables lists the scenarios that bind each project live table.
	LiveTables *map[string][]SharedAssetScenarioUse `json:"live_tables,omitempty"`
	// ObjectRepositories lists the scripts whose actions point at each
	// repository; a referenced repository cannot be deleted.
	ObjectRepositories *map[string][]SharedAssetScriptUse `json:"object_repositories,omitempty"`
	// CorrelationRulesets says where each rule set was learned and which
	// scripts it was applied to. Applying copies the rules into the script, so
	// those scripts keep them whatever happens to the rule set.
	CorrelationRulesets *map[string]SharedAssetRulesetUse `json:"correlation_rulesets,omitempty"`
}

// SharedAssetScenarioUse is one scenario binding of a pool or live table.
type SharedAssetScenarioUse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Alias    string `json:"alias"`
	Archived bool   `json:"archived,omitempty"`
}

// SharedAssetScriptUse is one script that uses an asset.
type SharedAssetScriptUse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived,omitempty"`
}

// SharedAssetRulesetUse is a correlation rule set's provenance and reach.
type SharedAssetRulesetUse struct {
	LearnedFrom *SharedAssetScriptUse `json:"learned_from,omitempty"`
	// AppliedTo is newest first, from the governance audit log's most recent
	// sharedAssetUsageAuditLimit applications in the project.
	AppliedTo []SharedAssetScriptUse `json:"applied_to"`
}

type sharedAssetUsageStore interface {
	ListProjectSharedAssetBindings(projectID string) ([]storage.SharedAssetScenarioBinding, error)
	ListProjectSharedAssetScripts(projectID string, references bool) ([]storage.SharedAssetScript, error)
}

// sharedAssetUsageKinds are the include values, in response order.
var sharedAssetUsageKinds = []string{"data_pools", "live_tables", "object_repositories", "correlation_rulesets"}

// sharedAssetUsageInclude reads ?include=a,b; none means every kind.
func sharedAssetUsageInclude(raw string) (map[string]bool, string) {
	include := map[string]bool{}
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !slices.Contains(sharedAssetUsageKinds, value) {
			return nil, "include must name " + strings.Join(sharedAssetUsageKinds, ", ")
		}
		include[value] = true
	}
	if len(include) == 0 {
		for _, kind := range sharedAssetUsageKinds {
			include[kind] = true
		}
	}
	return include, ""
}

const (
	// sharedAssetUsageRulesetPages bounds the rule sets read, 100 per page.
	sharedAssetUsageRulesetPages = 50
	// sharedAssetUsageAuditLimit bounds the rule set applications read.
	sharedAssetUsageAuditLimit = 1000
)

func (s *Server) handleSharedAssetUsage(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage is not configured")
		return
	}
	store, ok := s.store.(sharedAssetUsageStore)
	if !ok {
		writeError(w, http.StatusNotImplemented, "shared asset usage is not supported by this storage backend")
		return
	}
	kinds, problem := sharedAssetUsageInclude(r.URL.Query().Get("include"))
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	projectID := strings.TrimSpace(r.PathValue("id"))
	response := SharedAssetUsageResponse{ProjectID: projectID}

	if kinds["data_pools"] || kinds["live_tables"] {
		bindings, err := store.ListProjectSharedAssetBindings(projectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list data bindings: "+err.Error())
			return
		}
		pools, tables := map[string][]SharedAssetScenarioUse{}, map[string][]SharedAssetScenarioUse{}
		for _, binding := range bindings {
			use := SharedAssetScenarioUse{ID: binding.ScenarioID, Name: binding.ScenarioName, Alias: binding.Alias, Archived: binding.Archived}
			if binding.PoolID != "" {
				pools[binding.PoolID] = append(pools[binding.PoolID], use)
			}
			if binding.ProjectTableID != "" {
				tables[binding.ProjectTableID] = append(tables[binding.ProjectTableID], use)
			}
		}
		if kinds["data_pools"] {
			response.DataPools = &pools
		}
		if kinds["live_tables"] {
			response.LiveTables = &tables
		}
	}

	if kinds["object_repositories"] || kinds["correlation_rulesets"] {
		// Only repositories need each script's actions decoded; rule sets need names.
		scripts, err := store.ListProjectSharedAssetScripts(projectID, kinds["object_repositories"])
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list scripts: "+err.Error())
			return
		}
		scriptByID := make(map[string]SharedAssetScriptUse, len(scripts))
		repositories := map[string][]SharedAssetScriptUse{}
		for _, script := range scripts {
			use := SharedAssetScriptUse{ID: script.ID, Name: script.Name, Archived: script.Archived}
			scriptByID[script.ID] = use
			for _, repositoryID := range script.ObjectRepositoryIDs {
				repositories[repositoryID] = append(repositories[repositoryID], use)
			}
		}
		if kinds["object_repositories"] {
			response.ObjectRepositories = &repositories
		}
		if kinds["correlation_rulesets"] {
			rulesets, err := s.sharedAssetRulesetUsage(projectID, scriptByID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			response.CorrelationRulesets = &rulesets
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// sharedAssetRulesetUsage says where each of the project's rule sets was
// learned and, from the audit log, which scripts it was applied to.
func (s *Server) sharedAssetRulesetUsage(projectID string, scriptByID map[string]SharedAssetScriptUse) (map[string]SharedAssetRulesetUse, error) {
	usage := map[string]SharedAssetRulesetUse{}
	rulesets := s.correlationRulesetDomainService()
	if !rulesets.StorageConfigured() {
		return usage, nil
	}
	for page := 0; page < sharedAssetUsageRulesetPages; page++ {
		listed, err := rulesets.ListRulesets(projectID, maxCorrelationRulesetListLimit, page*maxCorrelationRulesetListLimit)
		if err != nil {
			return nil, fmt.Errorf("failed to list correlation rulesets: %w", err)
		}
		for _, ruleset := range listed {
			use := SharedAssetRulesetUse{AppliedTo: []SharedAssetScriptUse{}}
			if scriptID, ok := strings.CutPrefix(ruleset.Source, "script:"); ok {
				if script, found := scriptByID[scriptID]; found {
					use.LearnedFrom = &script
				}
			}
			usage[ruleset.ID] = use
		}
		if len(listed) < maxCorrelationRulesetListLimit {
			break
		}
	}
	if len(usage) == 0 {
		return usage, nil
	}
	events, err := s.store.ListAuditEvents(storage.AuditEventFilter{Action: "correlation_ruleset.apply", ProjectID: projectID, Limit: sharedAssetUsageAuditLimit})
	if err != nil {
		return nil, fmt.Errorf("failed to list correlation ruleset applications: %w", err)
	}
	applied := map[string]map[string]bool{}
	for _, event := range events {
		rulesetID, _ := event.Details["ruleset_id"].(string)
		use, known := usage[rulesetID]
		script, exists := scriptByID[event.ResourceID]
		if event.Status != "success" || !known || !exists {
			continue
		}
		if applied[rulesetID] == nil {
			applied[rulesetID] = map[string]bool{}
		}
		if applied[rulesetID][script.ID] {
			continue
		}
		applied[rulesetID][script.ID] = true
		use.AppliedTo = append(use.AppliedTo, script)
		usage[rulesetID] = use
	}
	return usage, nil
}
