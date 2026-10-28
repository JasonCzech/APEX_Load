package recordbridge

import (
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"net/http"
	"strings"
)

// ConversionError preserves the response status and explanation of preparation failures.
type ConversionError struct {
	Status  int
	Message string
}

func (e *ConversionError) Error() string { return e.Message }

// PreparedBrowserRecording contains only conversion outputs. Persistence and
// receipt ownership remain with the controller or standalone library adapter.
type PreparedBrowserRecording struct {
	Result             *recbrowser.ConvertResult
	Scripts            []*dsl.Script
	HTTPScript         *dsl.Script
	PreparedHTTPScript *dsl.Script
	HTTPBaseline       []correlate.Observation
	HTTPWarnings       []string
	Warnings           []string
	TransactionScripts []*dsl.Script
}

func (s *Service) PrepareBrowserRecordingConversion(artifact *recbrowser.Session, info *BrowserRecordingSession, req BrowserRecordingConvertRequest, durable bool) (*PreparedBrowserRecording, *ConversionError) {
	if _, err := sample.ValidateConversionPolicy(req.ConversionPolicy); err != nil {
		return nil, &ConversionError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	if _, err := sample.ValidateCorrelationMode(req.CorrelationMode); err != nil {
		return nil, &ConversionError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	if _, err := sample.ValidateResourceMode(req.ResourceMode); err != nil {
		return nil, &ConversionError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	result := s.ConvertBrowserRecording(artifact, info, req)

	// Store every generated script (one for the single-script path, several
	// when splitting at transactions).
	preparedScripts := make([]*dsl.Script, 0, len(result.Scripts))
	for scriptIndex, sc := range result.Scripts {
		if sc == nil {
			continue
		}
		parameterizedScript, err := ApplyBrowserRecordingParameterizationSelections(
			sc,
			scriptIndex,
			req.ParameterizationSelections,
		)
		if err != nil {
			return nil, &ConversionError{Status: http.StatusConflict, Message: "browser recording parameterization selections are stale or invalid: " + err.Error()}
		}
		boundScript, err := ApplyBrowserRecordingSecretBindings(
			parameterizedScript,
			scriptIndex,
			req.SecretBindings,
		)
		if err != nil {
			return nil, &ConversionError{Status: http.StatusConflict, Message: "browser recording secret bindings are stale or invalid: " + err.Error()}
		}
		result.Scripts[scriptIndex] = boundScript
		if scriptIndex == 0 {
			result.Script = boundScript
		}
		importedScript, err := importreview.PreparePersistentImportedLibraryScript(
			boundScript,
			nil,
			"browser_recorder",
		)
		if err != nil {
			return nil, &ConversionError{Status: http.StatusUnprocessableEntity, Message: "failed to prepare recorded credentials: " + err.Error()}
		}
		if err := importreview.ValidateConvertedScriptForCommit(importedScript); err != nil {
			return nil, &ConversionError{Status: http.StatusUnprocessableEntity, Message: "browser recording did not produce an executable script: " + err.Error()}
		}
		if strings.TrimSpace(importedScript.Version) == "" {
			importedScript.Version = "1"
		}
		preparedScripts = append(preparedScripts, importedScript)
	}
	if len(preparedScripts) == 0 {
		return nil, &ConversionError{Status: http.StatusUnprocessableEntity, Message: "conversion produced no scripts"}
	}

	warnings := append([]string(nil), result.Warnings...)
	var httpScript *dsl.Script
	var preparedHTTPScript *dsl.Script
	var httpCorrelationBaseline []correlate.Observation
	var httpWarnings []string
	if req.AlsoCreateHTTPScript {
		var err error
		// The convert route persists the drafts and answers with StoredScripts,
		// which carry no review sections; the preview route above is where the
		// generated HTTP draft's sections are shown.
		var generated BrowserHTTPScript
		generated, err = s.CreateHTTPScriptFromHARAtLevel(artifact, info, req.Name, req.HTTPScriptLevel, req.HTTPScriptOptionsForIndex(len(result.Scripts)))
		defer importreview.ClearCredentialRequirementValues(generated.ProtectedRequirements)
		httpScript, httpWarnings = generated.Script, generated.Warnings
		if err != nil {
			warnings = append(warnings, "HTTP script generation skipped: "+err.Error())
		} else if httpScript == nil {
			warnings = append(warnings, "HTTP script generation skipped: conversion produced no script")
		} else {
			if len(generated.ProtectedRequirements) > 0 {
				reqs := importreview.MergeReferencedImportCredentialRequirements([]*dsl.Script{httpScript}, generated.ProtectedRequirements)
				if len(reqs) > 0 {
					_, _ = importreview.AttachImportCredentialReview([]*dsl.Script{httpScript}, reqs)
					importreview.ClearCredentialRequirementValues(reqs)
				}
			}
			httpScriptIndex := len(result.Scripts)
			httpScript, err = ApplyBrowserRecordingParameterizationSelections(
				httpScript,
				httpScriptIndex,
				req.ParameterizationSelections,
			)
			if err != nil {
				return nil, &ConversionError{Status: http.StatusConflict, Message: "browser recording HTTP parameterization selections are stale or invalid: " + err.Error()}
			}
			httpScript, err = ApplyBrowserRecordingSecretBindings(
				httpScript,
				httpScriptIndex,
				req.SecretBindings,
			)
			if err != nil {
				return nil, &ConversionError{Status: http.StatusConflict, Message: "browser recording HTTP secret bindings are stale or invalid: " + err.Error()}
			}
			importedHTTPScript, prepareErr := importreview.PreparePersistentImportedLibraryScript(
				httpScript,
				nil,
				"browser_recording_har",
			)
			if prepareErr != nil {
				warnings = append(warnings, "HTTP script persistence skipped: failed to prepare recorded credentials: "+prepareErr.Error())
			} else if validationErr := importreview.ValidateConvertedScriptForCommit(importedHTTPScript); validationErr != nil {
				warnings = append(warnings, "HTTP script persistence skipped: "+validationErr.Error())
			} else {
				if strings.TrimSpace(importedHTTPScript.Version) == "" {
					importedHTTPScript.Version = "1"
				}
				preparedHTTPScript = importedHTTPScript
				// The conversion's own baseline is keyed by each request's action
				// index, which transaction markers and background conversations
				// move away from its position among the recorded requests.
				httpCorrelationBaseline = generated.Baseline
			}
		}
	}
	if err := ValidateBrowserRecordingHARBaselinePreparation(
		durable,
		preparedHTTPScript != nil,
		httpCorrelationBaseline,
	); err != nil {
		return nil, &ConversionError{Status: http.StatusUnprocessableEntity, Message: err.Error()}
	}
	availableScriptCount := len(result.Scripts)
	if httpScript != nil {
		availableScriptCount++
	}
	if err := ValidateBrowserRecordingSecretBindings(req.SecretBindings, availableScriptCount); err != nil {
		return nil, &ConversionError{Status: http.StatusConflict, Message: "browser recording secret bindings are stale or invalid: " + err.Error()}
	}
	if err := ValidateBrowserRecordingParameterizationSelections(
		req.ParameterizationSelections,
		availableScriptCount,
	); err != nil {
		return nil, &ConversionError{Status: http.StatusConflict, Message: "browser recording parameterization selections are stale or invalid: " + err.Error()}
	}

	transactionScripts := append([]*dsl.Script(nil), preparedScripts...)
	if preparedHTTPScript != nil {
		transactionScripts = append(transactionScripts, preparedHTTPScript)
	}
	if req.ExpectedScriptCount > 0 &&
		len(transactionScripts) != req.ExpectedScriptCount {
		return nil, &ConversionError{Status: http.StatusConflict, Message: fmt.Sprintf(
			"browser recording conversion changed from %d reviewed script(s) to %d persistable script(s); refresh the preview",
			req.ExpectedScriptCount,
			len(transactionScripts),
		)}
	}
	return &PreparedBrowserRecording{Result: result, Scripts: preparedScripts, HTTPScript: httpScript, PreparedHTTPScript: preparedHTTPScript, HTTPBaseline: httpCorrelationBaseline, HTTPWarnings: httpWarnings, Warnings: warnings, TransactionScripts: transactionScripts}, nil
}

func ValidateBrowserRecordingHARBaselinePreparation(
	durable bool,
	hasHTTPCompanion bool,
	observations []correlate.Observation,
) error {
	if !durable || !hasHTTPCompanion {
		return nil
	}
	if len(observations) == 0 {
		return errors.New("HTTP correlation baseline could not be prepared because the recorded HAR contains no persistable HTTP exchanges")
	}
	return nil
}
