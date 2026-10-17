package api

// handlers_import_devweb_k6 — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/k6"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
)

func (s *Server) handleK6SupportMatrix(w http.ResponseWriter, r *http.Request) {
	s.writeImporterSupportMatrix(w, r, "k6")
}

func (s *Server) handleImportDevWeb(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readDevWebImportRequest(w, r)
	if !ok {
		return
	}

	var (
		result *devweb.ConvertResult
		err    error
	)
	if importReq.IsZip {
		result, err = devweb.ConvertZip(importReq.Data, importReq.Opts)
	} else {
		result, err = devweb.Convert(devweb.ImportRequest{
			Name:      importReq.Name,
			ProjectID: importReq.ProjectID,
			Content:   string(importReq.Data),
			Options:   importReq.Opts,
		})
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	protocolCompat := buildDevWebProtocolCompatReport(result)
	persistProtocolCompatReport(result.Script, "devweb", protocolCompat)
	draft := importDraft{
		Source:           "devweb",
		SourceScriptName: importReq.Name,
		ProjectID:        importReq.ProjectID,
		Script:           result.Script,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Unsupported,
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
	}
	augmented := draft.Augment(r)
	result.Warnings = augmented.Warnings
	protectedRequirements := result.ProtectedRequirements.Take()

	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                result,
		Script:                result.Script,
		Warnings:              result.Warnings,
		Assumptions:           result.Assumptions,
		Stats:                 result.Stats,
		Diagnostics:           result.Unsupported,
		UnsupportedElements:   result.Unsupported.Strings(),
		ReviewSections:        augmented.Sections,
		SourceReview:          result.SourceReview,
		ProtocolCompat:        protocolCompat,
		Parameterization:      buildImportParameterization(result.Script, nil),
		Preflight:             augmented.Preflight,
		ProtectedRequirements: protectedRequirements,
	})
}

func (s *Server) handleImportK6(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readK6ImportRequest(w, r)
	if !ok {
		return
	}
	result, err := k6.Convert(k6.ImportRequest{
		Name:      importReq.Name,
		ProjectID: importReq.ProjectID,
		Content:   string(importReq.Data),
		Options:   importReq.Opts,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}
	draft := importDraft{
		Source:           "k6",
		SourceScriptName: importReq.Name,
		ProjectID:        importReq.ProjectID,
		Script:           result.Script,
		SourceReview:     result.SourceReview,
		Unsupported:      result.Unsupported,
		Warnings:         result.Warnings,
		Accounting:       result.Accounting(),
	}
	augmented := draft.Augment(r)
	result.Warnings = augmented.Warnings
	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:                 result,
		Script:                 result.Script,
		Warnings:               result.Warnings,
		Stats:                  result.Stats,
		Assumptions:            result.Assumptions,
		Diagnostics:            result.Unsupported,
		UnsupportedElements:    result.Unsupported.Strings(),
		ReviewSections:         augmented.Sections,
		SourceReview:           result.SourceReview,
		CredentialRequirements: result.SecretRequirements,
		Parameterization:       buildImportParameterization(result.Script, nil),
		Preflight:              augmented.Preflight,
		Summary: map[string]any{
			"import_mode":    "constrained_http_subset",
			"execution_mode": "apexload_http",
		},
	})
}

func (s *Server) handleExportK6(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req k6.ExportRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	result, err := k6.Export(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "export failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func readK6ImportRequest(w http.ResponseWriter, r *http.Request) (*k6ImportRequest, bool) {
	upload, ok := decodeImportUpload(w, r, k6ImportMaxBodyBytes)
	if !ok {
		return nil, false
	}
	if upload.Transport == importTransportJSON {
		var req k6.ImportRequest
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		return &k6ImportRequest{
			Data:      []byte(req.Content),
			ProjectID: upload.jsonProjectID(req.ProjectID),
			Name:      req.Name,
			Opts:      req.Options,
		}, true
	}
	return &k6ImportRequest{
		Data:      upload.Data,
		ProjectID: upload.ProjectID,
		Name:      upload.Name,
		Opts: k6.ConvertOptions{
			BaseURL:    upload.Value("base_url"),
			ScriptName: upload.Value("script_name"),
		},
	}, true
}

func readDevWebImportRequest(w http.ResponseWriter, r *http.Request) (*devWebImportRequest, bool) {
	upload, ok := decodeImportUpload(w, r, webAPIImportMaxBodyBytes)
	if !ok {
		return nil, false
	}
	if upload.Transport == importTransportJSON {
		var req devweb.ImportRequest
		if err := json.Unmarshal(upload.Data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		return &devWebImportRequest{
			Data:      []byte(req.Content),
			ProjectID: upload.jsonProjectID(req.ProjectID),
			Name:      req.Name,
			Opts:      req.Options,
		}, true
	}
	// A DevWeb upload may be a single script or a project archive. The archive
	// is recognised by extension, and on a raw body also by content type, since
	// a bare POST need not name the file.
	isZip := strings.EqualFold(archiveingest.LogicalExt(upload.Filename), ".zip")
	if upload.Transport == importTransportRaw {
		isZip = isZip || strings.HasPrefix(r.Header.Get("Content-Type"), "application/zip")
	}
	return &devWebImportRequest{
		Data:      upload.Data,
		ProjectID: upload.ProjectID,
		Name:      upload.Name,
		IsZip:     isZip,
		Opts:      devweb.ConvertOptions{BaseURL: upload.Value("base_url")},
	}, true
}

func buildDevWebProtocolCompatReport(result *devweb.ConvertResult) *protocolCompatReport {
	if result == nil {
		return nil
	}
	unsupported := result.Unsupported.Strings()
	status := protocolPromotionStatus("executable", len(unsupported), len(result.Warnings))
	return &protocolCompatReport{
		SourceFormat:         "DevWeb",
		CanonicalProtocol:    "devweb",
		ImportMode:           "executable",
		MappedSteps:          result.Stats.ConvertedActions,
		ReviewGatedSteps:     len(unsupported),
		UnsupportedFragments: uniqueSortedStrings(append(append([]string{}, result.Stats.UnsupportedCalls...), unsupported...)),
		RuntimeParity:        protocolRuntimeParityFromScript(result.Script),
		DataSources:          result.Stats.DataSources,
		CorrelationCoverage:  protocolCorrelationCoverage(result.Stats.Extractors, result.Stats.HTTPRequests),
		PromotionStatus:      status,
		NextActions:          protocolNextActions(status, "Validate DevWeb replay against the target environment."),
	}
}
