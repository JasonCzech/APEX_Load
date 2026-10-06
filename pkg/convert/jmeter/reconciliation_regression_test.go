package jmeter

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestMergeJMeterCSVSourcesPreservesScopedSemanticConflicts(t *testing.T) {
	outer := dsl.DataSource{Name: "Rows", Type: "csv", Path: "rows.csv", Strategy: "sequential", Delimiter: ",", Columns: []string{"id"}}
	inner := dsl.DataSource{Name: "Rows", Type: "csv", Path: "rows.csv", Strategy: "random", Delimiter: ",", FirstRowHeader: true, Columns: []string{"tenant", "id"}}

	merged, conflicts := mergeJMeterCSVSources([]dsl.DataSource{outer}, []dsl.DataSource{outer, inner})
	if len(merged) != 2 {
		t.Fatalf("merged sources = %#v, want both scoped definitions", merged)
	}
	if merged[1].Name != "Rows_scoped_2" || merged[1].Strategy != "random" || !merged[1].FirstRowHeader || strings.Join(merged[1].Columns, ",") != "tenant,id" {
		t.Fatalf("nested source = %#v, want renamed source with all runtime semantics preserved", merged[1])
	}
	if len(conflicts) != 1 || conflicts[0].OriginalName != "Rows" || conflicts[0].RenamedName != "Rows_scoped_2" {
		t.Fatalf("conflicts = %#v", conflicts)
	}

	mergedAgain, repeatedConflicts := mergeJMeterCSVSources(merged, []dsl.DataSource{inner})
	if len(mergedAgain) != 2 || len(repeatedConflicts) != 0 {
		t.Fatalf("repeated merge = %#v, conflicts = %#v; want stable idempotent rename", mergedAgain, repeatedConflicts)
	}
}

func TestScopedCSVNameConflictIsPreservedAndReviewGated(t *testing.T) {
	const plan = `<?xml version="1.0"?>
<jmeterTestPlan><hashTree><TestPlan testname="Plan"/><hashTree><ThreadGroup testname="Users"/><hashTree>
  <CSVDataSet testname="Rows"><stringProp name="filename">rows.csv</stringProp><stringProp name="variableNames">id</stringProp><stringProp name="delimiter">,</stringProp></CSVDataSet><hashTree/>
  <TransactionController testname="Nested"/><hashTree>
    <CSVDataSet testname="Rows"><stringProp name="filename">rows.csv</stringProp><stringProp name="variableNames">tenant,id</stringProp><stringProp name="delimiter">,</stringProp><boolProp name="randomOrder">true</boolProp><boolProp name="ignoreFirstLine">true</boolProp></CSVDataSet><hashTree/>
    <HTTPSamplerProxy testname="Request"><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/${tenant}/${id}</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
  </hashTree>
</hashTree></hashTree></hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "scoped-csv.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	if len(script.DataSources) != 2 || script.DataSources[0].Name != "Rows" || script.DataSources[1].Name != "Rows_scoped_2" {
		t.Fatalf("data sources = %#v, want both unique scoped sources", script.DataSources)
	}
	if script.DataSources[1].Strategy != "random" || !script.DataSources[1].FirstRowHeader || strings.Join(script.DataSources[1].Columns, ",") != "tenant,id" {
		t.Fatalf("nested source semantics = %#v", script.DataSources[1])
	}
	if !hasJMeterReviewReason(script.Actions, "conflicting_csv_data_source_scope") {
		t.Fatalf("actions = %#v, want scoped CSV execution gate", script.Actions)
	}
	parity, _ := script.Metadata.Import.Preserved["runtime_parity"].(map[string]string)
	if parity["csvDataSet.scope"] != "review_required" {
		t.Fatalf("runtime parity = %#v", parity)
	}
}

func TestNestedControllerPropagatesCSVSourceAndRebasesSamplerMapping(t *testing.T) {
	const plan = `<?xml version="1.0"?>
<jmeterTestPlan><hashTree>
  <TestPlan testname="Plan"/><hashTree>
    <ThreadGroup testname="Users"/><hashTree>
      <LoopController testname="Loop"><stringProp name="LoopController.loops">2</stringProp></LoopController><hashTree>
        <CSVDataSet testname="Rows">
          <stringProp name="filename">rows.csv</stringProp>
          <stringProp name="variableNames">id</stringProp>
          <stringProp name="delimiter">,</stringProp>
        </CSVDataSet><hashTree/>
        <HTTPSamplerProxy testname="Nested request">
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.domain">example.test</stringProp>
          <stringProp name="HTTPSampler.path">/items/${id}</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy><hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "nested.jmx", SidecarFiles: map[string][]byte{"rows.csv": []byte("id\n1\n")}})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	if len(script.DataSources) != 1 || script.DataSources[0].Name != "Rows" {
		t.Fatalf("nested CSV sources = %#v, want propagated Rows source", script.DataSources)
	}
	found := false
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == "HTTPSamplerProxy" && mapping.TargetPath == "actions[0].control.steps[0]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("source mappings = %#v, want nested sampler mapping rebased into control steps", result.SourceReview.Mappings)
	}
}

func TestCSVFiniteEOFBehaviorIsReviewGated(t *testing.T) {
	const plan = `<?xml version="1.0"?>
<jmeterTestPlan><hashTree><TestPlan testname="Plan"/><hashTree><ThreadGroup testname="Users"/><hashTree>
  <CSVDataSet testname="Finite rows">
    <stringProp name="filename">rows.csv</stringProp>
    <boolProp name="recycle">false</boolProp>
    <boolProp name="stopThread">true</boolProp>
  </CSVDataSet><hashTree/>
  <HTTPSamplerProxy testname="Request"><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "finite.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	if len(script.Actions) < 2 || script.Actions[0].Type != "review_required" || script.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("actions = %#v, want EOF review gate before request", script.Actions)
	}
	parity, _ := script.Metadata.Import.Preserved["runtime_parity"].(map[string]string)
	if parity["csvDataSet.eof"] != "review_required" {
		t.Fatalf("runtime parity = %#v", parity)
	}
}

func TestCSVRecyclingDoesNotGateStopThreadSetting(t *testing.T) {
	const plan = `<?xml version="1.0"?>
<jmeterTestPlan><hashTree><TestPlan testname="Plan"/><hashTree><ThreadGroup testname="Users"/><hashTree>
  <CSVDataSet testname="Recycling rows">
    <stringProp name="filename">rows.csv</stringProp>
    <boolProp name="recycle">true</boolProp>
    <boolProp name="stopThread">true</boolProp>
  </CSVDataSet><hashTree/>
  <HTTPSamplerProxy testname="Request"><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "recycling.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	if len(script.Actions) != 1 || script.Actions[0].Type == "review_required" {
		t.Fatalf("actions = %#v, stopThread cannot trigger without finite EOF", script.Actions)
	}
	if script.Metadata != nil && script.Metadata.Import != nil {
		if parity, ok := script.Metadata.Import.Preserved["runtime_parity"].(map[string]string); ok && parity["csvDataSet.eof"] == "review_required" {
			t.Fatalf("runtime parity = %#v, recycling CSV must not be EOF-gated", parity)
		}
	}
}

func TestJMeterDynamicCounterAndThreadNumberCreateRuntimeParameters(t *testing.T) {
	const plan = `<?xml version="1.0"?>
<jmeterTestPlan><hashTree><TestPlan testname="Plan"/><hashTree><ThreadGroup testname="Users"/><hashTree>
  <HTTPSamplerProxy testname="Request"><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/${__counter(TRUE,checkoutCounter)}/${__threadNum}</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "functions.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	if !strings.Contains(script.Actions[0].Target, "{{param.jmeter_counter_checkoutCounter}}") || !strings.Contains(script.Actions[0].Target, "{{param.jmeter_thread_num}}") {
		t.Fatalf("target = %q, want concrete parameter namespaces", script.Actions[0].Target)
	}
	parameters := map[string]string{}
	for _, parameter := range script.Parameters {
		parameters[parameter.Name] = parameter.Type
	}
	if parameters["jmeter_counter_checkoutCounter"] != "counter" || parameters["jmeter_thread_num"] != "vuser_id" {
		t.Fatalf("parameters = %#v", script.Parameters)
	}
}

func TestJMeterDynamicCounterWithPunctuationCreatesMatchingParameter(t *testing.T) {
	const plan = `<?xml version="1.0"?>
<jmeterTestPlan><hashTree><TestPlan testname="Plan"/><hashTree><ThreadGroup testname="Users"/><hashTree>
  <HTTPSamplerProxy testname="Request"><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/${__counter(TRUE,checkout:counter)}</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "counter-name.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	const name = "jmeter_counter_checkout_counter"
	if !strings.Contains(script.Actions[0].Target, "{{param."+name+"}}") {
		t.Fatalf("target = %q, want sanitized counter template", script.Actions[0].Target)
	}
	for _, parameter := range script.Parameters {
		if parameter.Name == name && parameter.Type == "counter" {
			return
		}
	}
	t.Fatalf("parameters = %#v, want matching %s counter", script.Parameters, name)
}

// __counter(FALSE, ...) is one sequence shared by every thread. It maps to a
// unique-number counter, so values stay distinct across VUsers and the request
// runs.
func TestJMeterCrossThreadCounterMapsToUniqueNumberBlocks(t *testing.T) {
	const plan = `<?xml version="1.0"?>
<jmeterTestPlan><hashTree><TestPlan testname="Plan"/><hashTree><ThreadGroup testname="Users"/><hashTree>
  <HTTPSamplerProxy testname="Request"><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/${__counter(FALSE,shared)}</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
</hashTree></hashTree></hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "global-counter.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	if len(script.Actions) != 1 || !strings.Contains(script.Actions[0].Target, "{{param.jmeter_global_counter_shared}}") {
		t.Fatalf("actions = %#v, want the request alone with the global-counter identity", script.Actions)
	}
	if script.Actions[0].Metadata["execution_blocker"] != nil {
		t.Fatalf("request is still gated: %#v", script.Actions[0].Metadata)
	}
	parity, _ := script.Metadata.Import.Preserved["runtime_parity"].(map[string]string)
	if parity["dynamicFunctions.globalCounter"] != "partial" {
		t.Fatalf("runtime parity = %#v", parity)
	}
	var counter *dsl.Parameter
	for index := range script.Parameters {
		if script.Parameters[index].Name == "jmeter_global_counter_shared" {
			counter = &script.Parameters[index]
		}
	}
	if counter == nil || counter.Type != "counter" || counter.Selection != "unique" || counter.BlockSize != defaultJMeterSharedCounterBlock ||
		counter.Start != 1 || counter.Step != 1 || counter.Update != "each_occurrence" {
		t.Fatalf("parameters = %#v, want a unique-number global counter", script.Parameters)
	}
	noted := false
	for _, item := range append(append(diag.Diagnostics(nil), result.Diagnostics...), result.Unsupported...) {
		if item.Severity == diag.Blocker && strings.Contains(item.Detail, "cross-thread") {
			t.Fatalf("cross-thread counter still blocks: %+v", item)
		}
		if strings.Contains(item.Detail, "became a unique-number counter") && strings.Contains(item.Detail, "1000000 values") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("diagnostics lack the unique-block note: %+v", result.Diagnostics)
	}
}

func TestNestedOnceOnlyControllerIsPreservedAndMappedToInit(t *testing.T) {
	const plan = `<?xml version="1.0"?>
<jmeterTestPlan><hashTree><TestPlan testname="Plan"/><hashTree><ThreadGroup testname="Users"/><hashTree>
  <LoopController testname="Loop"><stringProp name="LoopController.loops">2</stringProp></LoopController><hashTree>
    <OnceOnlyController testname="Once"><boolProp name="enabled">true</boolProp></OnceOnlyController><hashTree>
      <HTTPSamplerProxy testname="Once request"><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/once</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
    </hashTree>
    <HTTPSamplerProxy testname="Each request"><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/each</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/>
  </hashTree>
</hashTree></hashTree></hashTree></jmeterTestPlan>`
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "nested-once.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	if len(script.Init) != 2 || script.Init[0].Type != "review_required" || script.Init[0].Metadata["execution_blocker"] != "import_review_required" || script.Init[1].Target != "/once" {
		t.Fatalf("init = %#v, want durable review gate before preserved nested OnceOnly sampler", script.Init)
	}
	if len(script.Actions) != 1 || script.Actions[0].Control == nil || len(script.Actions[0].Control.Steps) != 1 || script.Actions[0].Control.Steps[0].Target != "/each" {
		t.Fatalf("actions = %#v, want ordinary loop child preserved", script.Actions)
	}
	parity, _ := script.Metadata.Import.Preserved["runtime_parity"].(map[string]string)
	if parity["onceOnlyController"] != "partial" {
		t.Fatalf("runtime parity = %#v, nested OnceOnly hoist must disclose partial ordering fidelity", parity)
	}
	foundSamplerMapping := false
	foundControllerMapping := false
	for _, mapping := range result.SourceReview.Mappings {
		if mapping.Function == "HTTPSamplerProxy" && mapping.TargetName == "Once_request" && mapping.TargetPath == "init[1]" {
			foundSamplerMapping = true
		}
		if mapping.Function == "OnceOnlyController" && mapping.TargetPath == "init[1]" {
			foundControllerMapping = true
		}
	}
	if !foundSamplerMapping || !foundControllerMapping {
		t.Fatalf("source mappings = %#v, want nested sampler and controller mapped behind init gate", result.SourceReview.Mappings)
	}
}
