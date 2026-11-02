package sample

import (
	"strings"
	"testing"
)

// Generic HAR mode (the /*ERROR*/ guard on /aura POSTs and the "enable
// Salesforce native authoring" warning) belongs to the entry points where
// native authoring is a default the user can turn off. The recorder and the
// sample importer never default it on and offer no such option, so their
// conversions must stay unchanged.
func TestGenericAuraModeIsScopedToHARImportEntryPoints(t *testing.T) {
	const host = "https://example-perf.lightning.force.com"
	content := recorderHAR(t,
		recorderEntry("2026-01-01T00:00:00Z", "GET", host+"/one/one.app", 200, "text/html", "<html><title>Lightning</title></html>", nil),
		formRequest(recorderEntry("2026-01-01T00:00:02Z", "POST", host+"/aura?r=1&ui-force-components-controllers.recordGlobalValueProvider.RecordGvp.getRecord=1", 200, "application/json", `{"actions":[{"id":"1;a","state":"SUCCESS","returnValue":{}}]}`, nil),
			"message=%7B%22actions%22%3A%5B%5D%7D&aura.context=%7B%7D&aura.token=SyntheticLiteralAuraTokenValue123"),
	)
	off := false
	cases := []struct {
		entryPoint string
		native     *bool
		want       bool
	}{
		{entryPoint: EntryPointImportHAR, native: &off, want: true},
		{entryPoint: EntryPointImportSalesforce, native: &off, want: true},
		{entryPoint: EntryPointBrowserHTTP, native: &off, want: true},
		{entryPoint: EntryPointRecorderConvert, want: false},
		{entryPoint: EntryPointImportSample, want: false},
		{entryPoint: "", want: false},
	}
	for _, tc := range cases {
		result, err := Convert(ImportRequest{Format: "har", Content: content, Options: ConvertOptions{EntryPoint: tc.entryPoint, SalesforceNative: tc.native, OmitSourceReview: true}})
		if err != nil {
			t.Fatalf("%q: %v", tc.entryPoint, err)
		}
		guarded := false
		for _, action := range result.Script.Actions {
			for _, assertion := range action.Assertions {
				if assertion.Type == "body_not_contains" && assertion.Value == "/*ERROR*/" {
					guarded = true
				}
			}
		}
		warned := false
		for _, warning := range result.Warnings {
			if strings.Contains(warning, "enable Salesforce native authoring") {
				warned = true
			}
		}
		if guarded != tc.want || warned != tc.want {
			t.Fatalf("%q: /*ERROR*/ guard=%v, native-authoring warning=%v, want both %v (warnings %q)", tc.entryPoint, guarded, warned, tc.want, result.Warnings)
		}
	}
}
