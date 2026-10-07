package analysis

import "github.com/jasonczech/apexload/pkg/monitorprovider"

// Aliases preserve existing provider wire names on shared canonical contracts.
type ProviderEntity = monitorprovider.ProviderEntity
type ProviderDiagnostic = monitorprovider.ProviderEvidence
type ProviderQueryProgress = monitorprovider.ProviderProgress
type ProviderEvaluationWindow = monitorprovider.ProviderEvaluationWindow
type ProviderScanSettlement = monitorprovider.ProviderScanSettlement
