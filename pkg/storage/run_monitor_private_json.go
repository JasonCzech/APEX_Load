package storage

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"

	"time"
)

// Internal snapshots survive restarts while the exported type/schema excludes
// credential references. Public handlers explicitly remove private state.
type monitorJobJSON RunMonitorJob

func (j RunMonitorJob) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		monitorJobJSON
		ElasticResolvedIndices           []string                                        `json:"elastic_resolved_indices,omitempty"`
		ElasticPendingIngestionWatermark time.Time                                       `json:"pending_ingestion_watermark,omitempty,omitzero"`
		ElasticRetiredContinuations      []*analysis.ProviderContinuation                `json:"retired_continuations,omitempty"`
		TruncatedQueries                 map[string]bool                                 `json:"truncated_queries,omitempty"`
		ElasticConnection                *ExpandedElasticConnectionSnapshot              `json:"elastic_connection_snapshot,omitempty"`
		ElasticContinuation              *analysis.ProviderContinuation                  `json:"continuation,omitempty"`
		RetainedEvidenceBytes            int64                                           `json:"retained_evidence_bytes,omitempty"`
		AcceptedArtifactBytes            int64                                           `json:"accepted_artifact_bytes,omitempty"`
		Connection                       *AppDynamicsConnectionSnapshot                  `json:"connection_snapshot,omitempty"`
		PrometheusConnection             *PrometheusConnectionSnapshot                   `json:"prometheus_connection_snapshot,omitempty"`
		QueryCoverage                    map[string]monitorprovider.Coverage             `json:"query_coverage,omitempty"`
		PrometheusDiagnosticTimes        map[string]PrometheusDiagnosticObservationTimes `json:"prometheus_diagnostic_times,omitempty"`
		StableSinceAt                    *time.Time                                      `json:"stable_since_at,omitempty"`
		Dynatrace                        *DynatraceConnectionSnapshot                    `json:"dynatrace_snapshot,omitempty"`
		Cursor                           string                                          `json:"provider_cursor,omitempty"`
		RunTraceIDs                      []string                                        `json:"run_trace_ids,omitempty"`
		Elastic                          *ElasticConnectionSnapshot                      `json:"elastic_snapshot,omitempty"`
	}{monitorJobJSON(j), j.ElasticResolvedIndices, j.ElasticPendingIngestionWatermark, j.ElasticRetiredContinuations, j.TruncatedQueries, j.ElasticConnectionSnapshot, j.ElasticContinuation, j.RetainedEvidenceBytes, j.AcceptedArtifactBytes, j.ConnectionSnapshot, j.PrometheusConnectionSnapshot, j.QueryCoverage, j.PrometheusDiagnosticTimes, j.StableSinceAt, j.DynatraceSnapshot, j.ProviderCursor, j.RunTraceIDs, j.ElasticSnapshot})
}
func (j *RunMonitorJob) UnmarshalJSON(body []byte) error {
	out := struct {
		*monitorJobJSON
		ElasticResolvedIndices           []string                                        `json:"elastic_resolved_indices,omitempty"`
		ElasticPendingIngestionWatermark time.Time                                       `json:"pending_ingestion_watermark,omitempty,omitzero"`
		ElasticRetiredContinuations      []*analysis.ProviderContinuation                `json:"retired_continuations,omitempty"`
		TruncatedQueries                 map[string]bool                                 `json:"truncated_queries,omitempty"`
		ElasticConnection                *ExpandedElasticConnectionSnapshot              `json:"elastic_connection_snapshot,omitempty"`
		ElasticContinuation              *analysis.ProviderContinuation                  `json:"continuation,omitempty"`
		RetainedEvidenceBytes            int64                                           `json:"retained_evidence_bytes,omitempty"`
		AcceptedArtifactBytes            int64                                           `json:"accepted_artifact_bytes,omitempty"`
		Connection                       *AppDynamicsConnectionSnapshot                  `json:"connection_snapshot,omitempty"`
		PrometheusConnection             *PrometheusConnectionSnapshot                   `json:"prometheus_connection_snapshot,omitempty"`
		QueryCoverage                    map[string]monitorprovider.Coverage             `json:"query_coverage,omitempty"`
		PrometheusDiagnosticTimes        map[string]PrometheusDiagnosticObservationTimes `json:"prometheus_diagnostic_times,omitempty"`
		StableSinceAt                    *time.Time                                      `json:"stable_since_at,omitempty"`
		Dynatrace                        *DynatraceConnectionSnapshot                    `json:"dynatrace_snapshot,omitempty"`
		Cursor                           string                                          `json:"provider_cursor,omitempty"`
		RunTraceIDs                      []string                                        `json:"run_trace_ids,omitempty"`
		Elastic                          *ElasticConnectionSnapshot                      `json:"elastic_snapshot,omitempty"`
	}{monitorJobJSON: (*monitorJobJSON)(j)}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	j.ElasticRetiredContinuations = out.ElasticRetiredContinuations
	j.ElasticResolvedIndices = out.ElasticResolvedIndices
	j.ElasticPendingIngestionWatermark = out.ElasticPendingIngestionWatermark
	j.TruncatedQueries = out.TruncatedQueries
	j.ElasticConnectionSnapshot = out.ElasticConnection
	j.ElasticContinuation = out.ElasticContinuation
	j.RetainedEvidenceBytes = out.RetainedEvidenceBytes
	j.AcceptedArtifactBytes = out.AcceptedArtifactBytes
	j.ConnectionSnapshot = out.Connection
	j.PrometheusConnectionSnapshot = out.PrometheusConnection
	j.QueryCoverage = out.QueryCoverage
	j.PrometheusDiagnosticTimes = out.PrometheusDiagnosticTimes
	j.StableSinceAt = out.StableSinceAt
	j.DynatraceSnapshot = out.Dynatrace
	j.ProviderCursor = out.Cursor
	j.RunTraceIDs = out.RunTraceIDs
	j.ElasticSnapshot = out.Elastic
	return nil
}

type preparationMonitorJSON PreparationMonitorSnapshot

func (p PreparationMonitorSnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		preparationMonitorJSON
		ExpandedElasticConnections map[string]*ExpandedElasticConnectionSnapshot `json:"expanded_elastic_connections,omitempty"`
		Connections                map[string]*AppDynamicsConnectionSnapshot     `json:"connections,omitempty"`
		PrometheusConnections      map[string]*PrometheusConnectionSnapshot      `json:"prometheus_connections,omitempty"`
		DynatraceConnections       map[string]*DynatraceConnectionSnapshot       `json:"dynatrace_connections,omitempty"`
		ElasticConnections         map[string]*ElasticConnectionSnapshot         `json:"elastic_connections,omitempty"`
	}{preparationMonitorJSON(p), p.ExpandedElasticConnections, p.Connections, p.PrometheusConnections, p.DynatraceConnections, p.ElasticConnections})
}
func (p *PreparationMonitorSnapshot) UnmarshalJSON(body []byte) error {
	out := struct {
		*preparationMonitorJSON
		ExpandedElasticConnections map[string]*ExpandedElasticConnectionSnapshot `json:"expanded_elastic_connections,omitempty"`
		Connections                map[string]*AppDynamicsConnectionSnapshot     `json:"connections,omitempty"`
		PrometheusConnections      map[string]*PrometheusConnectionSnapshot      `json:"prometheus_connections,omitempty"`
		DynatraceConnections       map[string]*DynatraceConnectionSnapshot       `json:"dynatrace_connections,omitempty"`
		ElasticConnections         map[string]*ElasticConnectionSnapshot         `json:"elastic_connections,omitempty"`
	}{preparationMonitorJSON: (*preparationMonitorJSON)(p)}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	p.ExpandedElasticConnections = out.ExpandedElasticConnections
	p.Connections = out.Connections
	p.PrometheusConnections = out.PrometheusConnections
	p.DynatraceConnections = out.DynatraceConnections
	p.ElasticConnections = out.ElasticConnections
	return nil
}
