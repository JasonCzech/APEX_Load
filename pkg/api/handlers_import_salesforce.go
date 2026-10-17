package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"net/http"

	"github.com/jasonczech/apexload/pkg/convert/har"
)

type salesforceHARAugmentation = importreview.SalesforceHARAugmentation

func (s *Server) handleImportSalesforce(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readHARImportRequest(w, r)
	if !ok {
		return
	}

	result, err := har.Convert(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	importReq.noteSalvage(result)
	protectedRequirements := result.ProtectedRequirements.Take()
	defer clearCredentialRequirementValues(protectedRequirements)
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	agentforceTransform := agentforceTransformRequested(r)
	salesforceAugmentation := augmentHARImportWithSalesforce(result, importReq.Data, importReq.ProjectID, importReq.Opts.ScriptName, "salesforce_har", true, agentforceTransform, false)
	applyClientGeneratedUUIDs(salesforceAugmentation.Script, importReq.Data, result)
	applyClientTimestamps(salesforceAugmentation.Script, importReq.Data, result)

	draft := importDraft{
		Source:           salesforceAugmentation.Source,
		SourceScriptName: importReq.Opts.ScriptName,
		ProjectID:        importReq.ProjectID,
		Script:           salesforceAugmentation.Script,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Unsupported,
		Sections:         salesforceAugmentation.ReviewSections,
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
	}
	augmented := draft.AugmentWithAgentforceTransform(agentforceTransform)
	result.Warnings = augmented.Warnings

	preApplied := sampleCorrelationsToPreApplied(result.Correlations)
	if salesforceAugmentation.Script != result.Script {
		preApplied = nil
	}

	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                result,
		Script:                salesforceAugmentation.Script,
		Warnings:              result.Warnings,
		Stats:                 result.Stats,
		Correlations:          salesforceAugmentation.Correlations,
		Diagnostics:           result.Unsupported,
		UnsupportedElements:   result.Unsupported.Strings(),
		ReviewSections:        augmented.Sections,
		SourceReview:          result.SourceReview,
		Summary:               salesforceAugmentation.Summary,
		Parameterization:      buildImportParameterization(salesforceAugmentation.Script, preApplied),
		Preflight:             augmented.Preflight,
		ProtectedRequirements: protectedRequirements,
	})
}

var augmentHARImportWithSalesforce = importreview.AugmentHARImportWithSalesforce

var salesforceReviewForResponse = importreview.SalesforceReviewForResponse
