package api

import (
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/storage"
)

func cloneMonitorBindings(b *storage.MonitorBindings) *storage.MonitorBindings {
	if b == nil {
		return nil
	}
	out := *b
	out.Sources = append([]storage.MonitorBinding{}, b.Sources...)
	return &out
}
func (tr *TestRunner) validateTestMonitorBindings(project string, b *storage.MonitorBindings, goals *GoalConfig) error {
	if goals != nil {
		if len(goals.MonitorRules) > 128 {
			return errors.New("at most 128 monitoring rules are allowed")
		}
		for _, r := range goals.MonitorRules {
			if err := r.Validate(); err != nil {
				return err
			}
		}
	}
	if b == nil {
		return nil
	}
	if len(b.Sources) > storage.MaxRunMonitorSources {
		return errors.New("at most 32 monitors may be selected")
	}
	if b.ProjectID != "" && b.ProjectID != project {
		return errors.New("monitor binding project mismatch")
	}
	if len(b.Sources) == 0 {
		return nil
	}
	if !runMonitoringEnabled(project) {
		return errors.New("run monitoring is not enabled for this project")
	}
	store, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok {
		return errors.New("monitoring storage unavailable")
	}
	sources, err := store.ListMonitorSources(project)
	if err != nil {
		return err
	}
	available := map[string]bool{}
	for _, s := range sources {
		available[s.ID] = true
	}
	seen := map[string]bool{}
	for _, v := range b.Sources {
		if !available[v.SourceID] || seen[v.SourceID] {
			return errors.New("monitor source unavailable or duplicated")
		}
		seen[v.SourceID] = true
	}
	return nil
}
func safeMonitorSource(source storage.MonitorSource) storage.MonitorSource {
	if source.CanonicalProvider() {
		source.CredentialRef = ""
		source.CollectorPublicKey = ""
	}
	if source.Dynatrace != nil {
		selection := *source.Dynatrace
		selection.Cursor = ""
		selection.QueryOffset = 0
		source.Dynatrace = &selection
	}
	return source
}
func safeMonitorJob(job storage.RunMonitorJob) storage.RunMonitorJob {
	job.ConnectionSnapshot = nil
	job.PrometheusConnectionSnapshot = nil
	job.DynatraceSnapshot = nil
	job.ProviderCursor = ""
	job.RunTraceIDs = nil
	job.QueryCoverage = nil
	job.PrometheusDiagnosticTimes = nil
	job.StableSinceAt = nil
	job.Source = safeMonitorSource(job.Source)
	job.FinalDigest = ""
	return job
}
func safePreparation(p *storage.Preparation) *storage.Preparation {
	if p == nil {
		return nil
	}
	raw, _ := json.Marshal(p)
	var out storage.Preparation
	_ = json.Unmarshal(raw, &out)
	if out.Monitors != nil {
		out.Monitors.Connections = nil
		out.Monitors.PrometheusConnections = nil
		out.Monitors.DynatraceConnections = nil
		for id, s := range out.Monitors.Sources {
			out.Monitors.Sources[id] = safeMonitorSource(s)
		}
	}
	return &out
}

func requestMonitorBindings(b *[]storage.MonitorBinding) *storage.MonitorBindings {
	if b == nil {
		return nil
	}
	return &storage.MonitorBindings{Sources: append([]storage.MonitorBinding{}, (*b)...)}
}

func (tr *TestRunner) resolvedTestMonitorBindings(record *storage.TestRecord) (*storage.MonitorBindings, error) {
	if record.MonitorBindings != nil {
		return cloneMonitorBindings(record.MonitorBindings), nil
	}
	store, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok {
		return &storage.MonitorBindings{}, nil
	}
	if record.ScenarioID == "" {
		return &storage.MonitorBindings{Sources: []storage.MonitorBinding{}}, nil
	}
	return store.GetMonitorBindings(record.ProjectID, record.ScenarioID)
}
func (tr *TestRunner) validateMonitorStorageAndSigning(record *storage.TestRecord) error {
	b, err := tr.resolvedTestMonitorBindings(record)
	if err != nil {
		return err
	}
	if len(b.Sources) == 0 {
		return nil
	}
	if err := ensureAnalysisBackend(tr.testStore); err != nil {
		return err
	}
	store, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok {
		return errors.New("monitor storage unavailable")
	}
	sources, err := store.ListMonitorSources(record.ProjectID)
	if err != nil {
		return err
	}
	if err := tr.validateAppDynamicsSharedArtifacts(sources, b); err != nil {
		return err
	}
	for _, binding := range b.Sources {
		for _, source := range sources {
			if source.ID == binding.SourceID && source.Enabled && source.ExecutorMode != "direct" {
				_, err := monitorSigningKey()
				return err
			}
		}
	}
	return nil
}
