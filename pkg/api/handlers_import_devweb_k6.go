package api

// handlers_import_devweb_k6 — split from handlers_scripts.go (same package, behavior-preserving).

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/k6"
)

func (s *Server) handleK6SupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix": k6.GetSupportMatrix(),
		"notes":          k6.GetSupportMatrixNotes(),
	})
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
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScript(result.Script, "devweb", importReq.Name, result.SourceReview)
	issues := validateScript(result.Script)
	if len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	protocolCompat := buildDevWebProtocolCompatReport(result)
	persistProtocolCompatReport(result.Script, "devweb", protocolCompat)
	preflight := buildAndAttachImportPreflight(result.Script, "devweb", issues, result.Warnings)

	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Assumptions:         result.Assumptions,
		Stats:               result.Stats,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      importDiagnosticReviewSections(result.Unsupported),
		SourceReview:        result.SourceReview,
		ProtocolCompat:      protocolCompat,
		Parameterization:    buildImportParameterization(result.Script, nil),
		Preflight:           preflight,
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
	if result.Script != nil {
		result.Script.ProjectID = normalizeProjectID(importReq.ProjectID)
	}
	attachSourceReviewToScript(result.Script, "k6", importReq.Name, result.SourceReview)
	issues := validateScript(result.Script)
	if len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	preflight := buildAndAttachImportPreflight(result.Script, "k6", issues, result.Warnings)
	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Assumptions:         result.Assumptions,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      importDiagnosticReviewSections(result.Unsupported),
		SourceReview:        result.SourceReview,
		Parameterization:    buildImportParameterization(result.Script, nil),
		Preflight:           preflight,
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
	r.Body = http.MaxBytesReader(w, r.Body, k6ImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var req k6.ImportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		return &k6ImportRequest{Data: []byte(req.Content), ProjectID: jsonImportProjectID(r, req.ProjectID), Name: req.Name, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		data, filename, ok := readImportMultipartFile(w, r, k6ImportMaxBodyBytes)
		if !ok {
			return nil, false
		}
		return &k6ImportRequest{
			Data:      data,
			ProjectID: authorizedImportProjectID(r),
			Name:      strings.TrimSuffix(filename, filepath.Ext(filename)),
			Opts: k6.ConvertOptions{
				BaseURL:    r.FormValue("base_url"),
				ScriptName: r.FormValue("script_name"),
			},
		}, true
	}
	data, ok := readImportBody(w, r)
	if !ok {
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &k6ImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Opts: k6.ConvertOptions{
			BaseURL:    r.URL.Query().Get("base_url"),
			ScriptName: r.URL.Query().Get("script_name"),
		},
	}, true
}

func readDevWebImportRequest(w http.ResponseWriter, r *http.Request) (*devWebImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var req devweb.ImportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		return &devWebImportRequest{
			Data:      []byte(req.Content),
			ProjectID: jsonImportProjectID(r, req.ProjectID),
			Name:      req.Name,
			Opts:      req.Options,
		}, true
	}

	if strings.HasPrefix(contentType, "multipart/form-data") {
		data, filename, ok := readImportMultipartFile(w, r, webAPIImportMaxBodyBytes)
		if !ok {
			return nil, false
		}
		opts := devweb.ConvertOptions{
			BaseURL: r.FormValue("base_url"),
		}
		return &devWebImportRequest{
			Data:      data,
			ProjectID: authorizedImportProjectID(r),
			Name:      strings.TrimSuffix(filename, filepath.Ext(filename)),
			IsZip:     strings.EqualFold(filepath.Ext(filename), ".zip"),
			Opts:      opts,
		}, true
	}

	data, ok := readImportBody(w, r)
	if !ok {
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &devWebImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		IsZip:     strings.EqualFold(filepath.Ext(filename), ".zip") || strings.HasPrefix(contentType, "application/zip"),
		Opts: devweb.ConvertOptions{
			BaseURL: r.URL.Query().Get("base_url"),
		},
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
