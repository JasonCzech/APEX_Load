package api

import (
	"context"
	"github.com/jasonczech/apexload/pkg/storage"
	"testing"
)

func TestElasticSecretsParticipateInProjectUsageAndRotation(t *testing.T) {
	server, _, job, _, _ := apiElasticMonitorFixture(t)
	name := "protected-key-reference"
	usage, err := server.projectSecretUsage(job.ProjectID, name)
	if err != nil || usage.ConsumerCount != 2 || usage.ReferenceCount != 2 {
		t.Fatalf("Elastic connection and frozen job missing from credential usage: %+v %v", usage, err)
	}
	kinds := map[string]bool{}
	for _, consumer := range usage.Consumers {
		kinds[consumer.ResourceType] = true
	}
	if !kinds["elastic_connection"] || !kinds["run_monitor_job"] {
		t.Fatal(usage)
	}
	other, err := server.projectSecretUsage("other-project", name)
	if err != nil || other.ConsumerCount != 0 {
		t.Fatal("cross-project consumers", other, err)
	}
	if err := server.invalidateRetainedMonitorSecretPreparations(context.Background(), job.ProjectID, name); err != nil {
		t.Fatal(err)
	}
	connection, err := server.store.(storage.ElasticConnectionStore).GetELKConnection(job.Source.ConnectionID)
	if err != nil || connection.VerifiedRevision != 0 || connection.LastCheckStatus != "credential_changed" {
		t.Fatalf("shared rotation did not invalidate Elastic verification: %+v %v", connection, err)
	}
	frozen, err := server.store.(storage.RunMonitorStore).GetRunMonitorJob(job.ProjectID, job.ID)
	if err != nil || frozen.ElasticConnectionSnapshot.Revision != job.ElasticConnectionSnapshot.Revision || frozen.ElasticConnectionSnapshot.APIKeySecretName != name {
		t.Fatal("rotation mutated active snapshot", frozen, err)
	}
}
