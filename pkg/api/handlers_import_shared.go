package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// handlers_import_shared — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"strings"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// requireImportProject answers 404 when an import save names a project that
// does not exist, before anything is reserved or written, so the operator
// learns to pick an existing project instead of meeting a server error.
func (s *Server) requireImportProject(w http.ResponseWriter, projectID string) bool {
	if status, err := s.importProjectStatus(projectID); err != nil {
		writeError(w, status, err.Error())
		return false
	}
	return true
}

// importProjectStatus is requireImportProject's check: 404 with what to do
// when projectID does not exist, 500 when it cannot be read.
func (s *Server) importProjectStatus(projectID string) (int, error) {
	if s == nil || s.store == nil {
		return 0, nil
	}
	projectID = normalizeProjectID(projectID)
	project, err := s.store.GetProject(projectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return http.StatusInternalServerError, fmt.Errorf("failed to load the import project: %w", err)
	}
	if project == nil {
		return http.StatusNotFound, fmt.Errorf("project %q not found; choose an existing project to import into", projectID)
	}
	return 0, nil
}

// bindImportDrafts returns copies of a batch save's scripts bound to
// projectID (400 for one bound elsewhere), after checking that project exists,
// so a missing project is named before any folder is checked or anything is
// reserved.
func (s *Server) bindImportDrafts(w http.ResponseWriter, scripts []dsl.Script, projectID string) ([]dsl.Script, bool) {
	drafts := make([]dsl.Script, len(scripts))
	for index := range scripts {
		drafts[index] = scripts[index]
		if err := bindDraftScriptProject(&drafts[index], projectID); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("script %d: %v", index+1, err))
			return nil, false
		}
	}
	if !s.requireImportProject(w, projectID) {
		return nil, false
	}
	return drafts, true
}

var ensureScriptImportPreserved = importreview.EnsureScriptImportPreserved

var attachImportReplayPreflight = importreview.AttachImportReplayPreflight

func (s *Server) handleAuthoringCapabilities(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	response := authoringCapabilityResponse(projectID)
	// A storage-less development/test server cannot persist restrictions, so it
	// keeps the catalog default of every feature enabled, matching what
	// domainFeatureAllowsRequest and requireDomainFeatureForProject already do.
	// Without this guard the project lookup below nil-dereferenced s.store.
	if projectID != "" && s.store != nil {
		project, err := s.store.GetProject(normalizeProjectID(projectID))
		if err != nil || project == nil {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		policy, err := s.loadDomainFeaturePolicy(project.DomainID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "domain feature policy is unavailable")
			return
		}
		featurePolicy := domainFeatureResponse(policy)
		response.FeaturePolicy = &featurePolicy
		filtered := make([]AuthoringImportSurface, 0, len(response.ImportSurfaces))
		for _, surface := range response.ImportSurfaces {
			enabled := false
			featureID := "script_import." + surface.ID
			for _, feature := range featurePolicy.Features {
				if feature.ID == featureID {
					enabled = feature.EffectiveEnabled
					break
				}
			}
			if enabled {
				filtered = append(filtered, surface)
			}
		}
		response.ImportSurfaces = filtered
	}
	writeJSON(w, http.StatusOK, response)
}

var parseBoolDefault = importreview.ParseBoolDefault

func stringMapAnyValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch value := values[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func truncateReviewText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return strings.TrimSpace(value[:limit-3]) + "..."
}

func stableReviewKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

var stringSliceValue = importreview.StringSliceValue

var uniqueSortedStrings = importreview.UniqueSortedStrings

var stringValue = importreview.StringValue

var attachSourceReviewToScripts = importreview.AttachSourceReviewToScripts

var attachSourceReviewToScript = importreview.AttachSourceReviewToScript

func persistProtocolCompatReport(script *dsl.Script, source string, report *protocolCompatReport) {
	if script == nil || report == nil {
		return
	}
	preserved := ensureScriptImportPreserved(script, source)
	preserved["protocol_compat_report"] = report
}

func protocolPromotionStatus(importMode string, reviewCount int, warningCount int) string {
	switch importMode {
	case string(enterprisearchive.ImportModeExternalRunnerRequired):
		return "external_runner_required"
	case string(enterprisearchive.ImportModeUnsupported):
		return "unsupported"
	case string(enterprisearchive.ImportModeReviewOnly):
		return "review_required"
	}
	if reviewCount > 0 {
		return "review_required"
	}
	if warningCount > 0 {
		return "ready_with_warnings"
	}
	return "ready_for_validation"
}

func protocolNextActions(status string, validationAction string) []string {
	switch status {
	case "external_runner_required":
		return []string{"Preserve source artifact and attach approved external runner evidence before promotion."}
	case "unsupported":
		return []string{"Confirm protocol support strategy or defer this import as unsupported."}
	case "review_required":
		return []string{"Resolve review-gated fragments and unsupported constructs.", validationAction}
	case "ready_with_warnings":
		return []string{"Review converter warnings.", validationAction}
	default:
		return []string{validationAction}
	}
}

func protocolCorrelationCoverage(extractors int, requests int) string {
	if requests <= 0 {
		return "not_applicable"
	}
	if extractors <= 0 {
		return "none_detected"
	}
	return fmt.Sprintf("%d extractor(s) across %d request action(s)", extractors, requests)
}

func protocolRuntimeParityFromScript(script *dsl.Script) map[string]int {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return nil
	}
	switch typed := script.Metadata.Import.Preserved["runtime_parity"].(type) {
	case map[string]string:
		counts := map[string]int{}
		for _, status := range typed {
			counts[status]++
		}
		return counts
	case map[string]any:
		counts := map[string]int{}
		for _, status := range typed {
			if text, ok := status.(string); ok {
				counts[text]++
			}
		}
		return counts
	default:
		return nil
	}
}

func protocolDataSourceCount(script *dsl.Script) int {
	if script == nil {
		return 0
	}
	return len(script.DataSources)
}

func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		" ", "_",
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	result := replacer.Replace(name)
	if result == "" {
		result = "script"
	}
	return result
}
