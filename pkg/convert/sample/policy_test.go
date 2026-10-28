package sample

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

func TestValidatePolicyAndModeEnums(t *testing.T) {
	for _, valid := range []string{"", "legacy", "unified_v1", "LEGACY", " unified_v1 "} {
		if _, err := ValidateConversionPolicy(valid); err != nil {
			t.Fatalf("ValidateConversionPolicy(%q) returned unexpected error: %v", valid, err)
		}
	}
	if _, err := ValidateConversionPolicy("v2_unknown"); err == nil {
		t.Fatal("ValidateConversionPolicy(v2_unknown) succeeded, want error")
	}

	for _, valid := range []string{"", "auto", "high_confidence", "all", "review_only", "disabled"} {
		if _, err := ValidateCorrelationMode(valid); err != nil {
			t.Fatalf("ValidateCorrelationMode(%q) returned unexpected error: %v", valid, err)
		}
	}
	if _, err := ValidateCorrelationMode("bogus"); err == nil {
		t.Fatal("ValidateCorrelationMode(bogus) succeeded, want error")
	}

	for _, valid := range []string{"", "runtime", "in_step", "separate"} {
		if _, err := ValidateResourceMode(valid); err != nil {
			t.Fatalf("ValidateResourceMode(%q) returned unexpected error: %v", valid, err)
		}
	}
	if _, err := ValidateResourceMode("inline"); err == nil {
		t.Fatal("ValidateResourceMode(inline) succeeded, want error")
	}
}

func TestResolveConversionPolicyPrecedenceAndEntryPoints(t *testing.T) {
	t.Run("legacy import_sample defaults to all correlations without salesforce native", func(t *testing.T) {
		settings, err := ResolveConversionPolicy(PolicyResolutionInput{
			EntryPoint: EntryPointImportSample,
			Format:     "har",
		})
		if err != nil {
			t.Fatalf("ResolveConversionPolicy error: %v", err)
		}
		if settings.ConversionPolicy != ConversionPolicyLegacy {
			t.Fatalf("ConversionPolicy = %q, want legacy", settings.ConversionPolicy)
		}
		if settings.CorrelationMode != CorrelationModeAll || settings.HighConfidenceOnly || settings.ReviewOnlyCorrelations {
			t.Fatalf("correlation settings = %#v, want all/highConfidence=false/reviewOnly=false", settings)
		}
		if settings.SalesforceNative {
			t.Fatalf("SalesforceNative = true, want false on legacy import_sample")
		}
	})

	t.Run("unified_v1 import_sample enables high_confidence and salesforce native for HAR", func(t *testing.T) {
		settings, err := ResolveConversionPolicy(PolicyResolutionInput{
			EntryPoint:       EntryPointImportSample,
			Format:           "har",
			ConversionPolicy: ConversionPolicyUnifiedV1,
		})
		if err != nil {
			t.Fatalf("ResolveConversionPolicy error: %v", err)
		}
		if settings.ConversionPolicy != ConversionPolicyUnifiedV1 {
			t.Fatalf("ConversionPolicy = %q, want unified_v1", settings.ConversionPolicy)
		}
		if settings.CorrelationMode != CorrelationModeHighConfidence || !settings.HighConfidenceOnly {
			t.Fatalf("correlation settings = %#v, want high_confidence", settings)
		}
		if !settings.SalesforceNative {
			t.Fatalf("SalesforceNative = false, want true on unified_v1 HAR import_sample")
		}
	})

	t.Run("disable_salesforce clears both salesforce_native and agentforce_transform", func(t *testing.T) {
		tr := true
		settings, err := ResolveConversionPolicy(PolicyResolutionInput{
			EntryPoint:          EntryPointImportSalesforce,
			Format:              "har",
			ConversionPolicy:    ConversionPolicyUnifiedV1,
			DisableSalesforce:   &tr,
			SalesforceNative:    &tr,
			AgentforceTransform: &tr,
		})
		if err != nil {
			t.Fatalf("ResolveConversionPolicy error: %v", err)
		}
		if !settings.DisableSalesforce || settings.SalesforceNative || settings.AgentforceTransform {
			t.Fatalf("settings = %#v, want DisableSalesforce=true, SalesforceNative=false, AgentforceTransform=false", settings)
		}
	})

	t.Run("recording profile disables correlation and supplies generation defaults", func(t *testing.T) {
		pageTitleChecks := false
		asyncScan := false
		profile := &recordingprofile.Profile{
			ID:              "prof-disabled",
			Name:            "Disabled Corr Profile",
			PageTitleChecks: &pageTitleChecks,
			ResourceMode:    "separate",
			AsyncScan:       &asyncScan,
			Correlation: recordingprofile.CorrelationSettings{
				Disabled: true,
			},
		}
		settings, err := ResolveConversionPolicy(PolicyResolutionInput{
			EntryPoint: EntryPointImportHAR,
			Format:     "har",
			Profile:    profile,
		})
		if err != nil {
			t.Fatalf("ResolveConversionPolicy error: %v", err)
		}
		if settings.RecordingProfileID != "prof-disabled" || settings.RecordingProfileName != "Disabled Corr Profile" {
			t.Fatalf("RecordingProfile = %q/%q, want prof-disabled", settings.RecordingProfileID, settings.RecordingProfileName)
		}
		if settings.CorrelationMode != CorrelationModeDisabled {
			t.Fatalf("settings = %#v, want disabled correlation from profile", settings)
		}
		if settings.PageTitleChecks || settings.ResourceMode != "separate" || settings.AsyncScan {
			t.Fatalf("generation settings = %#v, want profile defaults applied", settings)
		}

		// Explicit request resource_mode and correlation_mode override non-disabled profile defaults,
		// while profile.Correlation.Disabled without user rules keeps effective CorrelationMode disabled
		// even as ReviewOnlyCorrelations is recorded.
		overrideSettings, err := ResolveConversionPolicy(PolicyResolutionInput{
			EntryPoint:      EntryPointImportHAR,
			Format:          "har",
			Profile:         profile,
			ResourceMode:    "in_step",
			CorrelationMode: CorrelationModeReviewOnly,
		})
		if err != nil {
			t.Fatalf("ResolveConversionPolicy override error: %v", err)
		}
		if overrideSettings.ResourceMode != "in_step" {
			t.Fatalf("override ResourceMode = %q, want in_step", overrideSettings.ResourceMode)
		}
		if !overrideSettings.ReviewOnlyCorrelations {
			t.Fatalf("override ReviewOnlyCorrelations = false, want true")
		}

		// When profile correlation is not disabled, explicit CorrelationModeReviewOnly sets both
		// CorrelationMode=review_only and ReviewOnlyCorrelations=true.
		enabledProfile := *profile
		enabledProfile.Correlation.Disabled = false
		reviewOnlySettings, err := ResolveConversionPolicy(PolicyResolutionInput{
			EntryPoint:      EntryPointImportHAR,
			Format:          "har",
			Profile:         &enabledProfile,
			ResourceMode:    "in_step",
			CorrelationMode: CorrelationModeReviewOnly,
		})
		if err != nil {
			t.Fatalf("ResolveConversionPolicy reviewOnly error: %v", err)
		}
		if reviewOnlySettings.CorrelationMode != CorrelationModeReviewOnly || !reviewOnlySettings.ReviewOnlyCorrelations {
			t.Fatalf("reviewOnlySettings = %#v, want CorrelationMode=review_only and ReviewOnlyCorrelations=true", reviewOnlySettings)
		}
	})

	t.Run("AttachEffectiveSettings and ReadEffectiveSettings round-trip", func(t *testing.T) {
		script := &dsl.Script{Name: "test", Protocol: "http"}
		want := &EffectiveSettings{
			ConversionPolicy:   ConversionPolicyUnifiedV1,
			SourceEntryPoint:   EntryPointImportHAR,
			RecordingProfileID: "prof-1",
			ResourceMode:       "separate",
			CorrelationMode:    CorrelationModeHighConfidence,
		}
		AttachEffectiveSettings(script, want)
		got, ok := ReadEffectiveSettings(script)
		if !ok || got == nil {
			t.Fatal("ReadEffectiveSettings returned false/nil")
		}
		if got.ConversionPolicy != want.ConversionPolicy || got.RecordingProfileID != want.RecordingProfileID || got.ResourceMode != want.ResourceMode {
			t.Fatalf("ReadEffectiveSettings = %#v, want %#v", got, want)
		}
	})

	t.Run("HasNonDefaultRecordingConversionSettings checks every inspected field", func(t *testing.T) {
		if HasNonDefaultRecordingConversionSettings(nil) {
			t.Fatal("HasNonDefaultRecordingConversionSettings(nil) = true, want false")
		}
		defaultRec, err := ResolveConversionPolicy(PolicyResolutionInput{
			EntryPoint: EntryPointRecorderConvert,
			Format:     "har",
		})
		if err != nil {
			t.Fatalf("ResolveConversionPolicy error: %v", err)
		}
		cases := []struct {
			name   string
			mutate func(*EffectiveSettings)
			want   bool
		}{
			{"all defaults", func(s *EffectiveSettings) {}, false},
			{"CorrelationMode=high_confidence", func(s *EffectiveSettings) { s.CorrelationMode = CorrelationModeHighConfidence }, false},
			{"CorrelationMode=auto", func(s *EffectiveSettings) { s.CorrelationMode = CorrelationModeAuto }, false},
			{"ConversionPolicy=unified_v1", func(s *EffectiveSettings) { s.ConversionPolicy = ConversionPolicyUnifiedV1 }, true},
			{"RecordingProfileID=prof-1", func(s *EffectiveSettings) { s.RecordingProfileID = "prof-1" }, true},
			{"ResourceMode=in_step", func(s *EffectiveSettings) { s.ResourceMode = "in_step" }, true},
			{"ResourceMode=separate", func(s *EffectiveSettings) { s.ResourceMode = "separate" }, true},
			{"AsyncScan=false", func(s *EffectiveSettings) { s.AsyncScan = false }, true},
			{"SalesforceNative=true", func(s *EffectiveSettings) { s.SalesforceNative = true }, true},
			{"AgentforceTransform=true", func(s *EffectiveSettings) { s.AgentforceTransform = true }, true},
			{"Parameterize=true", func(s *EffectiveSettings) { s.Parameterize = true }, true},
			{"CorrelationMode=all", func(s *EffectiveSettings) { s.CorrelationMode = CorrelationModeAll }, true},
			{"CorrelationMode=review_only", func(s *EffectiveSettings) { s.CorrelationMode = CorrelationModeReviewOnly }, true},
			{"CorrelationMode=disabled", func(s *EffectiveSettings) { s.CorrelationMode = CorrelationModeDisabled }, true},
			{"IncludeAssertions=false", func(s *EffectiveSettings) { s.IncludeAssertions = false }, true},
			{"InferExtractors=false", func(s *EffectiveSettings) { s.InferExtractors = false }, true},
			{"PageTitleChecks=false", func(s *EffectiveSettings) { s.PageTitleChecks = false }, true},
			{"HTTPVersionMode=exact", func(s *EffectiveSettings) { s.HTTPVersionMode = "exact" }, true},
			{"HTTPVersionMode=inherit", func(s *EffectiveSettings) { s.HTTPVersionMode = "inherit" }, true},
			{"HighConfidenceOnly=false", func(s *EffectiveSettings) { s.HighConfidenceOnly = false }, true},
			{"ReviewOnlyCorrelations=true", func(s *EffectiveSettings) { s.ReviewOnlyCorrelations = true }, true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				cp := *defaultRec
				tc.mutate(&cp)
				if got := HasNonDefaultRecordingConversionSettings(&cp); got != tc.want {
					t.Fatalf("HasNonDefaultRecordingConversionSettings(%s) = %t, want %t (settings=%#v)", tc.name, got, tc.want, cp)
				}
			})
		}
	})
}
