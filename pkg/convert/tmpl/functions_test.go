package tmpl

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

func TestTranslateDynamicJMeterFunctions(t *testing.T) {
	var diagnostics diag.Collector
	got := TranslateDynamic(`/orders/${__UUID()}?ts=${__time()}&n=${__Random(10,20)}&s=${__RandomString(8,abcdef,seed)}&c=${__counter(FALSE,checkoutCounter)}&t=${__threadNum}`, &diagnostics, "sampler")
	want := `/orders/{{fn.uuid()}}?ts={{fn.timestamp.unix_ms}}&n={{fn.random(10,20)}}&s={{fn.random_string(8)}}&c={{param.jmeter_global_counter_checkoutCounter}}&t={{param.jmeter_thread_num}}`
	if got != want {
		t.Fatalf("TranslateDynamic() = %q, want %q", got, want)
	}
	if len(diagnostics.Items()) != 1 || diagnostics.Items()[0].Severity != diag.Warning {
		t.Fatalf("diagnostics = %#v, want cross-thread counter review warning", diagnostics.Items())
	}
}

func TestTranslateDynamicSanitizesCounterNamesForParameterBinding(t *testing.T) {
	got := TranslateDynamic(`${__counter(TRUE,checkout:counter)}`, nil, "")
	if want := `{{param.jmeter_counter_checkout_counter}}`; got != want {
		t.Fatalf("TranslateDynamic() = %q, want %q", got, want)
	}
}

func TestTranslateDynamicLeavesUnknownJMeterFunctionWithDiagnostic(t *testing.T) {
	var diagnostics diag.Collector
	got := TranslateDynamic(`${__groovy(vars.get("token"))}`, &diagnostics, "sampler")
	if !strings.Contains(got, "__groovy") {
		t.Fatalf("TranslateDynamic() = %q, want original function preserved", got)
	}
	if len(diagnostics.Items()) != 1 || diagnostics.Items()[0].Severity != diag.Warning {
		t.Fatalf("diagnostics = %#v, want one warning", diagnostics.Items())
	}
}

func TestTranslateDynamicPostmanDynamicVariables(t *testing.T) {
	got := TranslateDynamic(`/orders/{{$guid}}/{{$timestamp}}/{{$isoTimestamp}}/{{$randomInt}}/{{$randomFirstName}}`, nil, "")
	want := `/orders/{{fn.uuid()}}/{{fn.timestamp.unix}}/{{fn.timestamp.iso8601}}/{{fn.random(0,1000)}}/{{fn.random_string(12)}}`
	if got != want {
		t.Fatalf("TranslateDynamic() = %q, want %q", got, want)
	}
}
