package api

import (
	"context"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func componentCredentialsPresent(c storage.ComponentSecretReferences) bool {
	return c.APIKeySecretName != "" || c.UsernameSecretName != "" || c.BearerTokenSecretName != "" || c.ClientCertificateSecretName != ""
}

func (s *Server) appendElasticSecretUsage(usage projectSecretUsage) (projectSecretUsage, error) {
	store, ok := s.store.(storage.ElasticSecretReferenceStore)
	if !ok {
		return usage, nil
	}
	consumers, err := store.ListElasticSecretConsumers(usage.ProjectID, usage.Name)
	if err != nil {
		return usage, err
	}
	for _, c := range consumers {
		usage.Consumers = append(usage.Consumers, projectSecretConsumer{ScriptID: c.ResourceType + ":" + c.ResourceID, ScriptName: scrubELKText(c.Name), ResourceType: c.ResourceType, ResourceID: c.ResourceID, ReferenceCount: c.ReferenceCount})
		usage.ReferenceCount += c.ReferenceCount
	}
	sort.Slice(usage.Consumers, func(i, j int) bool { return usage.Consumers[i].ScriptID < usage.Consumers[j].ScriptID })
	usage.ConsumerCount = len(usage.Consumers)
	usage.UsageRevision = projectSecretUsageRevision(usage.ProjectID, usage.Name, usage.Consumers)
	return usage, nil
}

func (s *Server) invalidateElasticSecretPreparations(_ context.Context, project, name string) error {
	store, ok := s.store.(storage.ElasticSecretReferenceStore)
	if !ok {
		return nil
	}
	preparations, err := store.InvalidateElasticSecretPreparations(project, name, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, p := range preparations {
		if s.runner != nil {
			s.runner.cancelPreparationExecution(p.TestID, p.ID)
		}
	}
	return nil
}
