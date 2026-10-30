package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type salesforceHARAugmentation = importreview.SalesforceHARAugmentation

func (s *Server) handleImportSalesforce(w http.ResponseWriter, r *http.Request) {
	// This route converts one capture; a comparison HAR is diffed only by
	// /scripts/import/har. One sent here is drained without being kept, and
	// the import says so instead of reporting a pair that was never compared.
	importReq, comparisonIgnored, ok := readSalesforceImportRequest(w, r)
	if !ok {
		return
	}
	runHARImportWithinWindow(w, r, func(w http.ResponseWriter, r *http.Request) {
		s.importSalesforce(w, r, importReq, comparisonIgnored)
	})
}

// importSalesforce converts an uploaded Salesforce HAR and writes the import
// response. It runs under runHARImportWithinWindow.
func (s *Server) importSalesforce(w http.ResponseWriter, r *http.Request, importReq *harImportRequest, comparisonIgnored bool) {
	profile, err := s.projectRecordingProfile(normalizeProjectID(importReq.ProjectID), importReq.RecordingProfileID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	importReq.Opts.Profile = profile
	importReq.Opts.RecordingProfileID = strings.TrimSpace(importReq.RecordingProfileID)
	importReq.Opts.EntryPoint = sample.EntryPointImportSalesforce

	result, err := har.Convert(importReq.Data, importReq.Opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	importReq.noteSalvage(result)
	protectedRequirements := result.ProtectedRequirements.Take()
	defer clearCredentialRequirementValues(protectedRequirements)
	dropUnperformedHARPairMode(result)
	if comparisonIgnored {
		result.Warnings = append(result.Warnings, "The comparison HAR was not used: the Salesforce import converts one recording. Import the pair as a HAR to label correlations dynamic or static.")
	}
	if !harImportStageOK(w, r) {
		return
	}
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}

	salesforceNative := salesforceNativeRequested(r)
	agentforceTransform := agentforceTransformRequested(r)
	disableSalesforce := false
	if result.EffectiveSettings != nil {
		salesforceNative = result.EffectiveSettings.SalesforceNative
		agentforceTransform = result.EffectiveSettings.AgentforceTransform
		disableSalesforce = result.EffectiveSettings.DisableSalesforce
	}
	salesforceAugmentation := salesforceHARAugmentation{
		Script:         result.Script,
		Correlations:   any(result.Correlations),
		ReviewSections: harImportReviewSections(result),
		Summary:        harImportSummary(result),
		Source:         "salesforce_har",
	}
	if !harImportStageOK(w, r) {
		return
	}
	if !disableSalesforce {
		salesforceAugmentation = importreview.AugmentHARImportWithSalesforceProfile(result, importReq.Data, importReq.ProjectID, importReq.Opts.ScriptName, "salesforce_har", salesforceNative, agentforceTransform, false, profile)
	}

	preApplied := sampleCorrelationsToPreApplied(result.Correlations)
	if salesforceAugmentation.Script != result.Script {
		preApplied = nil
	}

	var preparedCredentialReview []importreview.SecretBindingRequirement
	var credentialReviewPrepared bool
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
		BeforeFinalize: func(s *dsl.Script, warnings *[]string, unsupported *diag.Diagnostics) error {
			applyClientGeneratedUUIDs(s, importReq.Data, result)
			applyClientTimestamps(s, importReq.Data, result)
			*warnings = result.Warnings
			*unsupported = result.Unsupported
			hasSelectedIDs := len(paramize.SelectedIDsForScript(importReq.Opts.ParameterizationSelections, 0)) > 0
			if result.EffectiveSettings != nil && result.EffectiveSettings.Parameterize && !hasSelectedIDs {
				result.Warnings = append(result.Warnings, importreview.ApplyAutoParameterizationToScript(s)...)
				*warnings = result.Warnings
			}
			if hasSelectedIDs {
				if _, err := importreview.ApplyParameterizationSelectionsToScript(s, 0, importReq.Opts.ParameterizationSelections); err != nil {
					return err
				}
			}
			if len(importReq.SecretBindings) > 0 {
				var err error
				_, preparedCredentialReview, credentialReviewPrepared, err = importreview.ApplySecretBindingsToImportDraft(s, 0, importReq.SecretBindings, protectedRequirements)
				if err != nil {
					return err
				}
			}
			sample.AttachEffectiveSettings(s, result.EffectiveSettings)
			return nil
		},
	}
	augmented, err := draft.AugmentWithAgentforceTransformChecked(agentforceTransform)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result.Warnings = augmented.Warnings
	if !harImportStageOK(w, r) {
		return
	}

	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                   result,
		Script:                   salesforceAugmentation.Script,
		Warnings:                 result.Warnings,
		Stats:                    result.Stats,
		Correlations:             salesforceAugmentation.Correlations,
		Diagnostics:              result.Unsupported,
		UnsupportedElements:      result.Unsupported.Strings(),
		ReviewSections:           augmented.Sections,
		SourceReview:             result.SourceReview,
		Summary:                  salesforceAugmentation.Summary,
		Parameterization:         buildImportParameterization(salesforceAugmentation.Script, preApplied),
		Preflight:                augmented.Preflight,
		EffectiveSettings:        result.EffectiveSettings,
		ProtectedRequirements:    protectedRequirements,
		PreparedCredentialReview: preparedCredentialReview,
		CredentialReviewPrepared: credentialReviewPrepared,
	})
}

var augmentHARImportWithSalesforce = importreview.AugmentHARImportWithSalesforce

var salesforceReviewForResponse = importreview.SalesforceReviewForResponse
