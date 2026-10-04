package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jasonczech/apexload/pkg/engine"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// RunTraceContextSettings opts a run into W3C trace context and Dynatrace
// transaction markers on operator-approved origins. It is controller-owned run
// configuration, never derived from script data.
type RunTraceContextSettings struct {
	// Enabled sends generated traceparent headers to the approved origins,
	// gives every top-level transaction instance its own trace, and strips
	// generated context on every other hop. Authored headers are untouched.
	Enabled bool `json:"enabled"`
	// TransactionMarker adds X-Dynatrace-Test (VUser, transaction, script and
	// test names) on the approved origins.
	TransactionMarker bool `json:"transaction_marker,omitempty"`
	// Origins are scheme://host[:port] values. They are shared with any
	// AppDynamics run-header origins bound to the run.
	Origins []string `json:"origins,omitempty"`
}

// normalizeRunTraceContext validates a requested setting and returns its
// canonical form; nil when nothing is enabled.
func normalizeRunTraceContext(requested *RunTraceContextSettings) (*RunTraceContextSettings, error) {
	if requested == nil || (!requested.Enabled && !requested.TransactionMarker) {
		return nil, nil
	}
	origins := make([]string, 0, len(requested.Origins))
	for _, origin := range requested.Origins {
		origin = strings.TrimSuffix(strings.TrimSpace(origin), "/")
		if origin == "" {
			continue
		}
		if !slices.Contains(origins, origin) {
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 {
		return nil, errors.New("trace context needs at least one approved origin")
	}
	slices.Sort(origins)
	carriers := runTraceContextCarriers(&RunTraceContextSettings{Enabled: requested.Enabled, TransactionMarker: requested.TransactionMarker})
	// Reuse the protocol's own origin rules so the controller can never accept
	// an origin a generator would reject.
	if err := httpprotocol.ValidateManagedRunCorrelation("trace-context-validation", origins, carriers); err != nil {
		return nil, fmt.Errorf("trace context: %w", err)
	}
	return &RunTraceContextSettings{Enabled: requested.Enabled, TransactionMarker: requested.TransactionMarker, Origins: origins}, nil
}

func cloneRunTraceContext(settings *RunTraceContextSettings) *RunTraceContextSettings {
	if settings == nil {
		return nil
	}
	clone := *settings
	clone.Origins = append([]string(nil), settings.Origins...)
	return &clone
}

func runTraceContextCarriers(settings *RunTraceContextSettings) []string {
	if settings == nil {
		return nil
	}
	var carriers []string
	if settings.Enabled {
		carriers = append(carriers, engine.CorrelationCarrierTraceparent)
	}
	if settings.TransactionMarker {
		carriers = append(carriers, engine.CorrelationCarrierTransactionMarker)
	}
	return carriers
}

// runCorrelation is what one run may add at its approved origins.
type runCorrelation struct {
	Origins  []string
	Carriers []string
	TestName string
}

// requiresPolicyV2 reports whether generators must understand carriers beyond
// the run marker.
func (c runCorrelation) requiresPolicyV2() bool {
	for _, carrier := range c.Carriers {
		if carrier != engine.CorrelationCarrierRunMarker {
			return true
		}
	}
	return false
}

// combineRunCorrelation merges AppDynamics run-header origins with the run's
// trace-context setting. Approved origins are shared by every carrier. A run
// with only run-header origins keeps the carrier-free form older generators
// understand.
func combineRunCorrelation(runHeaderOrigins []string, traceContext *RunTraceContextSettings, testName string) runCorrelation {
	origins := append([]string(nil), runHeaderOrigins...)
	carriers := runTraceContextCarriers(traceContext)
	if len(carriers) > 0 {
		for _, origin := range traceContext.Origins {
			if !slices.Contains(origins, origin) {
				origins = append(origins, origin)
			}
		}
		if len(runHeaderOrigins) > 0 {
			carriers = append([]string{engine.CorrelationCarrierRunMarker}, carriers...)
		}
	}
	slices.Sort(origins)
	return runCorrelation{Origins: origins, Carriers: carriers, TestName: strings.TrimSpace(testName)}
}

// runCorrelationAssignment resolves the correlation a run sends to generators.
func (tr *TestRunner) runCorrelationAssignment(id string) runCorrelation {
	runHeaderOrigins := tr.runMonitorCorrelationOrigins(id)
	var traceContext *RunTraceContextSettings
	var testName string
	tr.mu.RLock()
	test := tr.tests[tr.resolveRefKeyLocked(id)]
	if test != nil {
		traceContext = cloneRunTraceContext(test.TraceContext)
		testName = test.Name
	}
	tr.mu.RUnlock()
	if test == nil && tr.testStore != nil {
		if record, err := tr.testStore.GetTest(id); err == nil && record != nil {
			traceContext = decodeStoredRunTraceContext(record.TraceContext)
			testName = record.Name
		}
	}
	return combineRunCorrelation(runHeaderOrigins, traceContext, testName)
}

// decodeStoredRunTraceContext reads the persisted JSON form. A malformed value
// is dropped rather than guessed, so a run never sends context nobody approved.
func decodeStoredRunTraceContext(raw any) *RunTraceContextSettings {
	if raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var settings RunTraceContextSettings
	if err := json.Unmarshal(encoded, &settings); err != nil {
		return nil
	}
	normalized, err := normalizeRunTraceContext(&settings)
	if err != nil {
		return nil
	}
	return normalized
}

func runTraceContextForStorage(settings *RunTraceContextSettings) any {
	if settings == nil {
		return nil
	}
	return cloneRunTraceContext(settings)
}
