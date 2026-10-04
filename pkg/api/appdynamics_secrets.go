package api

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func validateAppDynamicsSecretReferences(connection *storage.AppDynamicsConnection) error {
	for _, name := range []string{connection.ClientIDSecretName, connection.ClientSecretSecretName, connection.UsernameSecretName, connection.PasswordSecretName, connection.EventsAPIKeySecretName} {
		if name != "" {
			if _, err := normalizeProjectSecretName(name); err != nil {
				return errors.New("AppDynamics credentials require valid logical project secret names")
			}
		}
	}
	return nil
}

func (s *Server) appendAppDynamicsSecretUsage(usage projectSecretUsage) (projectSecretUsage, error) {
	store, ok := s.store.(storage.AppDynamicsSecretReferenceStore)
	if !ok {
		return usage, nil
	}
	consumers, err := store.ListAppDynamicsSecretConsumers(usage.ProjectID, usage.Name)
	if err != nil {
		return usage, err
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

func (s *Server) invalidateAppDynamicsSecretPreparations(_ context.Context, project, name string) error {
	store, ok := s.store.(storage.AppDynamicsSecretReferenceStore)
	if !ok {
		return nil
	}
	preparations, err := store.InvalidateAppDynamicsSecretPreparations(project, name, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, preparation := range preparations {
		if s.runner != nil {
			s.runner.cancelPreparationExecution(preparation.TestID, preparation.ID)
		}
	}
	return nil
}
