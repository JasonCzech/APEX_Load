package sample

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func minimalPolicyTestScript() *dsl.Script {
	return &dsl.Script{Protocol: "http"}
}

// Native Salesforce rewrites have always applied only high-confidence
// correlations. The legacy generic HAR default (apply every correlation) must
// not widen them; only an explicit correlation_mode "all" may.
func TestResolveConversionPolicyNativeHighConfidenceThreshold(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name            string
		input           PolicyResolutionInput
		wantGeneric     bool
		wantNative      bool
		wantEffectiveCM string
	}{
		{
			name:            "legacy HAR import default keeps native high-confidence",
			input:           PolicyResolutionInput{EntryPoint: EntryPointImportHAR, Format: "har"},
			wantGeneric:     false,
			wantNative:      true,
			wantEffectiveCM: CorrelationModeAll,
		},
		{
			name:            "legacy browser HTTP default keeps native high-confidence",
			input:           PolicyResolutionInput{EntryPoint: EntryPointBrowserHTTP, Format: "har"},
			wantGeneric:     false,
			wantNative:      true,
			wantEffectiveCM: CorrelationModeAll,
		},
		{
			name:            "unified_v1 default is high-confidence for both",
			input:           PolicyResolutionInput{EntryPoint: EntryPointImportHAR, Format: "har", ConversionPolicy: ConversionPolicyUnifiedV1},
			wantGeneric:     true,
			wantNative:      true,
			wantEffectiveCM: CorrelationModeHighConfidence,
		},
		{
			name:            "explicit all widens native rewrites",
			input:           PolicyResolutionInput{EntryPoint: EntryPointImportHAR, Format: "har", CorrelationMode: CorrelationModeAll},
			wantGeneric:     false,
			wantNative:      false,
			wantEffectiveCM: CorrelationModeAll,
		},
		{
			name:            "explicit all overrides a recorder high-confidence default",
			input:           PolicyResolutionInput{EntryPoint: EntryPointRecorderConvert, Format: "har", HighConfidenceOnly: &yes, CorrelationMode: CorrelationModeAll},
			wantGeneric:     false,
			wantNative:      false,
			wantEffectiveCM: CorrelationModeAll,
		},
		{
			name:            "explicit high_confidence keeps native high-confidence",
			input:           PolicyResolutionInput{EntryPoint: EntryPointImportHAR, Format: "har", CorrelationMode: CorrelationModeHighConfidence},
			wantGeneric:     true,
			wantNative:      true,
			wantEffectiveCM: CorrelationModeHighConfidence,
		},
		{
			name:            "explicit caller threshold false applies without a mode",
			input:           PolicyResolutionInput{EntryPoint: EntryPointImportHAR, Format: "har", HighConfidenceOnly: &no},
			wantGeneric:     false,
			wantNative:      false,
			wantEffectiveCM: CorrelationModeAll,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			settings, err := ResolveConversionPolicy(tc.input)
			if err != nil {
				t.Fatalf("ResolveConversionPolicy: %v", err)
			}
			if settings.HighConfidenceOnly != tc.wantGeneric {
				t.Errorf("HighConfidenceOnly = %v, want %v", settings.HighConfidenceOnly, tc.wantGeneric)
			}
			if settings.NativeHighConfidenceOnly != tc.wantNative {
				t.Errorf("NativeHighConfidenceOnly = %v, want %v", settings.NativeHighConfidenceOnly, tc.wantNative)
			}
			if settings.CorrelationMode != tc.wantEffectiveCM {
				t.Errorf("CorrelationMode = %q, want %q", settings.CorrelationMode, tc.wantEffectiveCM)
			}
		})
	}
}

func TestNativeHighConfidenceThresholdIsNotPersisted(t *testing.T) {
	settings, err := ResolveConversionPolicy(PolicyResolutionInput{EntryPoint: EntryPointImportHAR, Format: "har"})
	if err != nil {
		t.Fatal(err)
	}
	if !settings.NativeHighConfidenceOnly {
		t.Fatal("legacy HAR import must resolve a native high-confidence threshold")
	}
	script := minimalPolicyTestScript()
	AttachEffectiveSettings(script, settings)
	preserved := script.Metadata.Import.Preserved[PreservedEffectiveSettingsKey].(map[string]any)
	for key := range preserved {
		if key == "NativeHighConfidenceOnly" || key == "native_high_confidence_only" {
			t.Fatalf("native threshold leaked into persisted effective_settings: %#v", preserved)
		}
	}
}
