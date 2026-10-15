package jmeter

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A renamed test plan declares its identity; JMeter's default "Test Plan"
// does not, so the file name identifies it instead. The thread group names
// the script within the plan.
func TestConvertJMXStampsThePlanAndThreadGroup(t *testing.T) {
	jmx := func(plan string) []byte {
		return []byte(`<jmeterTestPlan version="1.2"><hashTree>
  <TestPlan testname="` + plan + `"/><hashTree>
    <ThreadGroup testname="Buyers"><stringProp name="ThreadGroup.num_threads">1</stringProp></ThreadGroup><hashTree>
      <HTTPSamplerProxy testname="Home">
        <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
        <stringProp name="HTTPSampler.protocol">https</stringProp>
        <stringProp name="HTTPSampler.path">/home</stringProp>
        <stringProp name="HTTPSampler.method">GET</stringProp>
      </HTTPSamplerProxy><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`)
	}
	for _, test := range []struct {
		plan, document string
		declared       bool
	}{
		{"Checkout plan", "Checkout plan", true},
		{"Test Plan", "checkout.jmx", false},
	} {
		result, err := ConvertJMX(jmx(test.plan), ConvertOptions{FileName: "checkout.jmx"})
		if err != nil {
			t.Fatal(err)
		}
		document, item, declared, ok := dsl.ImportSourceIdentityOf(result.Scripts[0].Metadata.Import)
		if !ok || document != test.document || declared != test.declared || item != "Buyers" {
			t.Errorf("plan %q: identity = %q %q declared=%v", test.plan, document, item, declared)
		}
	}
}

// Thread groups that share a name (JMeter's default "Thread Group") are told
// apart by their place among the groups so named, so each script has its own
// identity.
func TestConvertJMXNumbersRepeatedThreadGroupNames(t *testing.T) {
	group := `<ThreadGroup testname="Thread Group"><stringProp name="ThreadGroup.num_threads">1</stringProp></ThreadGroup><hashTree>
      <HTTPSamplerProxy testname="Home">
        <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
        <stringProp name="HTTPSampler.protocol">https</stringProp>
        <stringProp name="HTTPSampler.path">/home</stringProp>
        <stringProp name="HTTPSampler.method">GET</stringProp>
      </HTTPSamplerProxy><hashTree/>
    </hashTree>`
	jmx := []byte(`<jmeterTestPlan version="1.2"><hashTree>
  <TestPlan testname="Checkout plan"/><hashTree>
    ` + group + `
    ` + group + `
  </hashTree>
</hashTree></jmeterTestPlan>`)
	result, err := ConvertJMX(jmx, ConvertOptions{FileName: "checkout.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %d, want one per thread group", len(result.Scripts))
	}
	for index, want := range []string{"Thread Group", "Thread Group #2"} {
		if _, item, _, _ := dsl.ImportSourceIdentityOf(result.Scripts[index].Metadata.Import); item != want {
			t.Errorf("script %d item = %q, want %q", index+1, item, want)
		}
	}
}
