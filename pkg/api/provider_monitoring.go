package api

import (
	"context"

	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

// providerDescriptor adds a governed provider's controller hooks to its
// static contract. Callers dispatch through it instead of comparing names.
type providerDescriptor struct {
	monitorprovider.Descriptor
	// Ingest commits one batch. Direct workers and collector submissions share it.
	Ingest func(*Server, context.Context, storage.RunMonitorJob, int64, storage.ProviderMonitorBatch, executionLeaseTerm) error
	// CollectDirect runs one controller-side collection pass.
	CollectDirect func(*Server, context.Context, storage.RunMonitorJob)
	// PopulateGrant signs a collector's work; GrantUnavailable is shown when it fails.
	PopulateGrant    func(*Server, context.Context, *collectoragent.RunJob, storage.RunMonitorJob) error
	GrantUnavailable string
	// ContractLabel names the provider in collector contract errors.
	ContractLabel string
	// ValidateSubmission checks a collector batch against its signed grant and
	// returns an HTTP status and message when it must be rejected.
	ValidateSubmission func(grant collectoragent.RunJob, job storage.RunMonitorJob, collector string, batch collectoragent.RunSampleBatch) (int, string)
}

// providerDescriptors is filled in init so the hooks, which reach back into
// the registry, do not form a package initialization cycle.
var providerDescriptors map[string]providerDescriptor

func init() {
	describe := func(pack string, d providerDescriptor) providerDescriptor {
		static, ok := monitorprovider.Lookup(pack)
		if !ok {
			panic("monitor provider registry is missing " + pack)
		}
		d.Descriptor = static
		return d
	}
	providerDescriptors = map[string]providerDescriptor{
		monitorprovider.PackAppDynamics: describe(monitorprovider.PackAppDynamics, providerDescriptor{
			Ingest: (*Server).ingestAppDynamicsBatch, CollectDirect: (*Server).collectDirectAppDynamics,
			PopulateGrant: (*Server).populateAppDynamicsGrant, GrantUnavailable: "AppDynamics credential unavailable", ContractLabel: "AppDynamics",
			ValidateSubmission: validateAppDynamicsSubmission,
		}),
		monitorprovider.PackDynatrace: describe(monitorprovider.PackDynatrace, providerDescriptor{
			Ingest: (*Server).ingestDynatraceBatch, CollectDirect: (*Server).collectDirectDynatrace,
			PopulateGrant: (*Server).populateDynatraceGrant, GrantUnavailable: "Dynatrace credential or quota unavailable", ContractLabel: "Dynatrace",
			ValidateSubmission: validateDynatraceSubmission,
		}),
		monitorprovider.PackPrometheusQuery: describe(monitorprovider.PackPrometheusQuery, providerDescriptor{
			Ingest: (*Server).ingestPrometheusBatch, CollectDirect: (*Server).collectDirectPrometheus,
			PopulateGrant: (*Server).populatePrometheusGrant, GrantUnavailable: "Prometheus executor unavailable", ContractLabel: "Prometheus query",
			ValidateSubmission: validatePrometheusSubmission,
		}),
	}
	for _, pack := range monitorprovider.Packs() {
		if _, ok := providerDescriptors[pack]; !ok {
			panic("controller hooks are missing for monitor provider " + pack)
		}
	}
}

func validateDynatraceSubmission(grant collectoragent.RunJob, job storage.RunMonitorJob, collector string, batch collectoragent.RunSampleBatch) (int, string) {
	if !dynatraceGrantMatchesJob(grant, job, collector) || batch.Sequence < 0 || batch.Sequence > grant.NextSequence+int64(max(1, grant.MaxBatches))-1 {
		return 403, "Dynatrace grant does not match job"
	}
	if !batch.QueryFromUTC.Equal(grant.QueryFromUTC) || !batch.QueryUntilUTC.Equal(grant.QueryUntilUTC) {
		return 400, "Dynatrace query does not match signed window"
	}
	return 0, ""
}

func validateAppDynamicsSubmission(grant collectoragent.RunJob, job storage.RunMonitorJob, collector string, batch collectoragent.RunSampleBatch) (int, string) {
	endpoint := grant.AppDynamics != nil && job.ConnectionSnapshot != nil && grant.AppDynamics.Config.Endpoint == job.ConnectionSnapshot.Config.Endpoint
	return validateWindowedSubmission(grant, job, collector, batch, endpoint)
}

func validatePrometheusSubmission(grant collectoragent.RunJob, job storage.RunMonitorJob, collector string, batch collectoragent.RunSampleBatch) (int, string) {
	endpoint := grant.Prometheus != nil && job.PrometheusConnectionSnapshot != nil && grant.Prometheus.Config.Endpoint == job.PrometheusConnectionSnapshot.Config.Endpoint
	return validateWindowedSubmission(grant, job, collector, batch, endpoint)
}

// validateWindowedSubmission is the grant check AppDynamics and Prometheus
// share; endpoint says whether the grant's endpoint matches the job's snapshot.
func validateWindowedSubmission(grant collectoragent.RunJob, job storage.RunMonitorJob, collector string, batch collectoragent.RunSampleBatch, endpoint bool) (int, string) {
	if job.Source.CollectorID != collector || grant.SourceID != job.Source.ID || grant.SourceRevision != job.Source.Revision || grant.RunID != job.RunID || grant.Pack != job.Source.Pack || !endpoint || batch.Sequence > grant.NextSequence+int64(max(1, grant.MaxBatches))-1 {
		return 403, "provider grant does not match job"
	}
	if batch.Sequence == job.NextSequence && (!batch.QueryFromUTC.Equal(grant.QueryFromUTC) || !batch.QueryUntilUTC.Equal(grant.QueryUntilUTC)) {
		return 400, "query continuation does not match signed window"
	}
	return 0, ""
}

// lookupProvider returns a governed provider's descriptor and hooks.
func lookupProvider(pack string) (providerDescriptor, bool) {
	d, ok := providerDescriptors[pack]
	return d, ok
}

// providerForCapability maps a collector capability such as "dynatrace.v1"
// to its provider.
func providerForCapability(capability string) (providerDescriptor, bool) {
	for _, d := range providerDescriptors {
		if d.CapabilityPack == capability {
			return d, true
		}
	}
	return providerDescriptor{}, false
}

// nativeProviderPack identifies providers using durable canonical monitoring.
// Older point-only collector packs retain their existing ingestion contract.
func nativeProviderPack(pack string) bool {
	return monitorprovider.Canonical(pack)
}
