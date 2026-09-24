package jmeter

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"os"
	"strings"
	"testing"
)

func splitPayFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/split_pay_" + name + ".jmx")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestSplitPayFidelity(t *testing.T) {
	r, e := ConvertJMX(splitPayFixture(t, "original"), ConvertOptions{FileName: "split.jmx", IncludeAssertions: true, ScenarioDraft: true})
	if e != nil {
		t.Fatal(e)
	}
	s := r.Scripts[0]
	if s.Config.BaseURL != "https://api.example.test:443" {
		t.Fatalf("endpoint=%s", s.Config.BaseURL)
	}
	if !hasJMeterReviewReason(s.Actions, "jmeter_timestamp_timezone_required") || !hasJMeterReviewReason(s.Actions, "import_missing_variables") {
		t.Fatalf("gates=%+v", s.Actions[0].Metadata)
	}
	if strings.Contains(s.Actions[0].Body, "fn.timestamp.iso8601") {
		t.Fatal("timestamp approximated")
	}
	if r.Stats.Unsupported != 0 {
		t.Fatalf("unsupported=%v", r.UnsupportedElements)
	}
	if len(s.Config.Results.CSVExports) != 1 {
		t.Fatalf("exports=%+v", s.Config.Results.CSVExports)
	}
	ext := s.Actions[0].Extractors[0]
	if ext.DefaultValue == nil || *ext.DefaultValue != "NOT_FOUND" || ext.OnMiss != "default" {
		t.Fatalf("extractor=%+v", ext)
	}
	for _, m := range r.SourceReview.Mappings {
		if m.Function == "jmeter_extractor" && m.StartLine != 154 {
			t.Fatalf("extractor mapping=%+v", m)
		}
		if m.Function == "jmeter_assertion" && m.StartLine != 144 {
			t.Fatalf("assertion mapping=%+v", m)
		}
	}
	if r.Stats.ReviewRequired == 0 {
		t.Fatal("missing review accounting")
	}
	repaired, e := ConvertJMX(splitPayFixture(t, "repaired"), ConvertOptions{TimeMode: "utc", IncludeAssertions: true})
	if e != nil {
		t.Fatal(e)
	}
	s = repaired.Scripts[0]
	if issues := dsl.ValidateScriptIssues(s); len(issues) > 0 {
		t.Fatal(issues)
	}
	if s.Actions[0].Metadata["execution_blocker"] != nil {
		t.Fatalf("repaired blocked: %+v", s.Actions[0].Metadata)
	}
	if len(s.Parameters) != 1 || s.Parameters[0].Format != "2006-01-02T15:04:05.000Z" || s.Parameters[0].Timezone != "UTC" {
		t.Fatalf("parameters=%+v", s.Parameters)
	}
}
func TestSplitPayRejectsPartialCSVWriter(t *testing.T) {
	for _, extra := range []string{"\nSystem.exit(0)", "\nvars.put('extra','value')"} {
		b := strings.Replace(string(splitPayFixture(t, "repaired")), "\n}</stringProp>", "\n}"+extra+"</stringProp>", 1)
		r, e := ConvertJMX([]byte(b), ConvertOptions{TimeMode: "utc"})
		if e != nil {
			t.Fatal(e)
		}
		if len(r.Scripts[0].Config.Results.CSVExports) != 0 || !hasJMeterReviewReason(r.Scripts[0].Actions, "unsupported_jmeter_processor") {
			t.Fatalf("partial source escaped gate: exports=%+v metadata=%+v", r.Scripts[0].Config.Results.CSVExports, r.Scripts[0].Actions[0].Metadata)
		}
	}
}
func TestJavaTimeLayouts(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		ok          bool
	}{{"yyyy-MM-dd", "2006-01-02", true}, {"HH:mm:ss.SSS", "15:04:05.000", true}, {"yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", "2006-01-02T15:04:05.000Z", true}, {"EEEE", "", false}, {"yyyy-'", "", false}} {
		got, ok := javaTimeLayout(tc.input)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s => %q %t", tc.input, got, ok)
		}
	}
}
func TestNestedScopedEndpointPreserved(t *testing.T) {
	b := `<jmeterTestPlan><hashTree><TestPlan/><hashTree><ThreadGroup testname="users"/><hashTree><TransactionController testname="nested"/><hashTree><HTTPSamplerProxy testname="one"><stringProp name="HTTPSampler.domain">one.example</stringProp><stringProp name="HTTPSampler.path">/one</stringProp></HTTPSamplerProxy><hashTree><ResponseAssertion><stringProp name="Assertion.test_field">Assertion.response_code</stringProp><collectionProp name="Asserion.test_strings"><stringProp name="0">200</stringProp></collectionProp></ResponseAssertion><hashTree/></hashTree></hashTree><HTTPSamplerProxy testname="two"><stringProp name="HTTPSampler.domain">two.example</stringProp><stringProp name="HTTPSampler.path">/two</stringProp></HTTPSamplerProxy><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`
	r, e := ConvertJMX([]byte(b), ConvertOptions{})
	if e != nil {
		t.Fatal(e)
	}
	s := r.Scripts[0]
	if s.Config.BaseURL != "https://one.example" || s.Actions[1].Target != "https://two.example/two" {
		t.Fatalf("script=%+v", s)
	}
}

func TestSplitPayBindingRepairKeepsCredentialAndTimezoneGates(t *testing.T) {
	r, err := ConvertJMX(splitPayFixture(t, "original"), ConvertOptions{VariableBindings: map[string]string{"client_id": "cid", "client_secret": "cs"}})
	if err != nil {
		t.Fatal(err)
	}
	if !hasJMeterReviewReason(r.Scripts[0].Actions, "jmeter_timestamp_timezone_required") {
		t.Fatal("binding repair cleared timezone gate")
	}
	if r.Scripts[0].Actions[0].Metadata["execution_blocker"] == nil {
		t.Fatal("masked credentials became runnable")
	}
}

func TestMaskedCredentialsRemainBlockedAfterTimezoneRepair(t *testing.T) {
	r, err := ConvertJMX(splitPayFixture(t, "original"), ConvertOptions{TimeMode: "utc", VariableBindings: map[string]string{"client_id": "cid", "client_secret": "cs"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Scripts[0].Actions[0].Metadata["execution_blocker"] == nil {
		t.Fatalf("masked source runnable: %+v", r.Scripts[0].Actions[0])
	}
}

func TestTimePoliciesRejectUnsupportedAndPreserveSelectedZone(t *testing.T) {
	for _, pattern := range []string{"${__time(EEEE,)}", "${__time(${format},)}", "${__timeShift(yyyy-MM-dd,,P1D,,)}", "${__time(SSS,)}", "${__time('Mon',)}"} {
		source := strings.Replace(string(splitPayFixture(t, "repaired")), "${__time(yyyy-MM-dd,)}T${__time(HH:mm:ss.SSS,)}Z", pattern, 1)
		result, err := ConvertJMX([]byte(source), ConvertOptions{TimeMode: "utc"})
		if err != nil {
			t.Fatal(err)
		}
		if result.Scripts[0].Actions[0].Metadata["execution_blocker"] == nil {
			t.Fatalf("unsupported time runnable: %s", pattern)
		}
	}
	result, err := ConvertJMX(splitPayFixture(t, "repaired"), ConvertOptions{TimeMode: "source", Timezone: "America/New_York"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	if len(script.Parameters) != 2 || script.Parameters[0].Timezone != "America/New_York" || script.Parameters[1].Format != "15:04:05.000" || script.Actions[0].Metadata["execution_blocker"] != nil {
		t.Fatalf("source repair=%+v", script)
	}
	if _, err := ConvertJMX(splitPayFixture(t, "repaired"), ConvertOptions{TimeMode: "source"}); err == nil {
		t.Fatal("source zone was guessed")
	}
}

func TestCSVExportSurvivesNestedController(t *testing.T) {
	source := string(splitPayFixture(t, "repaired"))
	sampler := strings.Index(source, "        <HTTPSamplerProxy")
	end := strings.Index(source, "        <ResultCollector")
	if sampler < 0 || end < 0 {
		t.Fatal("fixture structure changed")
	}
	source = source[:sampler] + `<TransactionController testname="wrapper"/><hashTree>` + source[sampler:end] + `</hashTree>` + source[end:]
	result, err := ConvertJMX([]byte(source), ConvertOptions{TimeMode: "utc", IncludeAssertions: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts[0].Config.Results.CSVExports) != 1 {
		t.Fatal("nested export lost")
	}
	if issues := dsl.ValidateScriptIssues(result.Scripts[0]); len(issues) > 0 {
		t.Fatal(issues)
	}
}

func TestSplitPayExplicitEndpointOverride(t *testing.T) {
	result, err := ConvertJMX(splitPayFixture(t, "repaired"), ConvertOptions{TimeMode: "utc", BaseURL: "http://127.0.0.1:12345"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	if script.Config.BaseURL != "http://127.0.0.1:12345" || strings.HasPrefix(script.Actions[0].Target, "http") {
		t.Fatalf("override did not retarget first endpoint: base=%s target=%s", script.Config.BaseURL, script.Actions[0].Target)
	}
}

func TestCSVExportsRetainUniqueActionIdentity(t *testing.T) {
	source := string(splitPayFixture(t, "repaired"))
	start, end := strings.Index(source, "        <HTTPSamplerProxy"), strings.Index(source, "        <ResultCollector")
	block := source[start:end]
	source = source[:end] + strings.ReplaceAll(block, "traceId-results.csv", "traceId-second.csv") + source[end:]
	result, err := ConvertJMX([]byte(source), ConvertOptions{TimeMode: "utc"})
	if err != nil {
		t.Fatal(err)
	}
	script := result.Scripts[0]
	exports := script.Config.Results.CSVExports
	if len(exports) != 2 || exports[0].ActionName == exports[1].ActionName || exports[0].ActionName != script.Actions[0].Name || exports[1].ActionName != script.Actions[1].Name {
		t.Fatalf("exports=%+v actions=%+v", exports, script.Actions)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) > 0 {
		t.Fatal(issues)
	}
}
