package monitorprovider

import "sort"

// Source packs of the governed providers. Code outside this registry compares
// against these constants or asks a Descriptor, never the literal names.
const (
	PackAppDynamics     = "appdynamics"
	PackDynatrace       = "dynatrace"
	PackPrometheusQuery = "prometheus-query"
)

// Provider names carried on metric points, diagnostics and monitor rules.
// Prometheus queries report as "prometheus".
const (
	ProviderAppDynamics = "appdynamics"
	ProviderDynatrace   = "dynatrace"
	ProviderPrometheus  = "prometheus"
)

// Scheduling says how a provider's collection advances.
const (
	// SchedulingPolled evaluates queries at a fixed interval.
	SchedulingPolled = "polled"
	// SchedulingWindowed collects closed time windows behind the clock.
	SchedulingWindowed = "windowed"
	// SchedulingCursor resumes a provider cursor under scan budgets.
	SchedulingCursor = "cursor"
)

// Observation says what one collected value describes.
const (
	// ObservationInterval values summarize a bounded bucket.
	ObservationInterval = "interval"
	// ObservationCumulative values accumulate from an origin; deltas need care.
	ObservationCumulative = "cumulative"
	// ObservationSnapshot values are instantaneous evaluations.
	ObservationSnapshot = "snapshot"
)

// Descriptor is one governed provider's static contract. Each provider keeps
// its own scheduling, ingestion, validation and observation semantics; the
// descriptor names them so callers can dispatch without comparing names.
type Descriptor struct {
	Pack         string
	ProviderName string
	Label        string
	// CapabilityPack is what a collector must advertise to run the provider.
	CapabilityPack string
	// ContractUnavailableReason is the coverage reason when it does not.
	ContractUnavailableReason string
	Scheduling                string
	Observation               string
	DefaultIntervalSeconds    int
	MinIntervalSeconds        int
	MaxIntervalSeconds        int
	// DefaultExecutorMode applies when a source leaves the executor unset.
	DefaultExecutorMode string
	// CollectorCredentialKey means collectors need the credential key to
	// decrypt the provider's grant.
	CollectorCredentialKey bool
	SupportsTraceLookup    bool
	SupportsDiagnostics    bool
	SupportsRunHeader      bool
}

var descriptors = map[string]Descriptor{
	PackAppDynamics: {
		Pack: PackAppDynamics, ProviderName: ProviderAppDynamics, Label: "AppDynamics",
		CapabilityPack: "appdynamics.v1", ContractUnavailableReason: "collector_appdynamics_contract_unavailable",
		Scheduling: SchedulingWindowed, Observation: ObservationInterval,
		DefaultIntervalSeconds: 60, MinIntervalSeconds: 60, MaxIntervalSeconds: 3600,
		CollectorCredentialKey: true, SupportsDiagnostics: true, SupportsRunHeader: true,
	},
	PackDynatrace: {
		Pack: PackDynatrace, ProviderName: ProviderDynatrace, Label: "Dynatrace",
		CapabilityPack: "dynatrace.v1", ContractUnavailableReason: "collector_dynatrace_contract_unavailable",
		Scheduling: SchedulingCursor, Observation: ObservationInterval,
		DefaultIntervalSeconds: 60, MinIntervalSeconds: 60, MaxIntervalSeconds: 3600, DefaultExecutorMode: "direct",
		CollectorCredentialKey: true, SupportsTraceLookup: true, SupportsDiagnostics: true, SupportsRunHeader: true,
	},
	PackPrometheusQuery: {
		Pack: PackPrometheusQuery, ProviderName: ProviderPrometheus, Label: "Prometheus",
		CapabilityPack: "prometheus-query.v1", ContractUnavailableReason: "collector_prometheus_contract_unavailable",
		Scheduling: SchedulingPolled, Observation: ObservationSnapshot,
		DefaultIntervalSeconds: 30, MinIntervalSeconds: 15, MaxIntervalSeconds: 3600,
		SupportsDiagnostics: true,
	},
}

// Lookup returns the governed provider for a source pack.
func Lookup(pack string) (Descriptor, bool) {
	d, ok := descriptors[pack]
	return d, ok
}

// Canonical reports whether a pack uses durable canonical monitoring: query
// progress and revisions are reconciled. Point-only packs keep their contract.
func Canonical(pack string) bool {
	_, ok := descriptors[pack]
	return ok
}

// IsCapabilityPack reports whether name is a governed provider's collector
// capability, such as "dynatrace.v1".
func IsCapabilityPack(name string) bool {
	for _, d := range descriptors {
		if d.CapabilityPack == name {
			return true
		}
	}
	return false
}

// ByProviderName returns the governed provider that reports under name.
func ByProviderName(name string) (Descriptor, bool) {
	for _, d := range descriptors {
		if d.ProviderName == name {
			return d, true
		}
	}
	return Descriptor{}, false
}

// KnownProviderName reports whether points or rules may carry this provider name.
func KnownProviderName(name string) bool {
	_, ok := ByProviderName(name)
	return ok
}

// Packs lists the governed provider packs in a stable order.
func Packs() []string {
	out := make([]string, 0, len(descriptors))
	for pack := range descriptors {
		out = append(out, pack)
	}
	sort.Strings(out)
	return out
}
