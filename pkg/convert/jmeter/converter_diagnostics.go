package jmeter

// converter_diagnostics.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func countJMeterSamplerElements(nodes []*treeNode) (int, int) {
	total := 0
	skipped := 0
	for _, node := range nodes {
		if node == nil || node.Element == nil {
			continue
		}
		if isJMeterSamplerElement(node.Element.Name) {
			total++
			if node.Element.Name != "HTTPSamplerProxy" {
				skipped++
			}
		}
		childTotal, childSkipped := countJMeterSamplerElements(node.Children)
		total += childTotal
		skipped += childSkipped
	}
	return total, skipped
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
		*mappings = append(*mappings, jmeterSourceMapping(
			sourceFile,
			node.Element,
			"jmeter_extractor",
			fmt.Sprintf("actions[%d].extractors[%d]", actionIndex, index),
			extractor.Name,
			"extractor",
			"contributor",
			"JMeter post-processor was converted into a generated extractor.",
		))
	}
	for index, assertion := range action.Assertions {
		*mappings = append(*mappings, jmeterSourceMapping(
			sourceFile,
			node.Element,
			"jmeter_assertion",
			fmt.Sprintf("actions[%d].assertions[%d]", actionIndex, index),
			assertion.Type,
			"assertion",
			"contributor",
			"JMeter assertion or default assertion contributed to this generated assertion.",
		))
	}
	if action.ThinkTimeAfter {
		*mappings = append(*mappings, jmeterSourceMapping(
			sourceFile,
			node.Element,
			"jmeter_timer",
			fmt.Sprintf("actions[%d].think_time", actionIndex),
			"Think Time",
			"config",
			"contributor",
			"JMeter timer contributed to this generated think-time setting.",
		))
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
	case "TestPlan", "ThreadGroup", "hashTree", "ConfigTestElement", "HeaderManager", "CookieManager", "CacheManager", "AuthManager", "Arguments", "CSVDataSet", "ConstantTimer", "UniformRandomTimer", "GaussianRandomTimer", "PoissonRandomTimer", "ConstantThroughputTimer", "PreciseThroughputTimer", "ResponseAssertion", "HTTPSamplerProxy", "RegexExtractor", "JSONPostProcessor", "JMESPathExtractor", "HtmlExtractor", "XPathExtractor", "XPath2Extractor", "BoundaryExtractor", "LoopController", "IfController", "OnceOnlyController", "TestAction", "DebugSampler":
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
	for i, script := range scripts {
		tg := threadGroups[min(i, len(threadGroups)-1)]
		vusers := parseInt(convertutil.FirstNonEmpty(prop(tg.Element, "ThreadGroup.num_threads"), "1"))
		rampSeconds := parseInt(prop(tg.Element, "ThreadGroup.ramp_time"))
		durationSeconds := parseInt(prop(tg.Element, "ThreadGroup.duration"))
		if durationSeconds > 0 {
			draft.Duration = (time.Duration(durationSeconds) * time.Second).String()
		}
		draft.Groups = append(draft.Groups, ScenarioDraftGroup{
			Name:           attrOr(tg.Element, "testname", script.Name),
			ScriptName:     script.Name,
			VUsers:         max(vusers, 1),
			RampUp:         (time.Duration(rampSeconds) * time.Second).String(),
			Initialization: "at_runtime",
		})
	}
	return draft
}
