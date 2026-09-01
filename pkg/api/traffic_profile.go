package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/trafficprofile"
)

const trafficProfileVersion = 1

type TrafficProfile struct {
	Version        int                     `json:"version"`
	Source         TrafficProfileSource    `json:"source"`
	ValueSemantics string                  `json:"value_semantics"`
	RuntimeTarget  string                  `json:"runtime_target"`
	Interpolation  string                  `json:"interpolation"`
	SourceDuration string                  `json:"source_duration"`
	TargetDuration string                  `json:"target_duration"`
	GapPolicy      string                  `json:"gap_policy,omitempty"`
	Window         TrafficProfileWindow    `json:"window,omitempty"`
	Amplitude      TrafficProfileAmplitude `json:"amplitude,omitempty"`
	Mappings       []TrafficProfileMapping `json:"mappings"`
	Sizing         *TrafficProfileSizing   `json:"sizing,omitempty"`
	Warnings       []string                `json:"warnings,omitempty"`
	RefreshedAt    time.Time               `json:"refreshed_at"`
}

type TrafficProfileSource struct {
	Kind       string                          `json:"kind"`
	Name       string                          `json:"name,omitempty"`
	File       *TrafficProfileFileSource       `json:"file,omitempty"`
	Prometheus *TrafficProfilePrometheusSource `json:"prometheus,omitempty"`
	OpenSearch *TrafficProfileOpenSearchSource `json:"opensearch,omitempty"`
	Sources    []TrafficProfileSourceEntry     `json:"sources,omitempty"`
}

// TrafficProfileSourceEntry is deliberately non-recursive. Composite sources
// may contain primitive source entries, but never another composite source.
type TrafficProfileSourceEntry struct {
	Kind       string                          `json:"kind"`
	Name       string                          `json:"name,omitempty"`
	File       *TrafficProfileFileSource       `json:"file,omitempty"`
	Prometheus *TrafficProfilePrometheusSource `json:"prometheus,omitempty"`
	OpenSearch *TrafficProfileOpenSearchSource `json:"opensearch,omitempty"`
}

type TrafficProfileFileSource struct {
	Filename          string   `json:"filename"`
	Sheet             string   `json:"sheet,omitempty"`
	TimestampColumn   string   `json:"timestamp_column"`
	TimestampMode     string   `json:"timestamp_mode,omitempty"`
	RelativeStartTime string   `json:"relative_start_time,omitempty"`
	ValueColumns      []string `json:"value_columns"`
	ARTColumns        []string `json:"art_columns,omitempty"`
	SeriesColumn      string   `json:"series_column,omitempty"`
	Timezone          string   `json:"timezone,omitempty"`
}

type TrafficProfileQueryRange struct {
	Mode        string    `json:"mode"`
	Start       time.Time `json:"start,omitempty"`
	End         time.Time `json:"end,omitempty"`
	Lookback    string    `json:"lookback,omitempty"`
	StepSeconds int       `json:"step_seconds"`
}

type TrafficProfilePrometheusSource struct {
	Query       string                   `json:"query"`
	Range       TrafficProfileQueryRange `json:"range"`
	LabelFields []string                 `json:"label_fields,omitempty"`
}

type TrafficProfileOpenSearchSource struct {
	Index          string                   `json:"index"`
	TimestampField string                   `json:"timestamp_field"`
	Filter         json.RawMessage          `json:"filter,omitempty"`
	Interval       string                   `json:"interval"`
	Metric         string                   `json:"metric"`
	MetricField    string                   `json:"metric_field,omitempty"`
	Range          TrafficProfileQueryRange `json:"range"`
}

type TrafficProfileWindow struct {
	Mode        string    `json:"mode,omitempty"`
	Start       time.Time `json:"start,omitempty"`
	End         time.Time `json:"end,omitempty"`
	StartOffset string    `json:"start_offset,omitempty"`
	EndOffset   string    `json:"end_offset,omitempty"`
	Duration    string    `json:"duration,omitempty"`
}

type TrafficProfileAmplitude struct {
	Mode  string  `json:"mode,omitempty"`
	Value float64 `json:"value,omitempty"`
}

type TrafficProfileMapping struct {
	SeriesKey      string                        `json:"series_key"`
	SeriesName     string                        `json:"series_name"`
	GroupName      string                        `json:"group_name"`
	Source         *TrafficProfileSourceEntry    `json:"source,omitempty"`
	SourceStart    string                        `json:"source_start,omitempty"`
	SourceEnd      string                        `json:"source_end,omitempty"`
	SourceDuration string                        `json:"source_duration,omitempty"`
	Points         []trafficprofile.RuntimePoint `json:"points"`
	PeakTarget     float64                       `json:"peak_target"`
	ART            *TrafficProfileART            `json:"art,omitempty"`
}

type TrafficProfileART struct {
	Basis               string                    `json:"basis"`
	SeriesKey           string                    `json:"series_key,omitempty"`
	SeriesName          string                    `json:"series_name,omitempty"`
	SourceUnit          string                    `json:"source_unit,omitempty"`
	Points              []trafficprofile.ARTPoint `json:"points,omitempty"`
	MinARTMS            float64                   `json:"min_art_ms"`
	AverageARTMS        float64                   `json:"average_art_ms"`
	PeakARTMS           float64                   `json:"peak_art_ms"`
	EstimatedPeakVUsers int                       `json:"estimated_peak_vusers"`
	FallbackToPeak      bool                      `json:"fallback_to_peak,omitempty"`
}

type TrafficProfileSizing struct {
	Complete              bool `json:"complete"`
	ProfilePeakVUsers     int  `json:"profile_peak_vusers"`
	StaticPeakVUsers      int  `json:"static_peak_vusers"`
	EstimatedPeakVUsers   int  `json:"estimated_peak_vusers"`
	SuggestedMinVUsers    int  `json:"suggested_min_vusers"`
	SuggestedMaxVUsers    int  `json:"suggested_max_vusers"`
	RecommendedGenerators int  `json:"recommended_generators,omitempty"`
}

func cloneTrafficProfile(profile *TrafficProfile) *TrafficProfile {
	if profile == nil {
		return nil
	}
	data, err := json.Marshal(profile)
	if err != nil {
		return nil
	}
	var cloned TrafficProfile
	if json.Unmarshal(data, &cloned) != nil {
		return nil
	}
	return &cloned
}

func trafficProfileForStorage(profile *TrafficProfile) any {
	if profile == nil {
		return nil
	}
	return cloneTrafficProfile(profile)
}

func decodeStoredTrafficProfile(value any) *TrafficProfile {
	if value == nil {
		return nil
	}
	if profile, ok := value.(*TrafficProfile); ok {
		return cloneTrafficProfile(profile)
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" {
		return nil
	}
	var profile TrafficProfile
	if json.Unmarshal(data, &profile) != nil || (profile.Version == 0 && len(profile.Mappings) == 0) {
		return nil
	}
	return &profile
}

func validateTrafficProfile(profile *TrafficProfile, groups []CreateGroupEntry, duration, scenarioMode string, goalConfig *GoalConfig) error {
	if profile == nil {
		return nil
	}
	if profile.Version != trafficProfileVersion {
		return fmt.Errorf("traffic_profile.version must be %d", trafficProfileVersion)
	}
	if err := validateTrafficProfileSource(profile.Source); err != nil {
		return err
	}
	if profile.RefreshedAt.IsZero() {
		return errors.New("traffic_profile.refreshed_at is required")
	}
	if profile.Interpolation != "step" {
		return errors.New("traffic_profile.interpolation must be step")
	}
	if sourceDuration, err := time.ParseDuration(profile.SourceDuration); err != nil || sourceDuration <= 0 {
		return errors.New("traffic_profile.source_duration must be a positive Go duration")
	}
	window, err := trafficProfileWindowSpec(profile.Window)
	if err != nil {
		return err
	}
	switch window.Mode {
	case "", "full":
	case "explicit":
		if window.RelativeOffsets {
			if window.StartOffset < 0 || window.EndOffset <= window.StartOffset {
				return errors.New("traffic_profile explicit offset window end must be after start")
			}
		} else if !window.End.After(window.Start) {
			return errors.New("traffic_profile explicit window end must be after start")
		}
	case "peak":
		if window.Duration <= 0 {
			return errors.New("traffic_profile peak window duration must be positive")
		}
	default:
		return fmt.Errorf("unsupported traffic_profile.window.mode %q", window.Mode)
	}
	if profile.Amplitude.Value < 0 || math.IsNaN(profile.Amplitude.Value) || math.IsInf(profile.Amplitude.Value, 0) {
		return errors.New("traffic_profile.amplitude.value must be finite and nonnegative")
	}
	if profile.Amplitude.Mode != "" && profile.Amplitude.Mode != "factor" && profile.Amplitude.Mode != "peak" {
		return fmt.Errorf("unsupported traffic_profile.amplitude.mode %q", profile.Amplitude.Mode)
	}
	switch trafficprofile.GapPolicy(profile.GapPolicy) {
	case trafficprofile.GapHold, trafficprofile.GapZero, trafficprofile.GapDrop:
	default:
		return errors.New("traffic_profile.gap_policy must be hold, zero, or drop")
	}
	semantics := trafficprofile.ValueSemantics(profile.ValueSemantics)
	expectedTarget := "tps"
	if semantics == trafficprofile.SemanticsVUsers {
		expectedTarget = "vusers"
	}
	switch semantics {
	case trafficprofile.SemanticsTransactionsPerBucket, trafficprofile.SemanticsCumulativeCounter, trafficprofile.SemanticsTPS, trafficprofile.SemanticsVUsers:
	default:
		return fmt.Errorf("unsupported traffic_profile.value_semantics %q", profile.ValueSemantics)
	}
	if profile.RuntimeTarget != expectedTarget {
		return fmt.Errorf("traffic_profile.runtime_target must be %s", expectedTarget)
	}
	profileDuration, err := time.ParseDuration(profile.TargetDuration)
	if err != nil || profileDuration <= 0 {
		return errors.New("traffic_profile.target_duration must be a positive Go duration")
	}
	scenarioDuration, err := time.ParseDuration(duration)
	if err != nil || scenarioDuration != profileDuration {
		return errors.New("scenario duration must equal traffic_profile.target_duration")
	}
	if expectedTarget == "tps" && normalizeScenarioMode(scenarioMode) != "goal_oriented" {
		return errors.New("TPS traffic profiles require scenario_mode goal_oriented")
	}
	if expectedTarget == "tps" && (goalConfig == nil || goalConfig.MinVUsers == nil || goalConfig.MaxVUsers == nil) {
		return errors.New("TPS traffic profiles require goal_config.min_vusers and goal_config.max_vusers guardrails")
	}
	if expectedTarget == "vusers" && normalizeScenarioMode(scenarioMode) != "vuser_driven" {
		return errors.New("VUser traffic profiles require scenario_mode vuser_driven")
	}
	groupByName := make(map[string]CreateGroupEntry, len(groups))
	for _, group := range groups {
		groupByName[normalizeEffectiveGroupName(group.Name)] = group
	}
	if len(profile.Mappings) == 0 {
		return errors.New("traffic_profile requires at least one series mapping")
	}
	seenGroups, totalPoints, totalARTPoints := map[string]struct{}{}, 0, 0
	compositeSource := strings.EqualFold(strings.TrimSpace(profile.Source.Kind), "multiple")
	allowedSources := map[string]struct{}{}
	if compositeSource {
		for _, source := range profile.Source.Sources {
			allowedSources[trafficProfileSourceIdentity(source)] = struct{}{}
		}
	}
	for index := range profile.Mappings {
		mapping := &profile.Mappings[index]
		seriesKey := strings.TrimSpace(mapping.SeriesKey)
		groupKey := normalizeEffectiveGroupName(mapping.GroupName)
		if seriesKey == "" || groupKey == "" {
			return fmt.Errorf("traffic_profile.mappings[%d] requires series_key and group_name", index)
		}
		if _, exists := seenGroups[groupKey]; exists {
			return fmt.Errorf("traffic_profile group %q is mapped more than once", mapping.GroupName)
		}
		group, exists := groupByName[groupKey]
		if !exists {
			return fmt.Errorf("traffic_profile group %q was not found", mapping.GroupName)
		}
		// Runtime scale primitives use the compiled group's exact name. Accept
		// case-insensitive authoring input, then canonicalize it once here so a
		// valid profile cannot fail only when its first boundary executes.
		mapping.GroupName = group.Name
		if compositeSource {
			if mapping.Source == nil {
				return fmt.Errorf("traffic_profile mapping %q requires source provenance for a multiple source profile", mapping.SeriesName)
			}
			if err := validateTrafficProfileSource(mapping.Source.asSource()); err != nil {
				return fmt.Errorf("traffic_profile mapping %q source: %w", mapping.SeriesName, err)
			}
			if _, exists := allowedSources[trafficProfileSourceIdentity(*mapping.Source)]; !exists {
				return fmt.Errorf("traffic_profile mapping %q source is not listed in traffic_profile.source.sources", mapping.SeriesName)
			}
			if sourceDuration, err := time.ParseDuration(mapping.SourceDuration); err != nil || sourceDuration <= 0 {
				return fmt.Errorf("traffic_profile mapping %q source_duration must be a positive Go duration", mapping.SeriesName)
			}
		} else if mapping.Source != nil && trafficProfileSourceIdentity(*mapping.Source) != trafficProfileSourceIdentity(profile.Source) {
			return fmt.Errorf("traffic_profile mapping %q source must match traffic_profile.source", mapping.SeriesName)
		}
		if len(group.Stages) > 0 {
			return fmt.Errorf("traffic_profile group %q cannot also use stages", mapping.GroupName)
		}
		if strings.TrimSpace(group.RampUp) != "" || strings.TrimSpace(group.RampDown) != "" {
			return fmt.Errorf("traffic_profile group %q cannot also use simple ramp settings", mapping.GroupName)
		}
		offset, offsetErr := parseOptionalGroupDuration(group.StartOffset, index, "start_offset")
		if offsetErr != nil || offset > 0 {
			return fmt.Errorf("traffic_profile group %q cannot use a nonzero start_offset", mapping.GroupName)
		}
		if len(mapping.Points) < 2 || len(mapping.Points) > trafficprofile.MaxPointsPerSeries {
			return fmt.Errorf("traffic_profile mapping %q must contain 2..%d points", mapping.SeriesName, trafficprofile.MaxPointsPerSeries)
		}
		previous := int64(-1)
		mapping.PeakTarget = 0
		minimumIntervalMS := int64(1000)
		if expectedTarget == "tps" {
			minimumIntervalMS = 5000
		}
		for pointIndex, point := range mapping.Points {
			if pointIndex == 0 && point.OffsetMS != 0 {
				return fmt.Errorf("traffic_profile mapping %q must begin at offset_ms 0", mapping.SeriesName)
			}
			if point.OffsetMS < 0 || point.OffsetMS <= previous {
				return fmt.Errorf("traffic_profile mapping %q point %d offset_ms must increase", mapping.SeriesName, pointIndex)
			}
			if previous >= 0 && point.OffsetMS-previous < minimumIntervalMS {
				return fmt.Errorf("traffic_profile mapping %q point %d interval is below the %dms minimum", mapping.SeriesName, pointIndex, minimumIntervalMS)
			}
			if point.OffsetMS >= profileDuration.Milliseconds() {
				return fmt.Errorf("traffic_profile mapping %q point %d is outside target duration", mapping.SeriesName, pointIndex)
			}
			if point.Target < 0 || math.IsNaN(point.Target) || math.IsInf(point.Target, 0) {
				return fmt.Errorf("traffic_profile mapping %q point %d target must be finite and nonnegative", mapping.SeriesName, pointIndex)
			}
			if expectedTarget == "vusers" && (point.Target != math.Trunc(point.Target) || point.Target > maxManagedTestVUsers) {
				return fmt.Errorf("traffic_profile mapping %q point %d target_vusers is invalid", mapping.SeriesName, pointIndex)
			}
			mapping.PeakTarget = math.Max(mapping.PeakTarget, point.Target)
			previous = point.OffsetMS
		}
		if profileDuration.Milliseconds()-previous < minimumIntervalMS {
			return fmt.Errorf("traffic_profile mapping %q final interval is below the %dms minimum", mapping.SeriesName, minimumIntervalMS)
		}
		if expectedTarget == "vusers" && group.VUsers != int(mapping.PeakTarget) {
			return fmt.Errorf("traffic_profile group %q vusers must equal its peak target %d", mapping.GroupName, int(mapping.PeakTarget))
		}
		if mapping.ART != nil {
			if expectedTarget != "tps" {
				return fmt.Errorf("traffic_profile mapping %q ART evidence requires a TPS runtime target", mapping.SeriesName)
			}
			if err := validateTrafficProfileART(mapping.ART, profileDuration); err != nil {
				return fmt.Errorf("traffic_profile mapping %q ART: %w", mapping.SeriesName, err)
			}
			artSource := profile.Source
			if mapping.Source != nil {
				artSource = mapping.Source.asSource()
			}
			if mapping.ART.Basis == "source_series" && !strings.EqualFold(strings.TrimSpace(artSource.Kind), "file") {
				return fmt.Errorf("traffic_profile mapping %q file-backed ART requires a file source", mapping.SeriesName)
			}
			mapping.ART.MinARTMS, mapping.ART.AverageARTMS = trafficProfileARTStatistics(mapping.ART, profileDuration)
			mapping.ART.EstimatedPeakVUsers = trafficProfileMappingEstimatedPeak(*mapping)
			totalARTPoints += len(mapping.ART.Points)
		}
		totalPoints += len(mapping.Points)
		seenGroups[groupKey] = struct{}{}
	}
	if totalPoints > trafficprofile.MaxPointsPerProfile {
		return fmt.Errorf("traffic_profile contains %d points; limit is %d", totalPoints, trafficprofile.MaxPointsPerProfile)
	}
	if totalARTPoints > trafficprofile.MaxPointsPerProfile {
		return fmt.Errorf("traffic_profile contains %d ART points; limit is %d", totalARTPoints, trafficprofile.MaxPointsPerProfile)
	}
	completeART := expectedTarget == "tps" && len(profile.Mappings) > 0
	for index := range profile.Mappings {
		if profile.Mappings[index].ART == nil {
			completeART = false
			break
		}
	}
	if completeART {
		profilePeak := trafficProfileEstimatedAggregatePeak(profile.Mappings)
		staticPeak := trafficProfileDraftStaticPeak(groups, profile.Mappings)
		totalPeak := saturatingAddNonNegative(staticPeak, profilePeak)
		suggestedMaximum := staticPeak
		if staticPeak >= maxManagedTestVUsers || profilePeak > (maxManagedTestVUsers-staticPeak)/2 {
			suggestedMaximum = maxManagedTestVUsers
		} else {
			suggestedMaximum += 2 * profilePeak
		}
		suggestedMaximum = min(maxManagedTestVUsers, max(1, suggestedMaximum))
		previousSizing := TrafficProfileSizing{}
		if profile.Sizing == nil {
			profile.Sizing = &TrafficProfileSizing{}
		} else {
			previousSizing = *profile.Sizing
		}
		profile.Sizing.Complete = true
		profile.Sizing.ProfilePeakVUsers = profilePeak
		profile.Sizing.StaticPeakVUsers = staticPeak
		profile.Sizing.EstimatedPeakVUsers = totalPeak
		profile.Sizing.SuggestedMinVUsers = 1
		profile.Sizing.SuggestedMaxVUsers = suggestedMaximum
		if !previousSizing.Complete || previousSizing.ProfilePeakVUsers != profilePeak || previousSizing.StaticPeakVUsers != staticPeak || previousSizing.EstimatedPeakVUsers != totalPeak || previousSizing.SuggestedMinVUsers != 1 || previousSizing.SuggestedMaxVUsers != suggestedMaximum {
			profile.Sizing.RecommendedGenerators = 0
		}
	} else {
		profile.Sizing = nil
	}
	return nil
}

func validateTrafficProfileART(art *TrafficProfileART, duration time.Duration) error {
	if art == nil {
		return nil
	}
	if art.Basis != "source_series" && art.Basis != "operator_peak" {
		return errors.New("basis must be source_series or operator_peak")
	}
	if art.PeakARTMS <= 0 || math.IsNaN(art.PeakARTMS) || math.IsInf(art.PeakARTMS, 0) {
		return errors.New("peak_art_ms must be finite and greater than zero")
	}
	if art.Basis == "source_series" && (strings.TrimSpace(art.SeriesKey) == "" || len(art.Points) == 0) {
		return errors.New("source_series basis requires series_key and points")
	}
	if art.Basis == "operator_peak" && len(art.Points) != 1 {
		return errors.New("operator_peak basis requires one constant ART point")
	}
	if len(art.Points) > trafficprofile.MaxPointsPerSeries {
		return fmt.Errorf("contains more than %d points", trafficprofile.MaxPointsPerSeries)
	}
	previous := int64(-1)
	peak := 0.0
	for index, point := range art.Points {
		if index == 0 && point.OffsetMS != 0 {
			return errors.New("points must begin at offset_ms 0")
		}
		if point.OffsetMS < 0 || point.OffsetMS <= previous || point.OffsetMS >= duration.Milliseconds() {
			return fmt.Errorf("point %d offset_ms must increase within the target duration", index)
		}
		if point.ARTMS <= 0 || math.IsNaN(point.ARTMS) || math.IsInf(point.ARTMS, 0) {
			return fmt.Errorf("point %d art_ms must be finite and greater than zero", index)
		}
		peak = math.Max(peak, point.ARTMS)
		previous = point.OffsetMS
	}
	if math.Abs(peak-art.PeakARTMS) > 1e-6 {
		return errors.New("peak_art_ms must equal the maximum ART point")
	}
	return nil
}

func validateTrafficProfileSource(source TrafficProfileSource) error {
	kind := strings.ToLower(strings.TrimSpace(source.Kind))
	switch kind {
	case "multiple":
		if source.File != nil || source.Prometheus != nil || source.OpenSearch != nil {
			return errors.New("traffic_profile multiple source must contain only source.sources")
		}
		if len(source.Sources) < 2 || len(source.Sources) > 100 {
			return errors.New("traffic_profile multiple source requires 2..100 source entries")
		}
		for index, child := range source.Sources {
			if err := validateTrafficProfileSource(child.asSource()); err != nil {
				return fmt.Errorf("traffic_profile multiple source entry %d: %w", index, err)
			}
		}
	case "file":
		if source.File == nil || source.Prometheus != nil || source.OpenSearch != nil || len(source.Sources) > 0 {
			return errors.New("traffic_profile file source must contain only source.file")
		}
		if strings.TrimSpace(source.File.Filename) == "" || strings.TrimSpace(source.File.TimestampColumn) == "" || len(source.File.ValueColumns) == 0 {
			return errors.New("traffic_profile file source requires filename, timestamp_column, and value_columns")
		}
		timestampMode := strings.ToLower(strings.TrimSpace(source.File.TimestampMode))
		if timestampMode == "" {
			timestampMode = "absolute"
		}
		if timestampMode != "absolute" && timestampMode != "relative" {
			return errors.New("traffic_profile file timestamp_mode must be absolute or relative")
		}
		if source.File.Timezone != "" {
			if _, err := time.LoadLocation(source.File.Timezone); err != nil {
				return errors.New("traffic_profile file timezone is invalid")
			}
		}
		if timestampMode == "relative" {
			if source.File.Timezone == "" {
				return errors.New("traffic_profile relative timestamps require a timezone")
			}
			if _, err := parseTrafficProfileRelativeStartTime(source.File.RelativeStartTime, time.UTC); err != nil {
				return fmt.Errorf("traffic_profile file relative_start_time: %w", err)
			}
		} else if strings.TrimSpace(source.File.RelativeStartTime) != "" {
			return errors.New("traffic_profile file relative_start_time requires timestamp_mode relative")
		}
		seenColumns := make(map[string]struct{}, len(source.File.ValueColumns))
		for _, column := range source.File.ValueColumns {
			if strings.TrimSpace(column) == "" {
				return errors.New("traffic_profile file value_columns cannot contain an empty name")
			}
			key := strings.ToLower(strings.TrimSpace(column))
			if _, exists := seenColumns[key]; exists {
				return errors.New("traffic_profile file value_columns must be unique")
			}
			seenColumns[key] = struct{}{}
		}
		seenARTColumns := make(map[string]struct{}, len(source.File.ARTColumns))
		for _, column := range source.File.ARTColumns {
			key := strings.ToLower(strings.TrimSpace(column))
			if key == "" {
				return errors.New("traffic_profile file art_columns cannot contain an empty name")
			}
			if _, exists := seenARTColumns[key]; exists {
				return errors.New("traffic_profile file art_columns must be unique")
			}
			if _, exists := seenColumns[key]; exists {
				return fmt.Errorf("traffic_profile file column %q cannot be both a workload value and ART", column)
			}
			seenARTColumns[key] = struct{}{}
		}
	case "prometheus":
		if source.Prometheus == nil || source.File != nil || source.OpenSearch != nil || len(source.Sources) > 0 {
			return errors.New("traffic_profile Prometheus source must contain only source.prometheus")
		}
		if strings.TrimSpace(source.Prometheus.Query) == "" {
			return errors.New("traffic_profile Prometheus query is required")
		}
		if err := validateTrafficProfileQueryRange(source.Prometheus.Range); err != nil {
			return fmt.Errorf("traffic_profile Prometheus %w", err)
		}
	case "opensearch":
		if source.OpenSearch == nil || source.File != nil || source.Prometheus != nil || len(source.Sources) > 0 {
			return errors.New("traffic_profile OpenSearch source must contain only source.opensearch")
		}
		value := source.OpenSearch
		if !openSearchIndexPattern.MatchString(strings.TrimSpace(value.Index)) || strings.TrimSpace(value.TimestampField) == "" {
			return errors.New("traffic_profile OpenSearch index and timestamp_field are required")
		}
		interval, err := time.ParseDuration(value.Interval)
		if err != nil || interval <= 0 {
			return errors.New("traffic_profile OpenSearch interval must be a positive duration")
		}
		if err := validateTrafficProfileQueryRange(value.Range); err != nil {
			return fmt.Errorf("traffic_profile OpenSearch %w", err)
		}
		if interval != time.Duration(value.Range.StepSeconds)*time.Second {
			return errors.New("traffic_profile OpenSearch interval must equal range.step_seconds")
		}
		metric := strings.ToLower(strings.TrimSpace(value.Metric))
		if metric != "count" && metric != "sum" && metric != "avg" {
			return errors.New("traffic_profile OpenSearch metric must be count, sum, or avg")
		}
		if metric != "count" && strings.TrimSpace(value.MetricField) == "" {
			return errors.New("traffic_profile OpenSearch metric_field is required for sum or avg")
		}
		if len(value.Filter) > 0 && string(value.Filter) != "null" {
			var filter map[string]any
			if err := json.Unmarshal(value.Filter, &filter); err != nil || filter == nil {
				return errors.New("traffic_profile OpenSearch filter must be a JSON object")
			}
		}
	default:
		return errors.New("traffic_profile.source.kind must be file, prometheus, opensearch, or multiple")
	}
	return nil
}

func (source TrafficProfileSourceEntry) asSource() TrafficProfileSource {
	return TrafficProfileSource{Kind: source.Kind, Name: source.Name, File: source.File, Prometheus: source.Prometheus, OpenSearch: source.OpenSearch}
}

func trafficProfileSourceEntry(source TrafficProfileSource) TrafficProfileSourceEntry {
	return TrafficProfileSourceEntry{Kind: source.Kind, Name: source.Name, File: source.File, Prometheus: source.Prometheus, OpenSearch: source.OpenSearch}
}

func parseTrafficProfileRelativeStartTime(value string, location *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("is required")
	}
	if location == nil {
		return time.Time{}, errors.New("requires a timezone")
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		parsed, err := time.ParseInLocation(layout, value, location)
		if err == nil {
			return time.Date(2000, time.January, 1, parsed.Hour(), parsed.Minute(), parsed.Second(), parsed.Nanosecond(), location), nil
		}
	}
	return time.Time{}, errors.New("must use HH:MM or HH:MM:SS")
}

func trafficProfileSourceIdentity(source any) string {
	data, _ := json.Marshal(source)
	return string(data)
}

func validateTrafficProfileQueryRange(value TrafficProfileQueryRange) error {
	mode := strings.ToLower(strings.TrimSpace(value.Mode))
	if mode != "absolute" && mode != "relative" {
		return errors.New("range.mode must be absolute or relative")
	}
	if value.StepSeconds <= 0 {
		return errors.New("range.step_seconds must be positive")
	}
	if mode == "relative" {
		lookback, err := time.ParseDuration(value.Lookback)
		if err != nil || lookback <= 0 {
			return errors.New("relative range requires a positive lookback")
		}
		return nil
	}
	if value.Start.IsZero() || value.End.IsZero() || !value.End.After(value.Start) {
		return errors.New("absolute range requires end after start")
	}
	return nil
}

func validateTrafficProfileCompiledGroups(profile *TrafficProfile, groups []TestGroup) error {
	if profile == nil || profile.RuntimeTarget != "tps" {
		return nil
	}
	managed := make(map[string]struct{}, len(profile.Mappings))
	for _, mapping := range profile.Mappings {
		managed[normalizeEffectiveGroupName(mapping.GroupName)] = struct{}{}
	}
	for _, group := range groups {
		if _, ok := managed[normalizeEffectiveGroupName(group.Name)]; !ok || group.script == nil {
			continue
		}
		if dsl.CanonicalWorkloadModel(group.script.Config.RunLogic.WorkloadModel) == "open" {
			return fmt.Errorf("traffic-profile TPS group %q cannot use an open-arrival script", group.Name)
		}
	}
	return nil
}

func trafficProfileCapacityVUsers(profile *TrafficProfile, groups []TestGroup, duration time.Duration, goal *GoalConfig) int {
	peak := managedGroupPeakVUsers(groups, duration)
	if profile == nil {
		return peak
	}
	if profile.RuntimeTarget == "tps" {
		if goal != nil && goal.MaxVUsers != nil && *goal.MaxVUsers > peak {
			return *goal.MaxVUsers
		}
		return peak
	}
	if profile.RuntimeTarget != "vusers" {
		return peak
	}
	managed := make(map[string]struct{}, len(profile.Mappings))
	profilePeak := 0
	for _, mapping := range profile.Mappings {
		managed[normalizeEffectiveGroupName(mapping.GroupName)] = struct{}{}
		mappingPeak := mapping.PeakTarget
		for _, point := range mapping.Points {
			mappingPeak = math.Max(mappingPeak, point.Target)
		}
		profilePeak = saturatingAddNonNegative(profilePeak, int(mappingPeak))
	}
	staticGroups := make([]TestGroup, 0, len(groups))
	for _, group := range groups {
		if _, ok := managed[normalizeEffectiveGroupName(group.Name)]; !ok {
			staticGroups = append(staticGroups, group)
		}
	}
	return saturatingAddNonNegative(profilePeak, managedGroupPeakVUsers(staticGroups, duration))
}

func trafficProfileARTAt(art *TrafficProfileART, offsetMS int64) float64 {
	if art == nil || len(art.Points) == 0 || offsetMS < art.Points[0].OffsetMS {
		return 0
	}
	low, high := 0, len(art.Points)-1
	for low < high {
		middle := (low + high + 1) / 2
		if art.Points[middle].OffsetMS <= offsetMS {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return art.Points[low].ARTMS
}

func trafficProfileTargetAt(mapping TrafficProfileMapping, offsetMS int64) float64 {
	if len(mapping.Points) == 0 || offsetMS < mapping.Points[0].OffsetMS {
		return 0
	}
	low, high := 0, len(mapping.Points)-1
	for low < high {
		middle := (low + high + 1) / 2
		if mapping.Points[middle].OffsetMS <= offsetMS {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return mapping.Points[low].Target
}

func trafficProfileEstimatedVUsersAt(mapping TrafficProfileMapping, offsetMS int64) int {
	target, artMS := trafficProfileTargetAt(mapping, offsetMS), trafficProfileARTAt(mapping.ART, offsetMS)
	if target <= 0 || artMS <= 0 {
		return 0
	}
	raw := target * (artMS / 1000)
	if math.IsNaN(raw) || math.IsInf(raw, 0) || raw >= float64(maxCapacityUnit) {
		return maxCapacityUnit
	}
	return littlesLawVUsers(target, artMS)
}

func trafficProfileARTStatistics(art *TrafficProfileART, duration time.Duration) (minimum, average float64) {
	if art == nil || len(art.Points) == 0 || duration <= 0 {
		return 0, 0
	}
	minimum = art.Points[0].ARTMS
	area := 0.0
	durationMS := duration.Milliseconds()
	for index, point := range art.Points {
		minimum = math.Min(minimum, point.ARTMS)
		end := durationMS
		if index+1 < len(art.Points) {
			end = art.Points[index+1].OffsetMS
		}
		area += point.ARTMS * float64(max(int64(0), end-point.OffsetMS))
	}
	return minimum, area / float64(durationMS)
}

func trafficProfileDraftStaticPeak(groups []CreateGroupEntry, mappings []TrafficProfileMapping) int {
	managed := make(map[string]struct{}, len(mappings))
	for _, mapping := range mappings {
		managed[normalizeEffectiveGroupName(mapping.GroupName)] = struct{}{}
	}
	total := 0
	for _, group := range groups {
		if _, exists := managed[normalizeEffectiveGroupName(group.Name)]; exists {
			continue
		}
		peak := max(0, group.VUsers)
		for _, stage := range group.Stages {
			peak = max(peak, stage.TargetVUsers)
		}
		total = saturatingAddNonNegative(total, peak)
	}
	return total
}

func trafficProfileMappingEstimatedPeak(mapping TrafficProfileMapping) int {
	if mapping.ART == nil {
		return 0
	}
	offsets := make(map[int64]struct{}, len(mapping.Points)+len(mapping.ART.Points))
	for _, point := range mapping.Points {
		offsets[point.OffsetMS] = struct{}{}
	}
	for _, point := range mapping.ART.Points {
		offsets[point.OffsetMS] = struct{}{}
	}
	peak := 0
	for offset := range offsets {
		peak = max(peak, trafficProfileEstimatedVUsersAt(mapping, offset))
	}
	return peak
}

func trafficProfileEstimatedAggregatePeak(mappings []TrafficProfileMapping) int {
	offsets := make(map[int64]struct{})
	for _, mapping := range mappings {
		if mapping.ART == nil {
			return 0
		}
		for _, point := range mapping.Points {
			offsets[point.OffsetMS] = struct{}{}
		}
		for _, point := range mapping.ART.Points {
			offsets[point.OffsetMS] = struct{}{}
		}
	}
	peak := 0
	for offset := range offsets {
		total := 0
		for _, mapping := range mappings {
			total = saturatingAddNonNegative(total, trafficProfileEstimatedVUsersAt(mapping, offset))
		}
		peak = max(peak, total)
	}
	return peak
}

func trafficProfilePeakAggregateTarget(profile *TrafficProfile) float64 {
	if profile == nil {
		return 0
	}
	type targetEvent struct {
		offset       int64
		mappingIndex int
		target       float64
	}
	events := make([]targetEvent, 0)
	for mappingIndex, mapping := range profile.Mappings {
		for _, point := range mapping.Points {
			events = append(events, targetEvent{offset: point.OffsetMS, mappingIndex: mappingIndex, target: point.Target})
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].offset < events[j].offset })
	current := make([]float64, len(profile.Mappings))
	aggregate, peak := 0.0, 0.0
	for index := 0; index < len(events); {
		offset := events[index].offset
		for index < len(events) && events[index].offset == offset {
			event := events[index]
			aggregate += event.target - current[event.mappingIndex]
			current[event.mappingIndex] = event.target
			index++
		}
		peak = math.Max(peak, aggregate)
	}
	return peak
}

func applyTrafficProfileInitialTargets(profile *TrafficProfile, groups []TestGroup, engineGroups []engine.GroupConfig) int {
	if profile == nil {
		return capacityVUsersForGroups(groups)
	}
	initialByGroup := make(map[string]float64, len(profile.Mappings))
	for _, mapping := range profile.Mappings {
		if len(mapping.Points) > 0 {
			initialByGroup[normalizeEffectiveGroupName(mapping.GroupName)] = mapping.Points[0].Target
		}
	}
	for index := range groups {
		initial, managed := initialByGroup[normalizeEffectiveGroupName(groups[index].Name)]
		if !managed || (profile.RuntimeTarget == "tps" && initial > 0) {
			continue
		}
		target := int(math.Round(initial))
		groups[index].VUsers = target
		groups[index].initialVUsers = target
		groups[index].currentVUsers = target
		if index < len(engineGroups) {
			engineGroups[index].VUsers = target
			engineGroups[index].RampUp = 0
			engineGroups[index].RampDown = 0
			engineGroups[index].AllowZeroVUsers = target == 0
		}
	}
	return capacityVUsersForGroups(groups)
}
