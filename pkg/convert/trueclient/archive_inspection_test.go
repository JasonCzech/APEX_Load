package trueclient

import (
	"strings"
	"testing"
)

func TestInspectArchivePreservesWeb2MetadataInputs(t *testing.T) {
	inspection, err := InspectArchive(map[string]string{
		"Web2/Web2.usr":                         "[General]\nType=TruClient 2.0 - Web\n",
		"Web2/default.xml":                      web2InspectionDefaultXML(),
		"Web2/ObjectRepository/MainObjects.xml": `<objects><object id="search-box" name="Search Box" selector="[data-testid='search']" xpath="//input[@id='search']" role="textbox" frame="main-frame" window="primary-tab" ordinal="2" regex="Search.*" data-testid="search-input" aria-label="Search field" /></objects>`,
		"Web2/parameters/users.dat":             "username,password\nqa,secret\n",
		"Web2/default.cfg":                      "[ThinkTime]\nOptions=RECORDED\n",
	}, "TruClient 2.0 - Web")
	if err != nil {
		t.Fatalf("InspectArchive returned error: %v", err)
	}

	if inspection.ScriptTypeScope == nil || inspection.ScriptTypeScope.CanonicalName != "truclient2_web" || inspection.ScriptTypeScope.Status != ScopeSupported {
		t.Fatalf("script type scope = %#v, want supported TruClient 2.0 Web scope", inspection.ScriptTypeScope)
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
	if len(inspection.ObjectDescriptors) != 1 || inspection.ObjectDescriptors[0].ID != "search-box" || inspection.ObjectDescriptors[0].FrameContext != "main-frame" {
		t.Fatalf("object descriptors = %#v, want search-box descriptor with frame context", inspection.ObjectDescriptors)
	}
	if !containsSelectorCandidate(inspection.ObjectDescriptors[0].SelectorCandidates, "css", `[data-testid="search-input"]`, 95) {
		t.Fatalf("object descriptor candidates = %#v, want data-testid candidate", inspection.ObjectDescriptors[0].SelectorCandidates)
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
	if inspection.Steps[1].ObjectDescriptorID != "search-box" || !containsSelectorCandidate(inspection.Steps[1].SelectorCandidates, "css", "#search", 85) || !containsSelectorCandidate(inspection.Steps[1].SelectorCandidates, "xpath", "//input[@id='search']", 60) {
		t.Fatalf("steps[1] = %#v, want object descriptor id and selector candidates", inspection.Steps[1])
	}
	if inspection.Steps[1].ObjectDescriptor == nil || inspection.Steps[1].ObjectDescriptor.SourcePath != "Web2/ObjectRepository/MainObjects.xml" {
		t.Fatalf("steps[1].object_descriptor = %#v, want matched object repository descriptor", inspection.Steps[1].ObjectDescriptor)
	}
	if inspection.Steps[1].FrameContext != "main-frame" || inspection.Steps[1].WindowContext != "primary-tab" || inspection.Steps[1].Ordinal != "2" || inspection.Steps[1].Regex != "Search.*" {
		t.Fatalf("steps[1] context = frame %q window %q ordinal %q regex %q, want descriptor context", inspection.Steps[1].FrameContext, inspection.Steps[1].WindowContext, inspection.Steps[1].Ordinal, inspection.Steps[1].Regex)
	}
	if !containsString(inspection.Steps[1].ParameterRefs, "username") || !containsString(inspection.Steps[1].SnapshotRefs, "snapshots/search.png") {
		t.Fatalf("steps[1] refs = params %#v snapshots %#v, want username and snapshots/search.png", inspection.Steps[1].ParameterRefs, inspection.Steps[1].SnapshotRefs)
	}
	if !containsReplayDiagnostic(inspection.Steps[1].ReplayDiagnostics, "frame_context_resolved") || !containsReplayDiagnostic(inspection.Steps[1].ReplayDiagnostics, "window_context_resolved") || !containsReplayDiagnostic(inspection.Steps[1].ReplayDiagnostics, "ambiguous_match_risk") {
		t.Fatalf("steps[1] diagnostics = %#v, want resolved frame/window and ambiguous-match diagnostics", inspection.Steps[1].ReplayDiagnostics)
	}
	if !containsSelectorCandidate(inspection.Steps[3].SelectorCandidates, "text", "Results", 55) {
		t.Fatalf("steps[3] = %#v, want text selector candidate", inspection.Steps[3])
	}
	if len(inspection.UnsupportedFragments) != 2 {
		t.Fatalf("unsupported fragments = %#v, want review-only JavaScript and unsupported native fragments", inspection.UnsupportedFragments)
	}
	if !containsSourceFragment(inspection.UnsupportedFragments, "Evaluate JavaScript", ScopeReviewOnly, "window.app.checkout") ||
		!containsSourceFragment(inspection.UnsupportedFragments, "Native Desktop API", ScopeOutOfScope, "NativeCall") {
		t.Fatalf("unsupported fragments = %#v, want preserved JavaScript and native source snippets", inspection.UnsupportedFragments)
	}
	if inspection.CapabilityStatuses["object_identification"] != ScopeReviewOnly || inspection.CapabilityStatuses["native_desktop_automation"] != ScopeOutOfScope {
		t.Fatalf("capability statuses = %#v, want review-only object identification and out-of-scope native desktop", inspection.CapabilityStatuses)
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
    <step type="testObject" action="Set / Type" stepId="type-1" index="2" arguments="{&quot;Object ID&quot;:{&quot;value&quot;:&quot;\&quot;search-box\&quot;&quot;},&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#search\&quot;&quot;},&quot;XPath&quot;:{&quot;value&quot;:&quot;\&quot;//input[@id='search']\&quot;&quot;},&quot;Value&quot;:{&quot;value&quot;:&quot;\&quot;{username}\&quot;&quot;},&quot;Snapshot&quot;:{&quot;value&quot;:&quot;\&quot;snapshots/search.png\&quot;&quot;}}" />
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

func containsSelectorCandidate(values []SelectorCandidate, kind string, value string, confidence int) bool {
	for _, candidate := range values {
		if candidate.Kind == kind && candidate.Value == value && candidate.Confidence == confidence {
			return true
		}
	}
	return false
}

func containsReplayDiagnostic(values []ReplayDiagnostic, code string) bool {
	for _, diagnostic := range values {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
