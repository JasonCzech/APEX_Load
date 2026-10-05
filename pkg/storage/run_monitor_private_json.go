package storage

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"time"
)

// Internal snapshots survive restarts while the exported type/schema excludes
// credential references. Public handlers explicitly remove private state.
type monitorJobJSON RunMonitorJob

func (j RunMonitorJob) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		monitorJobJSON
		Connection                *AppDynamicsConnectionSnapshot                  `json:"connection_snapshot,omitempty"`
		PrometheusConnection      *PrometheusConnectionSnapshot                   `json:"prometheus_connection_snapshot,omitempty"`
		QueryCoverage             map[string]appdynamics.Coverage                 `json:"query_coverage,omitempty"`
		PrometheusDiagnosticTimes map[string]PrometheusDiagnosticObservationTimes `json:"prometheus_diagnostic_times,omitempty"`
		StableSinceAt             *time.Time                                      `json:"stable_since_at,omitempty"`
	}{monitorJobJSON(j), j.ConnectionSnapshot, j.PrometheusConnectionSnapshot, j.QueryCoverage, j.PrometheusDiagnosticTimes, j.StableSinceAt})
}
func (j *RunMonitorJob) UnmarshalJSON(body []byte) error {
	out := struct {
		*monitorJobJSON
		Connection                *AppDynamicsConnectionSnapshot                  `json:"connection_snapshot,omitempty"`
		PrometheusConnection      *PrometheusConnectionSnapshot                   `json:"prometheus_connection_snapshot,omitempty"`
		QueryCoverage             map[string]appdynamics.Coverage                 `json:"query_coverage,omitempty"`
		PrometheusDiagnosticTimes map[string]PrometheusDiagnosticObservationTimes `json:"prometheus_diagnostic_times,omitempty"`
		StableSinceAt             *time.Time                                      `json:"stable_since_at,omitempty"`
	}{monitorJobJSON: (*monitorJobJSON)(j)}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	j.ConnectionSnapshot = out.Connection
	j.PrometheusConnectionSnapshot = out.PrometheusConnection
	j.QueryCoverage = out.QueryCoverage
	j.PrometheusDiagnosticTimes = out.PrometheusDiagnosticTimes
	j.StableSinceAt = out.StableSinceAt
	return nil
}

type preparationMonitorJSON PreparationMonitorSnapshot

func (p PreparationMonitorSnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		preparationMonitorJSON
		Connections           map[string]*AppDynamicsConnectionSnapshot `json:"connections,omitempty"`
		PrometheusConnections map[string]*PrometheusConnectionSnapshot  `json:"prometheus_connections,omitempty"`
	}{preparationMonitorJSON(p), p.Connections, p.PrometheusConnections})
}
func (p *PreparationMonitorSnapshot) UnmarshalJSON(body []byte) error {
	out := struct {
		*preparationMonitorJSON
		Connections           map[string]*AppDynamicsConnectionSnapshot `json:"connections,omitempty"`
		PrometheusConnections map[string]*PrometheusConnectionSnapshot  `json:"prometheus_connections,omitempty"`
	}{preparationMonitorJSON: (*preparationMonitorJSON)(p)}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	p.Connections = out.Connections
	p.PrometheusConnections = out.PrometheusConnections
	return nil
}
