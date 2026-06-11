package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/asyncapi"
	"github.com/jasonczech/apexload/pkg/convert/devweb"
	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/wsdl"
)

type openAPIImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	Opts      openapi.ConvertOptions
}

type wsdlImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	Opts      wsdl.ConvertOptions
}

type postmanImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	Opts      postman.ConvertOptions
}

type asyncAPIImportRequest struct {
	Data      []byte
	ProjectID string
	Name      string
	Opts      asyncapi.ConvertOptions
}

func (s *Server) handleDevWebSupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix":  devweb.GetSupportMatrix(),
		"notes":           devweb.GetSupportMatrixNotes(),
		"tested_examples": devweb.GetSupportMatrixExamples(),
	})
}

func (s *Server) handleImportOpenAPI(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readOpenAPIImportRequest(w, r)
	if !ok {
		return
	}
	result, err := openapi.Convert(openapi.ImportRequest{
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
	attachSourceReviewToScript(result.Script, "openapi", importReq.Name, result.SourceReview)
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Summary:             result.Summary,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      appendImportDiagnosticReviewSections(buildOpenAPIReviewSections(result), result.Unsupported),
		SourceReview:        result.SourceReview,
		Parameterization:    buildImportParameterization(result.Script, nil),
	}))
}

func (s *Server) handleImportWSDL(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readWSDLImportRequest(w, r)
	if !ok {
		return
	}
	result, err := wsdl.Convert(wsdl.ImportRequest{
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
	attachSourceReviewToScript(result.Script, "wsdl", importReq.Name, result.SourceReview)
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Summary:             result.Summary,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      appendImportDiagnosticReviewSections(buildWSDLReviewSections(result), result.Unsupported),
		SourceReview:        result.SourceReview,
		Parameterization:    buildImportParameterization(result.Script, nil),
	}))
}

func (s *Server) handleImportPostman(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readPostmanImportRequest(w, r)
	if !ok {
		return
	}
	result, err := postman.Convert(postman.ImportRequest{
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
	attachSourceReviewToScript(result.Script, "postman", importReq.Name, result.SourceReview)
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Summary:             result.Summary,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      appendImportDiagnosticReviewSections(buildPostmanReviewSections(result), result.Unsupported),
		SourceReview:        result.SourceReview,
		Parameterization:    buildImportParameterization(result.Script, nil),
	}))
}

func (s *Server) handleAsyncAPISupportMatrix(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"support_matrix": asyncapi.GetSupportMatrix(),
		"notes":          asyncapi.GetSupportMatrixNotes(),
	})
}

func (s *Server) handleImportAsyncAPI(w http.ResponseWriter, r *http.Request) {
	importReq, ok := readAsyncAPIImportRequest(w, r)
	if !ok {
		return
	}
	result, err := asyncapi.Convert(asyncapi.ImportRequest{
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
	attachSourceReviewToScript(result.Script, "asyncapi", importReq.Name, result.SourceReview)
	if issues := validateScript(result.Script); len(issues) > 0 {
		result.Warnings = append(result.Warnings, issues...)
	}
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Result:              result,
		Script:              result.Script,
		Warnings:            result.Warnings,
		Stats:               result.Stats,
		Summary:             result.Summary,
		UnsupportedElements: result.Unsupported.Strings(),
		ReviewSections:      appendImportDiagnosticReviewSections(buildAsyncAPIReviewSections(result), result.Unsupported),
		SourceReview:        result.SourceReview,
		Parameterization:    buildImportParameterization(result.Script, nil),
	}))
}

func buildAsyncAPIReviewSections(result *asyncapi.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return nil
	}
	sections := []scriptImportReviewSection{
		{
			Key:      "asyncapi_operations",
			Title:    "AsyncAPI Operations",
			Severity: "info",
			Summary:  "Channels converted into Kafka produce/consume/request-reply actions.",
			Items:    result.Summary.OperationNames,
		},
	}
	if len(result.Summary.Topics) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "asyncapi_topics",
			Title:    "Kafka Topics",
			Severity: "info",
			Summary:  "Topics resolved from channel kafka bindings, addresses, and names.",
			Items:    result.Summary.Topics,
		})
	}
	if result.Summary.SecurityProtocol != "" {
		sections = append(sections, scriptImportReviewSection{
			Key:      "asyncapi_security",
			Title:    "Security Review",
			Severity: "warning",
			Summary:  "Confirm the Kafka security protocol, SASL mechanism, and credentials before running against a real cluster.",
			Items:    []string{result.Summary.SecurityProtocol},
		})
	}
	if len(result.Summary.DataColumns) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "asyncapi_data_fit",
			Title:    "Payload Data Fit",
			Severity: "info",
			Summary:  "Generated CSV seed columns for scalar payload fields.",
			Items:    result.Summary.DataColumns,
		})
	}
	return sections
}

func readAsyncAPIImportRequest(w http.ResponseWriter, r *http.Request) (*asyncAPIImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return nil, false
		}
		var req asyncapi.ImportRequest
		if err := json.Unmarshal(data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		if strings.TrimSpace(req.Content) == "" && strings.Contains(string(data), `"asyncapi"`) {
			return &asyncAPIImportRequest{
				Data:      data,
				ProjectID: r.URL.Query().Get("project_id"),
				Name:      r.URL.Query().Get("name"),
				Opts:      asyncapi.ConvertOptions{ScriptName: r.URL.Query().Get("script_name")},
			}, true
		}
		return &asyncAPIImportRequest{Data: []byte(req.Content), ProjectID: req.ProjectID, Name: req.Name, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(webAPIImportMaxBodyBytes); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required")
			return nil, false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		return &asyncAPIImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Name:      strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)),
			Opts:      asyncapi.ConvertOptions{ScriptName: r.FormValue("script_name")},
		}, true
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &asyncAPIImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Opts:      asyncapi.ConvertOptions{ScriptName: r.URL.Query().Get("script_name")},
	}, true
}

func readOpenAPIImportRequest(w http.ResponseWriter, r *http.Request) (*openAPIImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return nil, false
		}
		var req openapi.ImportRequest
		if err := json.Unmarshal(data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		if strings.TrimSpace(req.Content) == "" && strings.Contains(string(data), `"paths"`) {
			return &openAPIImportRequest{
				Data:      data,
				ProjectID: r.URL.Query().Get("project_id"),
				Name:      r.URL.Query().Get("name"),
				Opts: openapi.ConvertOptions{
					BaseURL:    r.URL.Query().Get("base_url"),
					ScriptName: r.URL.Query().Get("script_name"),
				},
			}, true
		}
		return &openAPIImportRequest{Data: []byte(req.Content), ProjectID: req.ProjectID, Name: req.Name, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(webAPIImportMaxBodyBytes); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required")
			return nil, false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		return &openAPIImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Name:      strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)),
			Opts: openapi.ConvertOptions{
				BaseURL:    r.FormValue("base_url"),
				ScriptName: r.FormValue("script_name"),
			},
		}, true
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &openAPIImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Opts: openapi.ConvertOptions{
			BaseURL:    r.URL.Query().Get("base_url"),
			ScriptName: r.URL.Query().Get("script_name"),
		},
	}, true
}

func readWSDLImportRequest(w http.ResponseWriter, r *http.Request) (*wsdlImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		var req wsdl.ImportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		return &wsdlImportRequest{Data: []byte(req.Content), ProjectID: req.ProjectID, Name: req.Name, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(webAPIImportMaxBodyBytes); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required")
			return nil, false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		return &wsdlImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Name:      strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)),
			Opts: wsdl.ConvertOptions{
				BaseURL:     r.FormValue("base_url"),
				ScriptName:  r.FormValue("script_name"),
				SOAPVersion: r.FormValue("soap_version"),
			},
		}, true
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &wsdlImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Opts: wsdl.ConvertOptions{
			BaseURL:     r.URL.Query().Get("base_url"),
			ScriptName:  r.URL.Query().Get("script_name"),
			SOAPVersion: r.URL.Query().Get("soap_version"),
		},
	}, true
}

func readPostmanImportRequest(w http.ResponseWriter, r *http.Request) (*postmanImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, webAPIImportMaxBodyBytes)
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return nil, false
		}
		var req postman.ImportRequest
		if err := json.Unmarshal(data, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return nil, false
		}
		if strings.TrimSpace(req.Content) == "" && strings.Contains(string(data), `"item"`) {
			return &postmanImportRequest{
				Data:      data,
				ProjectID: r.URL.Query().Get("project_id"),
				Name:      r.URL.Query().Get("name"),
				Opts: postman.ConvertOptions{
					BaseURL:    r.URL.Query().Get("base_url"),
					ScriptName: r.URL.Query().Get("script_name"),
				},
			}, true
		}
		return &postmanImportRequest{Data: []byte(req.Content), ProjectID: req.ProjectID, Name: req.Name, Opts: req.Options}, true
	}
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(webAPIImportMaxBodyBytes); err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return nil, false
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required")
			return nil, false
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return nil, false
		}
		return &postmanImportRequest{
			Data:      data,
			ProjectID: r.FormValue("project_id"),
			Name:      strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename)),
			Opts: postman.ConvertOptions{
				BaseURL:    r.FormValue("base_url"),
				ScriptName: r.FormValue("script_name"),
			},
		}, true
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	filename := r.URL.Query().Get("filename")
	return &postmanImportRequest{
		Data:      data,
		ProjectID: r.URL.Query().Get("project_id"),
		Name:      strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)),
		Opts: postman.ConvertOptions{
			BaseURL:    r.URL.Query().Get("base_url"),
			ScriptName: r.URL.Query().Get("script_name"),
		},
	}, true
}

func buildOpenAPIReviewSections(result *openapi.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return nil
	}
	sections := []scriptImportReviewSection{
		{
			Key:      "openapi_operations",
			Title:    "OpenAPI Operations",
			Severity: "info",
			Summary:  "Converted operations available for replay after auth/data review.",
			Items:    result.Summary.OperationNames,
		},
	}
	if len(result.Summary.AuthSchemes) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "openapi_auth",
			Title:    "Authentication Review",
			Severity: "warning",
			Summary:  "Auth placeholders were inferred from the OpenAPI security schemes.",
			Items:    result.Summary.AuthSchemes,
		})
	}
	if len(result.Summary.DataColumns) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "openapi_data_fit",
			Title:    "Parameter Data Fit",
			Severity: "info",
			Summary:  "Generated CSV seed columns for path, query, and request-body placeholders.",
			Items:    result.Summary.DataColumns,
		})
	}
	return sections
}

func buildWSDLReviewSections(result *wsdl.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return nil
	}
	sections := []scriptImportReviewSection{
		{
			Key:      "wsdl_operations",
			Title:    "WSDL Operations",
			Severity: "info",
			Summary:  "Generated SOAP envelopes for review and replay.",
			Items:    result.Summary.Operations,
		},
	}
	if result.Summary.TargetNamespace != "" {
		sections = append(sections, scriptImportReviewSection{
			Key:      "wsdl_namespace",
			Title:    "Namespace",
			Severity: "info",
			Summary:  "Target namespace preserved from the WSDL.",
			Items:    []string{result.Summary.TargetNamespace},
		})
	}
	return sections
}

func buildPostmanReviewSections(result *postman.ConvertResult) []scriptImportReviewSection {
	if result == nil {
		return nil
	}
	sections := []scriptImportReviewSection{
		{
			Key:      "postman_requests",
			Title:    "Postman Requests",
			Severity: "info",
			Summary:  "Converted Postman requests into HTTP actions for replay review.",
			Items:    result.Summary.Requests,
		},
	}
	if len(result.Summary.Variables) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "postman_variables",
			Title:    "Collection Variables",
			Severity: "info",
			Summary:  "Collection variables were converted into APEX script parameters.",
			Items:    result.Summary.Variables,
		})
	}
	if len(result.Summary.AuthSchemes) > 0 {
		sections = append(sections, scriptImportReviewSection{
			Key:      "postman_auth",
			Title:    "Authentication Review",
			Severity: "warning",
			Summary:  "Postman auth settings were detected; review generated placeholders before replay.",
			Items:    result.Summary.AuthSchemes,
		})
	}
	return sections
}
