package trueclient

import (
	"strings"
	"testing"
)

func TestInspectArchivePreservesWeb2MetadataInputs(t *testing.T) {
	inspection, err := InspectArchive(map[string]string{
		"Web2/Web2.usr":                         "[General]\nType=TruClient 2.0 - Web\n",
		"Web2/default.xml":                      web2InspectionDefaultXML(),
		"Web2/ObjectRepository/MainObjects.xml": "<objects><object name=\"Search\" /></objects>",
		"Web2/parameters/users.dat":             "username,password\nqa,secret\n",
		"Web2/default.cfg":                      "[ThinkTime]\nOptions=RECORDED\n",
	}, "TruClient 2.0 - Web")
	if err != nil {
		t.Fatalf("InspectArchive returned error: %v", err)
	}

	if inspection.ScriptTypeScope == nil || inspection.ScriptTypeScope.CanonicalName != "truclient2_web" || inspection.ScriptTypeScope.Status != ScopeReviewOnly {
		t.Fatalf("script type scope = %#v, want review-only TruClient 2.0 Web scope", inspection.ScriptTypeScope)
	}
	if inspection.DefaultXML == nil || inspection.DefaultXML.Path != "Web2/default.xml" || inspection.DefaultXML.Kind != "default_xml" {
		t.Fatalf("default_xml = %#v, want Web2/default.xml summary", inspection.DefaultXML)
	}
	if len(inspection.ProjectFiles) != 1 || inspection.ProjectFiles[0].Path != "Web2/Web2.usr" {
		t.Fatalf("project files = %#v, want Web2.usr", inspection.ProjectFiles)
	}
	if len(inspection.ObjectRepositories) != 1 || inspection.ObjectRepositories[0].Path != "Web2/ObjectRepository/MainObjects.xml" {
		t.Fatalf("object repositories = %#v, want MainObjects.xml", inspection.ObjectRepositories)
	}
	if len(inspection.ParameterFiles) != 1 || inspection.ParameterFiles[0].Path != "Web2/parameters/users.dat" {
		t.Fatalf("parameter files = %#v, want users.dat", inspection.ParameterFiles)
	}
	if len(inspection.RuntimeFiles) != 1 || inspection.RuntimeFiles[0].Path != "Web2/default.cfg" {
		t.Fatalf("runtime files = %#v, want default.cfg", inspection.RuntimeFiles)
	}
	if inspection.StepInventory.Total != 6 {
		t.Fatalf("step inventory total = %d, want 6 inventory=%#v", inspection.StepInventory.Total, inspection.StepInventory)
	}
	if inspection.StepInventory.Supported["navigate"] != 1 || inspection.StepInventory.Supported["click"] != 1 || inspection.StepInventory.Supported["set_type"] != 1 || inspection.StepInventory.Supported["verify"] != 1 {
		t.Fatalf("supported step inventory = %#v, want navigate/click/set/verify counts", inspection.StepInventory.Supported)
	}
	if inspection.StepInventory.ReviewOnly["evaluate_javascript"] != 1 {
		t.Fatalf("review step inventory = %#v, want Evaluate JavaScript count", inspection.StepInventory.ReviewOnly)
	}
	if inspection.StepInventory.Unsupported["Native Desktop API"] != 1 {
		t.Fatalf("unsupported step inventory = %#v, want Native Desktop API count", inspection.StepInventory.Unsupported)
	}
	if len(inspection.Steps) != 6 {
		t.Fatalf("steps = %#v, want six step inspection records", inspection.Steps)
	}
	if inspection.Steps[0].StepID != "nav-1" || inspection.Steps[0].Index != "1" || inspection.Steps[0].Status != ScopeSupported || inspection.Steps[0].ScopeName != "navigate" {
		t.Fatalf("steps[0] = %#v, want supported navigate step evidence", inspection.Steps[0])
	}
	if len(inspection.UnsupportedFragments) != 2 {
		t.Fatalf("unsupported fragments = %#v, want review-only JavaScript and unsupported native fragments", inspection.UnsupportedFragments)
	}
	if !containsSourceFragment(inspection.UnsupportedFragments, "Evaluate JavaScript", ScopeReviewOnly, "window.app.checkout") ||
		!containsSourceFragment(inspection.UnsupportedFragments, "Native Desktop API", ScopeOutOfScope, "NativeCall") {
		t.Fatalf("unsupported fragments = %#v, want preserved JavaScript and native source snippets", inspection.UnsupportedFragments)
	}
	if inspection.CapabilityStatuses["object_identification"] != ScopePlanned || inspection.CapabilityStatuses["native_desktop_automation"] != ScopeOutOfScope {
		t.Fatalf("capability statuses = %#v, want planned object identification and out-of-scope native desktop", inspection.CapabilityStatuses)
	}
	if !containsString(inspection.PreservedMetadataInputs, "object_repository: Web2/ObjectRepository/MainObjects.xml") ||
		!containsString(inspection.PreservedMetadataInputs, "parameter: Web2/parameters/users.dat") ||
		!containsString(inspection.PreservedMetadataInputs, "runtime: Web2/default.cfg") {
		t.Fatalf("preserved metadata inputs = %#v, want object repository, parameter, and runtime entries", inspection.PreservedMetadataInputs)
	}
}

func TestInspectArchiveWarnsWhenDefaultXMLMissing(t *testing.T) {
	inspection, err := InspectArchive(map[string]string{
		"Web2/Web2.usr":    "[General]\nType=TruClient 2.0 - Web\n",
		"Web2/default.cfg": "[ThinkTime]\nOptions=RECORDED\n",
	}, "TruClient 2.0 - Web")
	if err != nil {
		t.Fatalf("InspectArchive returned error: %v", err)
	}
	if inspection.DefaultXML != nil {
		t.Fatalf("default_xml = %#v, want nil", inspection.DefaultXML)
	}
	if len(inspection.Warnings) != 1 || inspection.Warnings[0] != "No TruClient default.xml was found in the archive." {
		t.Fatalf("warnings = %#v, want missing default.xml warning", inspection.Warnings)
	}
	if len(inspection.RuntimeFiles) != 1 {
		t.Fatalf("runtime files = %#v, want runtime file still preserved", inspection.RuntimeFiles)
	}
}

func web2InspectionDefaultXML() string {
	return encodeUTF16LE(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" stepId="nav-1" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://shop.example.com\&quot;&quot;}}" />
    <step type="testObject" action="Set / Type" stepId="type-1" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#search\&quot;&quot;},&quot;Value&quot;:{&quot;value&quot;:&quot;\&quot;stormrunner\&quot;&quot;}}" />
    <step type="testObject" action="Click" stepId="click-1" index="3" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#submit\&quot;&quot;}}" />
    <step type="testObject" action="Verify" stepId="verify-1" index="4" arguments="{&quot;Text&quot;:{&quot;value&quot;:&quot;\&quot;Results\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" stepId="js-1" index="5" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;window.app.checkout()&quot;}}" />
    <step type="util" action="Native Desktop API" stepId="native-1" index="6" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;NativeCall('legacy')&quot;}}" />
  </step>
</truScript>`)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSourceFragment(values []SourceFragment, action string, status ScopeStatus, snippet string) bool {
	for _, value := range values {
		if value.Action == action && value.Status == status && strings.Contains(value.SourceSnippet, snippet) {
			return true
		}
	}
	return false
}
