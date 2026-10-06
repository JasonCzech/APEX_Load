package api

import (
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"

	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) testUsesElasticTraceEvidence(test *ManagedTest) bool {
	if tr == nil || test == nil {
		return false
	}
	store, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok {
		return false
	}
	tr.mu.RLock()
	record := &storage.TestRecord{ID: test.ID, ProjectID: test.ProjectID, ScenarioID: test.ScenarioID, MonitorBindings: cloneMonitorBindings(test.MonitorBindings)}
	tr.mu.RUnlock()
	usesTrace := func(sources []storage.MonitorSource) bool {
		for _, source := range sources {
			if source.Pack == "elastic" && source.Elastic != nil && source.Elastic.Defaults().TraceIDField != "" {
				return true
			}
		}
		return false
	}
	// Jobs and consumed/ready preparation sources are immutable launch inputs.
	// A later binding/source edit or feature disable cannot change an active run.
	jobs, err := store.ListRunMonitorJobs(record.ProjectID, record.ID)
	if err != nil {
		return false
	}
	if len(jobs) > 0 {
		sources := make([]storage.MonitorSource, 0, len(jobs))
		for _, job := range jobs {
			sources = append(sources, job.Source)
		}
		return usesTrace(sources)
	}
	if preparations, ok := tr.testStore.(storage.PreparationStore); ok {
		preparation, err := preparations.LatestPreparation(record.ID)
		if err == nil && preparation != nil && preparation.Monitors != nil && (preparation.State == "ready" || preparation.State == "consumed") {
			sources := make([]storage.MonitorSource, 0, len(preparation.Monitors.Sources))
			for _, source := range preparation.Monitors.Sources {
				sources = append(sources, source)
			}
			return usesTrace(sources)
		}
	}
	if !runMonitoringEnabled(record.ProjectID) {
		return false
	}
	bindings, err := tr.resolvedTestMonitorBindings(record)
	if err != nil || bindings == nil {
		return false
	}
	all, err := store.ListMonitorSources(record.ProjectID)
	if err != nil {
		return false
	}
	sources := []storage.MonitorSource{}
	for _, binding := range bindings.Sources {
		for _, source := range all {
			if source.ID == binding.SourceID && source.Enabled {
				sources = append(sources, source)
			}
		}
	}
	return usesTrace(sources)
}

// These identifiers only classify retained evidence. They never narrow the
// approved query or turn time association into a claim of exhaustive traces.
func (s *Server) elasticObservedTraceIDs(run string) ([]string, bool) {
	if s.runner == nil {
		return nil, false
	}
	s.runner.mu.RLock()
	test := s.runner.tests[run]
	var artifacts *runArtifacts
	if test != nil {
		artifacts = test.elkArtifacts
	}
	s.runner.mu.RUnlock()
	if artifacts == nil {
		return nil, false
	}
	artifacts.elkMu.Lock()
	ids := make([]string, 0, len(artifacts.elkTraceIDs))
	for id := range artifacts.elkTraceIDs {
		ids = append(ids, id)
	}
	limited := artifacts.elasticTraceLimitReached
	artifacts.elkMu.Unlock()
	sort.Strings(ids)
	return ids, limited
}

func (a *runArtifacts) observeElasticTraceID(id string) {
	id = strings.ToLower(strings.TrimSpace(id))
	if a == nil || !a.elasticTraceEnabled || !protocol.ValidTraceID(id) {
		return
	}
	a.elkMu.Lock()
	defer a.elkMu.Unlock()
	if _, exists := a.elkTraceIDs[id]; exists {
		return
	}
	if len(a.elkTraceIDs) >= 64 {
		a.elasticTraceLimitReached = true
		return
	}
	if a.elkTraceIDs == nil {
		a.elkTraceIDs = map[string]struct{}{}
	}
	a.elkTraceIDs[id] = struct{}{}
}
