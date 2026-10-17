package api

import (
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

// This preview accepts only caller-supplied inline data. It never resolves host
// paths, storage references, secrets, environment variables, or network targets.
type parameterPreviewRequest struct {
	Parameters  []dsl.Parameter  `json:"parameters"`
	DataSources []dsl.DataSource `json:"data_sources"`
	Name        string           `json:"name"`
	VUser       int              `json:"vuser"`
	Iteration   int              `json:"iteration"`
	Occurrence  int              `json:"occurrence"`
}

type parameterPreviewResponse struct {
	Value    string `json:"value"`
	Resolved bool   `json:"resolved"`
	Masked   bool   `json:"masked"`
	Note     string `json:"note"`
}

func (s *Server) handlePreviewParameter(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	var req parameterPreviewRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.VUser < 1 || req.VUser > 100000 || req.Iteration < 1 || req.Iteration > 1000 || req.Occurrence < 1 || req.Occurrence > 100 || len(req.Parameters) > 128 || len(req.DataSources) > 128 {
		writeError(w, http.StatusBadRequest, "Preview supports virtual users 1–100000, iterations 1–1000, occurrences 1–100, and up to 128 parameters and sources.")
		return
	}
	byName := make(map[string]dsl.Parameter)
	for _, parameter := range req.Parameters {
		parameter.Type = dsl.CanonicalParameterType(parameter.Type)
		name := strings.ToLower(strings.TrimSpace(parameter.Name))
		if _, exists := byName[name]; exists {
			writeError(w, http.StatusBadRequest, "Duplicate parameter names.")
			return
		}
		byName[name] = parameter
	}
	needed := make(map[string]bool)
	name := strings.ToLower(strings.TrimSpace(req.Name))
	for name != "" {
		if needed[name] {
			writeError(w, http.StatusBadRequest, "Circular same-row parameter reference.")
			return
		}
		parameter, exists := byName[name]
		if !exists {
			writeError(w, http.StatusBadRequest, "Parameter reference not found.")
			return
		}
		if parameter.Type == "host" {
			writeJSON(w, http.StatusOK, parameterPreviewResponse{Note: "Host values are supplied by the execution host and cannot be previewed here."})
			return
		}
		if dsl.AutoAllocatesUniqueRows(parameter) {
			writeJSON(w, http.StatusOK, parameterPreviewResponse{Note: "Unique rows are allocated to each virtual user when a run starts, from its virtual user count and iterations, so this preview cannot show which rows a virtual user receives. Set a block size to preview a fixed block."})
			return
		}
		needed[name] = true
		name = strings.ToLower(strings.TrimSpace(parameter.SameLineAs))
	}
	if len(needed) == 0 {
		writeError(w, http.StatusBadRequest, "Select a parameter.")
		return
	}
	parameters := make([]dsl.Parameter, 0, len(needed))
	sourceNames := make(map[string]bool)
	for _, parameter := range req.Parameters {
		if needed[strings.ToLower(strings.TrimSpace(parameter.Name))] {
			if parameter.Length > 4096 || parameter.PadWidth > 4096 || len(parameter.Format) > 4096 {
				writeError(w, http.StatusBadRequest, "Preview output length, padding, and date/time layouts are limited to 4096 bytes.")
				return
			}
			parameter.Type = dsl.CanonicalParameterType(parameter.Type)
			parameters = append(parameters, parameter)
			if parameter.DataSource != "" {
				sourceNames[strings.ToLower(strings.TrimSpace(parameter.DataSource))] = true
			}
		}
	}
	sources := make([]dsl.DataSource, 0, len(sourceNames))
	for _, source := range req.DataSources {
		if !sourceNames[strings.ToLower(strings.TrimSpace(source.Name))] {
			continue
		}
		if source.InlineData == "" && (source.Path != "" || source.StorageRef != "" || source.StagedAsset != "") {
			writeJSON(w, http.StatusOK, parameterPreviewResponse{Note: "External dataset is not loaded. Attach an inline copy to preview; generator availability remains an execution preflight check."})
			return
		}
		if source.Type != "csv" && source.Type != "dat" {
			writeError(w, http.StatusBadRequest, "Only inline CSV and DAT sources can be previewed.")
			return
		}
		source.Path, source.StorageRef, source.StagedAsset = "", "", ""
		sources = append(sources, source)
	}
	script := &dsl.Script{Name: "Parameter preview", Version: "1", Protocol: "http", Config: dsl.DefaultScriptConfig(), Parameters: parameters, DataSources: sources}
	// Reject every validation failure except the two intentional omissions in this actionless preview.
	for _, issue := range dsl.ValidateScriptIssues(script) {
		if issue != "script must have at least one action" && issue != "config.base_url is empty" {
			writeError(w, http.StatusBadRequest, "Parameter or dataset configuration is invalid. Validate the script to review the field errors.")
			return
		}
	}
	provider, err := engine.NewDataProvider(sources)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Dataset could not be parsed for preview.")
		return
	}
	runtime := engine.NewParameterRuntime(parameters, provider, req.VUser-1)
	runtime.Prime()
	for iteration := 1; iteration < req.Iteration; iteration++ {
		if r.Context().Err() != nil {
			return
		}
		runtime.RefreshIteration()
	}
	var value string
	var resolved bool
	for occurrence := 0; occurrence < req.Occurrence; occurrence++ {
		value, resolved = runtime.ResolveNamed(req.Name)
	}
	selected := byName[strings.ToLower(strings.TrimSpace(req.Name))]
	masked := credential.SensitiveName(selected.Name) || credential.SensitiveName(selected.Column)
	if masked {
		value = credential.Redacted
	}
	writeJSON(w, http.StatusOK, parameterPreviewResponse{Value: value, Resolved: resolved, Masked: masked, Note: "Engine preview for one isolated virtual user in an unsharded dataset. Time and unseeded random values vary; concurrent row reservations and earlier each-occurrence references are not simulated."})
}
