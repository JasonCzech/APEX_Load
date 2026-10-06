package storage

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"time"
)

// Internal snapshots survive restarts while the exported type/schema excludes
// credential references. Public handlers explicitly remove private state.
type monitorJobJSON RunMonitorJob

func (j RunMonitorJob) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		monitorJobJSON
		ElasticResolvedIndices           []string                         `json:"elastic_resolved_indices,omitempty"`
		ElasticPendingIngestionWatermark time.Time                        `json:"pending_ingestion_watermark,omitempty,omitzero"`
		ElasticRetiredContinuations      []*analysis.ProviderContinuation `json:"retired_continuations,omitempty"`
		TruncatedQueries                 map[string]bool                  `json:"truncated_queries,omitempty"`
		ElasticConnection                *ElasticConnectionSnapshot       `json:"elastic_connection_snapshot,omitempty"`
		ElasticContinuation              *analysis.ProviderContinuation   `json:"continuation,omitempty"`
		RetainedEvidenceBytes            int64                            `json:"retained_evidence_bytes,omitempty"`
		AcceptedArtifactBytes            int64                            `json:"accepted_artifact_bytes,omitempty"`
		Connection                       *AppDynamicsConnectionSnapshot   `json:"connection_snapshot,omitempty"`
		QueryCoverage                    map[string]appdynamics.Coverage  `json:"query_coverage,omitempty"`
		StableSinceAt                    *time.Time                       `json:"stable_since_at,omitempty"`
	}{monitorJobJSON(j), j.ElasticResolvedIndices, j.ElasticPendingIngestionWatermark, j.ElasticRetiredContinuations, j.TruncatedQueries, j.ElasticConnectionSnapshot, j.ElasticContinuation, j.RetainedEvidenceBytes, j.AcceptedArtifactBytes, j.ConnectionSnapshot, j.QueryCoverage, j.StableSinceAt})
}
func (j *RunMonitorJob) UnmarshalJSON(body []byte) error {
	out := struct {
		*monitorJobJSON
		ElasticResolvedIndices           []string                         `json:"elastic_resolved_indices,omitempty"`
		ElasticPendingIngestionWatermark time.Time                        `json:"pending_ingestion_watermark,omitempty,omitzero"`
		ElasticRetiredContinuations      []*analysis.ProviderContinuation `json:"retired_continuations,omitempty"`
		TruncatedQueries                 map[string]bool                  `json:"truncated_queries,omitempty"`
		ElasticConnection                *ElasticConnectionSnapshot       `json:"elastic_connection_snapshot,omitempty"`
		ElasticContinuation              *analysis.ProviderContinuation   `json:"continuation,omitempty"`
		RetainedEvidenceBytes            int64                            `json:"retained_evidence_bytes,omitempty"`
		AcceptedArtifactBytes            int64                            `json:"accepted_artifact_bytes,omitempty"`
		Connection                       *AppDynamicsConnectionSnapshot   `json:"connection_snapshot,omitempty"`
		QueryCoverage                    map[string]appdynamics.Coverage  `json:"query_coverage,omitempty"`
		StableSinceAt                    *time.Time                       `json:"stable_since_at,omitempty"`
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
	j.QueryCoverage = out.QueryCoverage
	j.StableSinceAt = out.StableSinceAt
	return nil
}

type preparationMonitorJSON PreparationMonitorSnapshot

func (p PreparationMonitorSnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		preparationMonitorJSON
		ElasticConnections map[string]*ElasticConnectionSnapshot     `json:"elastic_connections,omitempty"`
		Connections        map[string]*AppDynamicsConnectionSnapshot `json:"connections,omitempty"`
	}{preparationMonitorJSON(p), p.ElasticConnections, p.Connections})
}
func (p *PreparationMonitorSnapshot) UnmarshalJSON(body []byte) error {
	out := struct {
		*preparationMonitorJSON
		ElasticConnections map[string]*ElasticConnectionSnapshot     `json:"elastic_connections,omitempty"`
		Connections        map[string]*AppDynamicsConnectionSnapshot `json:"connections,omitempty"`
	}{preparationMonitorJSON: (*preparationMonitorJSON)(p)}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	p.ElasticConnections = out.ElasticConnections
	p.Connections = out.Connections
	return nil
}
