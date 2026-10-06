package jmeter

// converter_diagnostics.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func countJMeterSamplerElements(nodes []*treeNode) (int, int) {
	return countJMeterSamplerElementsEnabled(nodes, true)
}

func countJMeterSamplerElementsEnabled(nodes []*treeNode, ancestorsEnabled bool) (int, int) {
	total := 0
	skipped := 0
	for _, node := range nodes {
		if node == nil || node.Element == nil {
			continue
		}
		enabled := ancestorsEnabled && jmeterElementEnabled(node.Element)
		if isJMeterSamplerElement(node.Element.Name) {
			total++
			if !enabled || node.Element.Name != "HTTPSamplerProxy" {
				skipped++
			}
		}
		childTotal, childSkipped := countJMeterSamplerElementsEnabled(node.Children, enabled)
		total += childTotal
		skipped += childSkipped
	}
	return total, skipped
}

func appendJMeterDisabledMapping(mappings *[]sourceview.Mapping, sourceFile string, element *xmlNode) {
	if mappings == nil || element == nil {
		return
	}
	location := jmeterElementLocation(element)
	for _, existing := range *mappings {
		if existing.SourceFile == sourceFile &&
			existing.StartLine == element.StartLine &&
			existing.Function == element.Name &&
			existing.Status == "ignored" {
			return
		}
	}
	*mappings = append(*mappings, jmeterSourceMapping(
		sourceFile,
		element,
		element.Name,
		"",
		location,
		"disabled",
		"ignored",
		"Disabled JMeter element and its subtree were intentionally omitted.",
	))
}

func appendJMeterActionSourceMappings(mappings *[]sourceview.Mapping, sourceFile string, node *treeNode, actionIndex int, action *dsl.Action) {
	if mappings == nil || node == nil || node.Element == nil || action == nil {
		return
	}
	*mappings = append(*mappings, jmeterSourceMapping(
		sourceFile,
		node.Element,
		node.Element.Name,
		fmt.Sprintf("actions[%d]", actionIndex),
		action.Name,
		"action",
		"converted",
		"JMeter HTTP sampler was converted into a generated APEX Load action.",
	))
	for index, extractor := range action.Extractors {
		origin := node.Element
		for _, child := range node.Children {
			if !jmeterElementEnabled(child.Element) {
				continue
			}
			exts, _ := parseSamplerExtractors(&treeNode{Children: []*treeNode{child}}, nil)
			for _, ext := range exts {
				if ext.Name == extractor.Name && ext.Expression == extractor.Expression {
					origin = child.Element
				}
			}
		}
		*mappings = append(*mappings, jmeterSourceMapping(
			sourceFile,
			origin,
			"jmeter_extractor",
			fmt.Sprintf("actions[%d].extractors[%d]", actionIndex, index),
			extractor.Name,
			"extractor",
			"contributor",
			"JMeter post-processor was converted into a generated extractor.",
		))
	}
	for index, assertion := range action.Assertions {
		origin := node.Element
		for _, child := range node.Children {
			if child.Element.Name != "ResponseAssertion" || !jmeterElementEnabled(child.Element) {
				continue
			}
			for _, a := range parseResponseAssertions(child.Element, nil) {
				if reflect.DeepEqual(a, assertion) {
					origin = child.Element
				}
			}
		}
		*mappings = append(*mappings, jmeterSourceMapping(
			sourceFile,
			origin,
			"jmeter_assertion",
			fmt.Sprintf("actions[%d].assertions[%d]", actionIndex, index),
			assertion.Type,
			"assertion",
			"contributor",
			"JMeter assertion or default assertion contributed to this generated assertion.",
		))
	}
	if action.ThinkTimeAfter {
		for _, origin := range node.Children {
			if origin == nil || origin.Element == nil || !jmeterElementEnabled(origin.Element) || !strings.Contains(origin.Element.Name, "Timer") {
				continue
			}
			*mappings = append(*mappings, jmeterSourceMapping(sourceFile, origin.Element, "jmeter_timer", fmt.Sprintf("actions[%d].think_time", actionIndex), "Think Time", "config", "contributor", "JMeter timer contributed to this generated think-time setting."))
		}
	}

}

func jmeterSourceMapping(sourceFile string, element *xmlNode, function string, targetPath string, targetName string, targetKind string, status string, notes string) sourceview.Mapping {
	startLine := 1
	endLine := 1
	if element != nil {
		startLine = element.StartLine
		endLine = element.EndLine
		if endLine == 0 {
			endLine = startLine
		}
	}
	return sourceview.Mapping{
		ID:         jmeterMappingIdentity(element, function),
		SourceFile: sourceFile,
		StartLine:  startLine,
		EndLine:    endLine,
		Function:   function,
		TargetPath: targetPath,
		TargetName: targetName,
		TargetKind: targetKind,
		Status:     status,
		Notes:      notes,
	}
}

func isJMeterSamplerElement(name string) bool {
	if name == "HTTPSamplerProxy" || strings.HasSuffix(name, "Sampler") {
		return true
	}
	switch name {
	case "JMSPublisher", "JMSSubscriber", "JavaSampler", "TCPSampler", "FTPSampler":
		return true
	default:
		return false
	}
}

func diagnoseJMeterChildren(nodes []*treeNode, diagnostics *diag.Collector, unsupported map[string]struct{}) {
	for _, child := range nodes {
		if child == nil || child.Element == nil || !jmeterElementEnabled(child.Element) {
			continue
		}
		if diagnoseJMeterElement(child.Element, diagnostics) {
			unsupported[child.Element.Name] = struct{}{}
		}
		diagnoseJMeterChildren(child.Children, diagnostics, unsupported)
	}
}

func diagnoseJMeterElement(element *xmlNode, diagnostics *diag.Collector) bool {
	if element == nil || strings.TrimSpace(element.Name) == "" {
		return false
	}
	name := element.Name
	location := jmeterElementLocation(element)
	switch {
	case isJMeterListenerElement(name):
		diagnostics.Add(diag.Info, "JMeter "+name, location, "listener is reporting-only and does not affect replay; preserved as metadata")
		return false
	case isJMeterScriptElement(name):
		diagnostics.Add(diag.Warning, "JMeter "+name, location, "JSR223/BeanShell/Groovy scripting is not executed by the importer; translate script logic before replay")
		return true
	case isKnownJMeterUnsupported(name):
		diagnostics.Add(diag.Warning, "JMeter "+name, location, "element is not converted by the HTTP importer and was preserved for import review")
		return true
	case isKnownJMeterSupported(name):
		return false
	default:
		diagnostics.Add(diag.Warning, "JMeter "+name, location, "unrecognized JMX element was not converted; review the source plan before replay")
		return true
	}
}

func isJMeterScriptElement(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "jsr223") || strings.Contains(lower, "beanshell") || strings.Contains(lower, "bsh") || strings.Contains(lower, "groovy")
}

// isJMeterListenerElement reports whether the element is a reporting-only
// listener. Listeners do not affect replay and are preserved as metadata rather
// than blocking the import.
func isJMeterListenerElement(name string) bool {
	switch name {
	case "ResultCollector", "BackendListener", "Summariser", "MailerResultCollector":
		return true
	default:
		return false
	}
}

func isKnownJMeterUnsupported(name string) bool {
	switch name {
	case "JSR223Sampler", "BeanShellSampler", "BSFSampler", "JDBCSampler", "JMSSampler", "JMSPublisher", "JMSSubscriber", "FTPSampler", "JavaSampler", "TCPSampler", "JSR223PreProcessor", "JSR223PostProcessor", "BeanShellPreProcessor", "BeanShellPostProcessor":
		return true
	default:
		return false
	}
}

func isKnownJMeterSupported(name string) bool {
	switch name {
	case "TestPlan", "ThreadGroup", "hashTree", "ConfigTestElement", "HeaderManager", "CookieManager", "CacheManager", "AuthManager", "Arguments", "CSVDataSet", "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer", "PoissonRandomTimer", "ConstantThroughputTimer", "PreciseThroughputTimer", "ResponseAssertion", "HTTPSamplerProxy", "RegexExtractor", "JSONPostProcessor", "JMESPathExtractor", "HtmlExtractor", "XPathExtractor", "XPath2Extractor", "BoundaryExtractor", "LoopController", "IfController", "OnceOnlyController", "GenericController", "TransactionController", "TestAction", "DebugSampler":
		return true
	default:
		return false
	}
}

func jmeterElementLocation(element *xmlNode) string {
	if element == nil {
		return "JMX element"
	}
	name := attrOr(element, "testname", "")
	if name == "" {
		name = element.Name
	}
	return name
}

func buildScenarioDraft(threadGroups []*treeNode, scripts []*dsl.Script) *ScenarioDraft {
	draft := &ScenarioDraft{
		Name:         "Imported JMeter Scenario",
		Duration:     "5m",
		ScenarioMode: "vuser_driven",
		Groups:       []ScenarioDraftGroup{},
		Metadata:     map[string]interface{}{"source": "jmeter"},
	}
	authoredDurations := map[string]bool{}
	var messagingDuration time.Duration
	for _, script := range scripts {
		var tg *treeNode
		if script.Metadata != nil && script.Metadata.Import != nil {
			sourceID, _ := script.Metadata.Import.Preserved["source_group_id"].(string)
			planID, _ := script.Metadata.Import.Preserved["source_plan_id"].(string)
			for _, candidate := range threadGroups {
				if messagingNodeID(planID, candidate.Element) == sourceID {
					tg = candidate
					break
				}
			}
		}
		if tg == nil {
			continue
		}
		vusers := parseInt(convertutil.FirstNonEmpty(prop(tg.Element, "ThreadGroup.num_threads"), "1"))
		rampSeconds := parseInt(prop(tg.Element, "ThreadGroup.ramp_time"))
		durationSeconds := parseInt(prop(tg.Element, "ThreadGroup.duration"))
		if durationSeconds > 0 && strings.EqualFold(prop(tg.Element, "ThreadGroup.scheduler"), "true") {
			draft.Duration = (time.Duration(durationSeconds) * time.Second).String()
			authoredDurations[draft.Duration] = true
		}
		if script.Config.JMeter != nil {
			draft.GeneratorAffinity = "single_generator"
			var duration time.Duration
			for _, schedule := range script.Config.SamplerSchedules {
				if schedule.Duration() > duration {
					duration = schedule.Duration()
				}
			}
			if duration > 0 {
				if duration > messagingDuration {
					messagingDuration = duration
				}
				draft.Duration = duration.String()
				authoredDurations[draft.Duration] = true
			}
			draft.Metadata["strict_parity"] = "unverified"
			draft.Metadata["reference_context"] = "UTC and no property overrides require explicit confirmation"
		}
		draft.Groups = append(draft.Groups, ScenarioDraftGroup{
			SourceGroupID:    script.Metadata.Import.Preserved["source_group_id"].(string),
			SamplerSchedules: script.Config.SamplerSchedules,
			Name:             attrOr(tg.Element, "testname", script.Name),
			ScriptName:       script.Name,
			Iterations:       script.Config.RunLogic.Iterations,
			VUsers:           max(vusers, 1),
			RampUp:           (time.Duration(rampSeconds) * time.Second).String(),
			Initialization:   "at_runtime",
		})
	}

	if draft.GeneratorAffinity != "" {
		// Later, shorter groups must not truncate an earlier source schedule.
		// An absent messaging schedule must not acquire the legacy 5m default.
		if messagingDuration > 0 {
			draft.Duration = messagingDuration.String()
		} else if len(authoredDurations) == 0 {
			draft.Duration = ""
		}
	}
	allIterations := len(scripts) > 0
	anyIterations := false
	for _, script := range scripts {
		finite := script.Config.RunLogic.TerminationMode == dsl.TerminationIterations
		allIterations = allIterations && finite
		anyIterations = anyIterations || finite
	}
	if allIterations {
		draft.TerminationMode = dsl.TerminationIterations
		draft.Duration = ""
	} else if anyIterations || len(authoredDurations) > 1 {
		for _, script := range scripts {
			for i := range script.Actions {
				gateJMeterAction(&script.Actions[i], "jmeter_mixed_termination_modes", script.Name)
			}
		}
	}
	return draft
}

func jmeterMappingIdentity(e *xmlNode, function string) string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("jmeter-%d-%s", e.StartOffset, function)
}
func populateJMeterWorkloadAccounting(nodes []*treeNode, mappings []sourceview.Mapping, stats *ConvertStats) {
	byID := map[string]sourceview.Mapping{}
	for _, m := range mappings {
		if m.TargetPath != "" {
			byID[m.ID] = m
		}
	}
	var walk func([]*treeNode, bool)
	walk = func(nodes []*treeNode, parentEnabled bool) {
		for _, n := range nodes {
			if n == nil || n.Element == nil {
				continue
			}
			e := n.Element
			enabled := parentEnabled && jmeterElementEnabled(e)
			if isJMeterSamplerElement(e.Name) && e.Name != "DebugSampler" {
				if !enabled {
					stats.DisabledWorkloadSamplers++
				} else {
					stats.EnabledWorkloadSamplers++
					m, ok := byID[jmeterMappingIdentity(e, e.Name)]
					if ok && m.TargetKind != "unsupported" && (m.Status == "converted" || m.Status == "review_only") {
						stats.ConvertedWorkloadSamplers++
					}
					if !ok || m.Status != "converted" {
						stats.BlockedWorkloadSamplers++
					}
				}
			} else if enabled {
				if isJMeterListenerElement(e.Name) || e.Name == "DebugSampler" {
					stats.IgnoredReportingElements++
				}
				if isMessagingContributor(e.Name) || isJMeterDirectScopeConfig(e.Name) || strings.Contains(e.Name, "Timer") {
					stats.ConfigurationContributors++
				}
			}
			walk(n.Children, enabled)
		}
	}
	walk(nodes, true)
}
