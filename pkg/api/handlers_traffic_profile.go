package api

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/trafficprofile"
)

const (
	trafficProfileUploadMaxBytes   = 25 << 20
	trafficProfileDeriveMaxBytes   = 8 << 20
	trafficProfileUpstreamMaxBytes = 16 << 20
	trafficProfileMaxSeries        = 100
)

type trafficProfileSourcesResponse struct {
	Sources []trafficProfileSourceCapability `json:"sources"`
	Limits  trafficProfileLimits             `json:"limits"`
}

type trafficProfileSourceCapability struct {
	Kind       string   `json:"kind"`
	Enabled    bool     `json:"enabled"`
	Configured bool     `json:"configured"`
	Formats    []string `json:"formats,omitempty"`
}

type trafficProfileLimits struct {
	UploadBytes      int64 `json:"upload_bytes"`
	DeriveBytes      int64 `json:"derive_bytes"`
	UpstreamBytes    int64 `json:"upstream_bytes"`
	PointsPerSeries  int   `json:"points_per_series"`
	PointsPerProfile int   `json:"points_per_profile"`
}

func currentTrafficProfileLimits() trafficProfileLimits {
	return trafficProfileLimits{
		UploadBytes: trafficProfileUploadMaxBytes, DeriveBytes: trafficProfileDeriveMaxBytes,
		UpstreamBytes: trafficProfileUpstreamMaxBytes, PointsPerSeries: trafficprofile.MaxPointsPerSeries,
		PointsPerProfile: trafficprofile.MaxPointsPerProfile,
	}
}

type trafficProfileAnalyzeRequest struct {
	ProjectID string               `json:"project_id,omitempty"`
	Source    TrafficProfileSource `json:"source"`
}

type trafficProfileAnalyzedSeries struct {
	Series trafficprofile.Series `json:"series"`
	Stats  trafficprofile.Stats  `json:"stats"`
}

type trafficProfileAnalyzeResponse struct {
	Source   TrafficProfileSource           `json:"source"`
	Sheets   []trafficprofile.Sheet         `json:"sheets,omitempty"`
	Series   []trafficProfileAnalyzedSeries `json:"series,omitempty"`
	Warnings []string                       `json:"warnings,omitempty"`
	Limits   trafficProfileLimits           `json:"limits"`
}

type trafficProfileSeriesMappingRequest struct {
	SeriesKey string                     `json:"series_key"`
	GroupName string                     `json:"group_name"`
	Source    *TrafficProfileSourceEntry `json:"source,omitempty"`
}

type trafficProfileDeriveRequest struct {
	ProjectID      string                               `json:"project_id,omitempty"`
	Source         TrafficProfileSource                 `json:"source"`
	Series         []trafficprofile.Series              `json:"series"`
	Mappings       []trafficProfileSeriesMappingRequest `json:"mappings"`
	ValueSemantics trafficprofile.ValueSemantics        `json:"value_semantics"`
	GapPolicy      trafficprofile.GapPolicy             `json:"gap_policy"`
	Window         TrafficProfileWindow                 `json:"window,omitempty"`
	TargetDuration string                               `json:"target_duration,omitempty"`
	Amplitude      TrafficProfileAmplitude              `json:"amplitude,omitempty"`
	ScenarioDraft  CreateScenarioRequest                `json:"scenario_draft"`
}

type trafficProfileDeriveResponse struct {
	TrafficProfile      *TrafficProfile            `json:"traffic_profile"`
	Preview             []TrafficProfileMapping    `json:"preview"`
	Warnings            []string                   `json:"warnings,omitempty"`
	ScenarioDraft       CreateScenarioRequest      `json:"scenario_draft"`
	LoadModelValidation *LoadModelValidationReport `json:"load_model_validation,omitempty"`
}

func (s *Server) handleTrafficProfileSources(w http.ResponseWriter, r *http.Request) {
	settings := s.runner.GetSystemSettings()
	prom := newPromGraphDataSource(normalizePrometheusSettings(settings.Prometheus))
	promEnabled, promConfigured := prom.Available()
	openSearch := newOpenSearchTrafficProfileSource(settings.OpenSearch)
	openEnabled, openConfigured := openSearch.available()
	writeJSON(w, http.StatusOK, trafficProfileSourcesResponse{
		Sources: []trafficProfileSourceCapability{
			{Kind: "file", Enabled: true, Configured: true, Formats: []string{"csv", "xlsx"}},
			{Kind: "prometheus", Enabled: promEnabled, Configured: promConfigured},
			{Kind: "opensearch", Enabled: openEnabled, Configured: openConfigured},
		},
		Limits: currentTrafficProfileLimits(),
	})
}

func (s *Server) handleAnalyzeTrafficProfile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, trafficProfileUploadMaxBytes)
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/") {
		s.handleAnalyzeTrafficProfileFile(w, r)
		return
	}
	var request trafficProfileAnalyzeRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	start, end, err := resolveTrafficProfileRange(request.Source)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var series []trafficprofile.Series
	switch strings.ToLower(strings.TrimSpace(request.Source.Kind)) {
	case "prometheus":
		if request.Source.Prometheus == nil {
			writeError(w, http.StatusBadRequest, "source.prometheus is required")
			return
		}
		dataSource := newPromGraphDataSource(normalizePrometheusSettings(s.runner.GetSystemSettings().Prometheus))
		enabled, configured := dataSource.Available()
		if !enabled || !configured {
			writeError(w, http.StatusServiceUnavailable, "Prometheus is not enabled and configured")
			return
		}
		query := request.Source.Prometheus
		values, queryErr := dataSource.QueryRange(r.Context(), graphRangeQuery{Expr: query.Query, Start: start, End: end, StepSeconds: query.Range.StepSeconds, MaxSeries: trafficProfileMaxSeries, MaxBytes: trafficProfileUpstreamMaxBytes, LabelFields: query.LabelFields, SeriesKeyPrefix: "traffic-profile"})
		if queryErr != nil {
			writeError(w, http.StatusBadGateway, queryErr.Error())
			return
		}
		for _, value := range values {
			points := make([]trafficprofile.Point, 0, len(value.Values))
			for _, point := range value.Values {
				points = append(points, trafficprofile.Point{Timestamp: point.Timestamp, Value: point.Value})
			}
			series = append(series, trafficprofile.Series{Key: value.Key, Name: value.Label, Labels: value.Labels, Points: points})
		}
	case "opensearch":
		if request.Source.OpenSearch == nil {
			writeError(w, http.StatusBadRequest, "source.opensearch is required")
			return
		}
		dataSource := newOpenSearchTrafficProfileSource(s.runner.GetSystemSettings().OpenSearch)
		enabled, configured := dataSource.available()
		if !enabled || !configured {
			writeError(w, http.StatusServiceUnavailable, "OpenSearch is not enabled and configured")
			return
		}
		series, err = dataSource.query(r.Context(), *request.Source.OpenSearch, start, end, trafficProfileUpstreamMaxBytes)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "source.kind must be prometheus or opensearch for JSON analysis")
		return
	}
	s.writeTrafficProfileAnalysis(w, request.Source, nil, series)
}

func (s *Server) handleAnalyzeTrafficProfileFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, trafficProfileUploadMaxBytes)
	if err := r.ParseMultipartForm(trafficProfileUploadMaxBytes); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "invalid or oversized traffic-profile upload")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "multipart file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, trafficProfileUploadMaxBytes+1))
	if err != nil || len(data) > trafficProfileUploadMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "traffic-profile upload exceeds 25 MiB")
		return
	}
	extension := strings.ToLower(filepath.Ext(header.Filename))
	var sheets []trafficprofile.Sheet
	if extension == ".xlsx" {
		sheets, err = trafficprofile.ParseXLSX(data)
	} else if extension == ".csv" || extension == ".txt" || extension == ".tsv" {
		var sheet *trafficprofile.Sheet
		sheet, err = trafficprofile.ParseCSV(data)
		if sheet != nil {
			sheets = []trafficprofile.Sheet{*sheet}
		}
	} else {
		writeError(w, http.StatusUnsupportedMediaType, "only CSV, TSV, and XLSX uploads are supported")
		return
	}
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	selected := strings.TrimSpace(r.FormValue("sheet"))
	if selected == "" && len(sheets) == 1 {
		selected = sheets[0].Name
	}
	timestampMode := strings.ToLower(strings.TrimSpace(r.FormValue("timestamp_mode")))
	if timestampMode == "" {
		timestampMode = "absolute"
	}
	source := TrafficProfileSource{Kind: "file", Name: header.Filename, File: &TrafficProfileFileSource{Filename: header.Filename, Sheet: selected, TimestampColumn: r.FormValue("timestamp_column"), TimestampMode: timestampMode, RelativeStartTime: strings.TrimSpace(r.FormValue("relative_start_time")), SeriesColumn: r.FormValue("series_column"), Timezone: r.FormValue("timezone")}}
	valueColumns := append([]string(nil), r.MultipartForm.Value["value_column"]...)
	if len(valueColumns) == 0 {
		for _, value := range strings.Split(r.FormValue("value_columns"), ",") {
			if value = strings.TrimSpace(value); value != "" {
				valueColumns = append(valueColumns, value)
			}
		}
	}
	source.File.ValueColumns = valueColumns
	if source.File.TimestampColumn == "" || len(valueColumns) == 0 || selected == "" {
		writeJSON(w, http.StatusOK, trafficProfileAnalyzeResponse{Source: source, Sheets: sheets, Limits: currentTrafficProfileLimits()})
		return
	}
	var sheet *trafficprofile.Sheet
	for index := range sheets {
		if sheets[index].Name == selected {
			sheet = &sheets[index]
			break
		}
	}
	if sheet == nil {
		writeError(w, http.StatusBadRequest, "selected worksheet was not found")
		return
	}
	var location *time.Location
	if source.File.Timezone != "" {
		location, err = time.LoadLocation(source.File.Timezone)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid timezone")
			return
		}
	}
	var relativeStart time.Time
	if timestampMode == "relative" {
		if location == nil {
			writeError(w, http.StatusBadRequest, "relative timestamps require a timezone")
			return
		}
		relativeStart, err = parseTrafficProfileRelativeStartTime(source.File.RelativeStartTime, location)
		if err != nil {
			writeError(w, http.StatusBadRequest, "relative start time "+err.Error())
			return
		}
	} else if timestampMode != "absolute" {
		writeError(w, http.StatusBadRequest, "timestamp mode must be absolute or relative")
		return
	}
	series, err := trafficprofile.SeriesFromSheet(sheet, trafficprofile.TableOptions{TimestampColumn: source.File.TimestampColumn, TimestampMode: timestampMode, RelativeStart: relativeStart, ValueColumns: valueColumns, SeriesColumn: source.File.SeriesColumn, Timezone: location})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.writeTrafficProfileAnalysis(w, source, sheets, series)
}

func (s *Server) writeTrafficProfileAnalysis(w http.ResponseWriter, source TrafficProfileSource, sheets []trafficprofile.Sheet, series []trafficprofile.Series) {
	if len(series) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "analysis did not discover any series with at least two points")
		return
	}
	if len(series) > trafficProfileMaxSeries {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("analysis discovered %d series; limit is %d", len(series), trafficProfileMaxSeries))
		return
	}
	total := 0
	response := trafficProfileAnalyzeResponse{Source: source, Sheets: sheets, Limits: currentTrafficProfileLimits()}
	for _, item := range series {
		total += len(item.Points)
		if len(item.Points) > trafficprofile.MaxPointsPerSeries || total > trafficprofile.MaxPointsPerProfile {
			writeError(w, http.StatusUnprocessableEntity, "traffic profile exceeds point limits")
			return
		}
		stats, err := trafficprofile.SeriesStats(item)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		response.Series = append(response.Series, trafficProfileAnalyzedSeries{Series: item, Stats: stats})
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleDeriveTrafficProfile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, trafficProfileDeriveMaxBytes)
	var request trafficProfileDeriveRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	// The route authorizes against the declared project_id, but the draft it
	// compiles carries caller-supplied script IDs. Pin the draft to the
	// authorized project so derivation cannot become a cross-project read of
	// another tenant's scripts, data pools, and capacity advisory.
	if draftProjectID := trafficProfileAuthorizedProjectID(r, request.ProjectID); draftProjectID != "" {
		request.ScenarioDraft.ProjectID = draftProjectID
		if _, err := s.scenarioRequestProjectID(request.ScenarioDraft); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if len(request.Mappings) == 0 {
		writeError(w, http.StatusBadRequest, "at least one series mapping is required")
		return
	}
	if len(request.Series) == 0 || len(request.Series) > trafficProfileMaxSeries {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("series must contain 1..%d entries", trafficProfileMaxSeries))
		return
	}
	seriesByKey := make(map[string]trafficprofile.Series, len(request.Series))
	total := 0
	for _, series := range request.Series {
		series.Key = strings.TrimSpace(series.Key)
		if series.Key == "" {
			writeError(w, http.StatusBadRequest, "every analyzed series requires a non-empty key")
			return
		}
		if _, exists := seriesByKey[series.Key]; exists {
			writeError(w, http.StatusBadRequest, "analyzed series keys must be unique")
			return
		}
		if len(series.Points) > trafficprofile.MaxPointsPerSeries {
			writeError(w, http.StatusRequestEntityTooLarge, "analyzed series exceeds the per-series point limit")
			return
		}
		total += len(series.Points)
		seriesByKey[series.Key] = series
	}
	if total > trafficprofile.MaxPointsPerProfile {
		writeError(w, http.StatusRequestEntityTooLarge, "traffic profile exceeds total point limit")
		return
	}
	window, err := trafficProfileWindowSpec(request.Window)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var targetDuration time.Duration
	if strings.TrimSpace(request.TargetDuration) != "" {
		targetDuration, err = time.ParseDuration(request.TargetDuration)
		if err != nil || targetDuration <= 0 {
			writeError(w, http.StatusBadRequest, "target_duration must be a positive duration")
			return
		}
	}
	if err := validateTrafficProfileSource(request.Source); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	type normalizationCandidate struct {
		mapping    trafficProfileSeriesMappingRequest
		series     trafficprofile.Series
		source     TrafficProfileSource
		sourceEnd  time.Time
		normalized trafficprofile.NormalizedSeries
	}
	candidates := make([]normalizationCandidate, 0, len(request.Mappings))
	resolvedTargetDuration := targetDuration
	maximumSourceDuration := time.Duration(0)
	for _, mapping := range request.Mappings {
		series, ok := seriesByKey[mapping.SeriesKey]
		if !ok {
			writeError(w, http.StatusBadRequest, "mapped series "+mapping.SeriesKey+" was not analyzed")
			return
		}
		mappingSource := request.Source
		if strings.EqualFold(strings.TrimSpace(request.Source.Kind), "multiple") && mapping.Source == nil {
			writeError(w, http.StatusBadRequest, "every mapped series requires source provenance when source.kind is multiple")
			return
		}
		if mapping.Source != nil {
			mappingSource = mapping.Source.asSource()
		}
		if err := validateTrafficProfileSource(mappingSource); err != nil {
			writeError(w, http.StatusBadRequest, "mapped series "+mapping.SeriesKey+" source: "+err.Error())
			return
		}
		var sourceEnd time.Time
		if request.Window.Mode == "" || request.Window.Mode == "full" {
			kind := strings.ToLower(strings.TrimSpace(mappingSource.Kind))
			if kind == "prometheus" || kind == "opensearch" {
				_, sourceEnd, err = resolveTrafficProfileRange(mappingSource)
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
			}
		}
		normalized, normalizeErr := trafficprofile.Normalize(series, trafficprofile.NormalizeOptions{Semantics: request.ValueSemantics, GapPolicy: request.GapPolicy, Window: window, SourceEnd: sourceEnd, TargetDuration: targetDuration, AmplitudeMode: request.Amplitude.Mode, Amplitude: request.Amplitude.Value})
		if normalizeErr != nil {
			writeError(w, http.StatusUnprocessableEntity, normalizeErr.Error())
			return
		}
		if normalized.SourceDuration > maximumSourceDuration {
			maximumSourceDuration = normalized.SourceDuration
		}
		if targetDuration == 0 && normalized.TargetDuration > resolvedTargetDuration {
			resolvedTargetDuration = normalized.TargetDuration
		}
		candidates = append(candidates, normalizationCandidate{mapping: mapping, series: series, source: mappingSource, sourceEnd: sourceEnd, normalized: normalized})
	}
	profile := &TrafficProfile{Version: trafficProfileVersion, Source: request.Source, ValueSemantics: string(request.ValueSemantics), Interpolation: "step", GapPolicy: string(request.GapPolicy), Window: request.Window, Amplitude: request.Amplitude, SourceDuration: maximumSourceDuration.String(), TargetDuration: resolvedTargetDuration.String(), RefreshedAt: time.Now().UTC()}
	warnings := make([]string, 0)
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.normalized.TargetDuration != resolvedTargetDuration {
			normalized, normalizeErr := trafficprofile.Normalize(candidate.series, trafficprofile.NormalizeOptions{Semantics: request.ValueSemantics, GapPolicy: request.GapPolicy, Window: window, SourceEnd: candidate.sourceEnd, TargetDuration: resolvedTargetDuration, AmplitudeMode: request.Amplitude.Mode, Amplitude: request.Amplitude.Value})
			if normalizeErr != nil {
				writeError(w, http.StatusUnprocessableEntity, normalizeErr.Error())
				return
			}
			candidate.normalized = normalized
			warnings = append(warnings, fmt.Sprintf("%s was proportionally aligned from %s to the shared test duration %s", candidate.series.Name, normalized.SourceDuration, resolvedTargetDuration))
		}
		normalized := candidate.normalized
		if profile.RuntimeTarget == "" {
			profile.RuntimeTarget = normalized.RuntimeTarget
		}
		peak := 0.0
		for _, point := range normalized.Points {
			peak = math.Max(peak, point.Target)
		}
		resultMapping := TrafficProfileMapping{SeriesKey: candidate.series.Key, SeriesName: candidate.series.Name, GroupName: candidate.mapping.GroupName, SourceDuration: normalized.SourceDuration.String(), Points: normalized.Points, PeakTarget: peak}
		if strings.EqualFold(strings.TrimSpace(request.Source.Kind), "multiple") {
			source := trafficProfileSourceEntry(candidate.source)
			resultMapping.Source = &source
		}
		profile.Mappings = append(profile.Mappings, resultMapping)
		warnings = append(warnings, normalized.Warnings...)
	}
	draft := request.ScenarioDraft
	draft.TrafficProfile = profile
	draft.Duration = profile.TargetDuration
	shape := "production-replay"
	draft.WorkloadShapeID = &shape
	if profile.RuntimeTarget == "vusers" {
		draft.ScenarioMode = "vuser_driven"
	} else {
		draft.ScenarioMode = "goal_oriented"
		// A profile supplies the changing TPS objective, but the adaptive
		// controller still needs an initial safety envelope. Preserve an
		// operator-provided envelope; otherwise seed conservative editable
		// bounds from the draft's current VUser allocation.
		if draft.GoalConfig == nil || draft.GoalConfig.MinVUsers == nil || draft.GoalConfig.MaxVUsers == nil {
			maximum := 0
			for _, group := range draft.Groups {
				vusers := max(0, group.VUsers)
				if vusers > maxManagedTestVUsers-maximum {
					maximum = maxManagedTestVUsers
					break
				}
				maximum += vusers
			}
			maximum = max(1, maximum)
			minimum := 1
			if draft.GoalConfig == nil {
				draft.GoalConfig = &GoalConfig{}
			}
			if draft.GoalConfig.MinVUsers == nil {
				draft.GoalConfig.MinVUsers = &minimum
			}
			if draft.GoalConfig.MaxVUsers == nil {
				draft.GoalConfig.MaxVUsers = &maximum
			}
			warnings = append(warnings, "Adaptive TPS guardrails were initialized from the scenario VUser allocation; review min/max VUsers or use baseline sizing before launch.")
		}
	}
	for index := range draft.Groups {
		for mappingIndex := range profile.Mappings {
			mapping := &profile.Mappings[mappingIndex]
			if normalizeEffectiveGroupName(draft.Groups[index].Name) != normalizeEffectiveGroupName(mapping.GroupName) {
				continue
			}
			if len(draft.Groups[index].Stages) > 0 || strings.TrimSpace(draft.Groups[index].StartOffset) != "" || strings.TrimSpace(draft.Groups[index].RampUp) != "" || strings.TrimSpace(draft.Groups[index].RampDown) != "" {
				writeError(w, http.StatusConflict, "mapped groups must clear manual stages, ramps, and start_offset before derivation")
				return
			}
			if profile.RuntimeTarget == "vusers" {
				peak := 0
				for _, point := range mapping.Points {
					if int(point.Target) > peak {
						peak = int(point.Target)
					}
				}
				draft.Groups[index].VUsers = peak
			}
		}
	}
	profile.Warnings = dedupeTrafficProfileWarnings(warnings)
	if err := validateTrafficProfile(profile, draft.Groups, draft.Duration, draft.ScenarioMode, draft.GoalConfig); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	var validation *LoadModelValidationReport
	if trafficProfileDraftHasScripts(draft) {
		validation, err = s.runner.ValidateScenarioLoadModel(draft)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	} else {
		profile.Warnings = dedupeTrafficProfileWarnings(append(profile.Warnings, "Select a script for every group to generate the load-model validation report."))
	}
	writeJSON(w, http.StatusOK, trafficProfileDeriveResponse{TrafficProfile: profile, Preview: profile.Mappings, Warnings: profile.Warnings, ScenarioDraft: draft, LoadModelValidation: validation})
}

// trafficProfileAuthorizedProjectID reports the project this request was
// actually authorized against: the binding recorded by the project-scoped RBAC
// path, otherwise the declared project_id the route's ProjectLookup consumed.
// A globally granted caller may omit it, in which case the draft's own scripts
// remain the only scope, exactly as they were before the feature-policy work.
func trafficProfileAuthorizedProjectID(r *http.Request, declared string) string {
	if authorized, scoped := authorizedProjectFromRequest(r); scoped {
		return normalizeProjectID(authorized)
	}
	if projectID := normalizeExplicitImportProjectID(declared); projectID != "" {
		return projectID
	}
	return normalizeExplicitImportProjectID(r.URL.Query().Get("project_id"))
}

func trafficProfileDraftHasScripts(draft CreateScenarioRequest) bool {
	if len(draft.Groups) == 0 {
		return false
	}
	for _, group := range draft.Groups {
		if strings.TrimSpace(group.ScriptID) == "" {
			return false
		}
	}
	return true
}

func resolveTrafficProfileRange(source TrafficProfileSource) (time.Time, time.Time, error) {
	var value TrafficProfileQueryRange
	switch strings.ToLower(strings.TrimSpace(source.Kind)) {
	case "prometheus":
		if source.Prometheus == nil {
			return time.Time{}, time.Time{}, errors.New("source.prometheus is required")
		}
		value = source.Prometheus.Range
	case "opensearch":
		if source.OpenSearch == nil {
			return time.Time{}, time.Time{}, errors.New("source.opensearch is required")
		}
		value = source.OpenSearch.Range
	default:
		return time.Time{}, time.Time{}, errors.New("unsupported query source")
	}
	mode := strings.ToLower(strings.TrimSpace(value.Mode))
	if mode != "absolute" && mode != "relative" {
		return time.Time{}, time.Time{}, errors.New("range.mode must be absolute or relative")
	}
	if value.StepSeconds <= 0 {
		return time.Time{}, time.Time{}, errors.New("range.step_seconds must be positive")
	}
	end := value.End.UTC()
	if end.IsZero() {
		end = time.Now().UTC()
	}
	start := value.Start.UTC()
	if mode == "relative" {
		lookback, err := time.ParseDuration(value.Lookback)
		if err != nil || lookback <= 0 {
			return time.Time{}, time.Time{}, errors.New("relative range requires a positive lookback")
		}
		start = end.Add(-lookback)
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, errors.New("query range end must be after start")
	}
	if int(end.Sub(start)/time.Duration(value.StepSeconds)/time.Second)+1 > trafficprofile.MaxPointsPerSeries {
		return time.Time{}, time.Time{}, fmt.Errorf("query range can return more than %d points per series", trafficprofile.MaxPointsPerSeries)
	}
	return start, end, nil
}

func trafficProfileWindowSpec(window TrafficProfileWindow) (trafficprofile.WindowSpec, error) {
	result := trafficprofile.WindowSpec{Mode: window.Mode, Start: window.Start, End: window.End}
	startOffset := strings.TrimSpace(window.StartOffset)
	endOffset := strings.TrimSpace(window.EndOffset)
	if startOffset != "" || endOffset != "" {
		if strings.TrimSpace(window.Mode) != "explicit" {
			return result, errors.New("window offsets require mode explicit")
		}
		if startOffset == "" || endOffset == "" {
			return result, errors.New("explicit offset window requires start_offset and end_offset")
		}
		if !window.Start.IsZero() || !window.End.IsZero() {
			return result, errors.New("explicit window cannot mix timestamps and offsets")
		}
		var err error
		result.StartOffset, err = time.ParseDuration(startOffset)
		if err != nil || result.StartOffset < 0 {
			return result, errors.New("explicit window start_offset must be a nonnegative duration")
		}
		result.EndOffset, err = time.ParseDuration(endOffset)
		if err != nil || result.EndOffset <= result.StartOffset {
			return result, errors.New("explicit window end_offset must be after start_offset")
		}
		result.RelativeOffsets = true
	}
	if strings.TrimSpace(window.Duration) != "" {
		duration, err := time.ParseDuration(window.Duration)
		if err != nil {
			return result, fmt.Errorf("invalid window duration: %w", err)
		}
		result.Duration = duration
	}
	return result, nil
}

func dedupeTrafficProfileWarnings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				result = append(result, value)
			}
		}
	}
	return result
}
