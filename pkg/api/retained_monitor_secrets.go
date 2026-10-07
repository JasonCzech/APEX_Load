package api

import (
	"context"

	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) appendRetainedMonitorSecretUsage(usage projectSecretUsage) (projectSecretUsage, error) {
	var consumers []storage.MonitorSecretConsumer
	if store, ok := s.store.(storage.RetainedMonitorSecretReferenceStore); ok {
		var err error
		consumers, err = store.ListMonitorSecretConsumers(usage.ProjectID, usage.Name)
		if err != nil {
			return usage, err
		}
	}
	if dynatraceStore, ok := s.store.(storage.DynatraceSecretReferenceStore); ok {
		dynatraceConsumers, e := dynatraceStore.ListDynatraceSecretConsumers(usage.ProjectID, usage.Name)
		if e != nil {
			return usage, e
		}
		consumers = append(consumers, dynatraceConsumers...)
	}
	for _, consumer := range consumers {
		usage.Consumers = append(usage.Consumers, projectSecretConsumer{ScriptID: consumer.ResourceType + ":" + consumer.ResourceID, ScriptName: scrubELKText(consumer.Name), ResourceType: consumer.ResourceType, ResourceID: consumer.ResourceID, ReferenceCount: consumer.ReferenceCount})
		usage.ReferenceCount += consumer.ReferenceCount
	}
	sort.Slice(usage.Consumers, func(i, j int) bool {
		if usage.Consumers[i].ScriptName == usage.Consumers[j].ScriptName {
			return usage.Consumers[i].ScriptID < usage.Consumers[j].ScriptID
		}
		return usage.Consumers[i].ScriptName < usage.Consumers[j].ScriptName
	})
	usage.ConsumerCount = len(usage.Consumers)
	usage.UsageRevision = projectSecretUsageRevision(usage.ProjectID, usage.Name, usage.Consumers)
	return usage, nil
}

func (s *Server) invalidateRetainedMonitorSecretPreparations(_ context.Context, project, name string) error {
	var preparations []storage.Preparation
	if store, ok := s.store.(storage.RetainedMonitorSecretReferenceStore); ok {
		var err error
		preparations, err = store.InvalidateRetainedMonitorSecretPreparations(project, name, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	if dynatraceStore, ok := s.store.(storage.DynatraceSecretReferenceStore); ok {
		additional, e := dynatraceStore.InvalidateDynatraceSecretPreparations(project, name, time.Now().UTC())
		if e != nil {
			return e
		}
		preparations = append(preparations, additional...)
	}
	for _, preparation := range preparations {
		if s.runner != nil {
			s.runner.cancelPreparationExecution(preparation.TestID, preparation.ID)
		}
	}
	if err := s.invalidateElasticSecretPreparations(context.Background(), project, name); err != nil {
		return err
	}
	return nil
}
