package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"net/http"

	"strings"
)

// sourceSecretValuesForImportResponse maps disclosure-safe requirement IDs to
// their original source values without ever serializing those values. Script
// ordering mirrors secretBindingRequirementsForImportScripts.
func sourceSecretValuesForImportResponse(opts scriptImportResponseOptions, requirements []SecretBindingRequirement) map[string]string {
	return sourceSecretValuesForImportResponseWithProjection(opts, requirements, nil)
}

func sourceSecretValuesForImportResponseWithProjection(opts scriptImportResponseOptions, requirements []SecretBindingRequirement, projectedValue any) map[string]string {
	rawValue := importResponseRawScriptValue(opts.Scripts)
	if !hasScriptJSONNode(rawValue) {
		rawValue = importResponseRawScriptValue(opts.Script)
	}
	if projectedValue == nil {
		projectedValue = maskScriptResponseValue(rawValue)
	}
	return sourceSecretValuesForImportNodes(rawValue, projectedValue, requirements)
}

var sourceSecretValuesForImportNodes = importreview.SourceSecretValuesForImportNodes

func sourceSecretProjectID(opts scriptImportResponseOptions) string {
	for _, candidate := range []any{opts.Script, opts.Scripts} {
		raw := importResponseRawScriptValue(candidate)
		nodes := make([]map[string]any, 0, 1)
		collectScriptJSONNodes(raw, &nodes)
		for _, node := range nodes {
			if projectID, _ := node["project_id"].(string); strings.TrimSpace(projectID) != "" {
				return normalizeProjectID(projectID)
			}
		}
	}
	return ""
}

func sourceSecretProjectIDForRequest(r *http.Request, opts scriptImportResponseOptions) (string, bool) {
	projectID := sourceSecretProjectID(opts)
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped {
		if projectID != "" && normalizeProjectID(projectID) != authorizedProjectID {
			return authorizedProjectID, false
		}
		return authorizedProjectID, true
	}
	if projectID == "" && r != nil {
		projectID = strings.TrimSpace(r.URL.Query().Get("project_id"))
	}
	return normalizeProjectID(projectID), true
}

var importResponseRawScriptValue = importreview.ImportResponseRawScriptValue

var jsonPointerString = importreview.JsonPointerString

var decimalTokenIndex = importreview.DecimalTokenIndex

const maxProjectedRedactionAlignmentWork = importreview.MaxProjectedRedactionAlignmentWork

// projectedRedactionCandidateWork charges each candidate literal position for
// the alignment state it allocates, so input matching a short literal
// everywhere exhausts the budget long before it holds a state per byte.
const projectedRedactionCandidateWork = importreview.ProjectedRedactionCandidateWork

type projectedRedactionAlignment = importreview.ProjectedRedactionAlignment

// extractProjectedRedactions aligns the fixed text surrounding projected
// sentinels with the raw scalar and returns each removed source segment. It
// succeeds only when exactly one segmentation exists. Picking the first matching
// delimiter can silently bind a credential to the wrong logical requirement
// when credential material itself contains that delimiter.
var extractProjectedRedactions = importreview.ExtractProjectedRedactions

var extractLinearlyProjectedRedactions = importreview.ExtractLinearlyProjectedRedactions

var extractStructurallyProjectedJSONRedactions = importreview.ExtractStructurallyProjectedJSONRedactions

var decodeStructuredJSONValue = importreview.DecodeStructuredJSONValue

var collectStructurallyProjectedJSONRedactions = importreview.CollectStructurallyProjectedJSONRedactions
