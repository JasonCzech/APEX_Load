package analysis

import "github.com/jasonczech/apexload/pkg/monitorprovider"

// Shared contracts live below analysis so the provider registry has no reverse dependency.
type ScalarValue = monitorprovider.ScalarValue
type ProviderLog = monitorprovider.ProviderLog
type ProviderSpan = monitorprovider.ProviderSpan
type ProviderTransaction = monitorprovider.ProviderTransaction
type ProviderEvidence = monitorprovider.ProviderEvidence
type ProviderCoverage = monitorprovider.ProviderCoverage
type ProviderContinuation = monitorprovider.ProviderContinuation
type ProviderProgress = monitorprovider.ProviderProgress

func ValidateResolvedIndices(indices []string) error {
	return monitorprovider.ValidateResolvedIndices(indices)
}
