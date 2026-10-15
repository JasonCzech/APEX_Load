package asyncapi

import (
	"os"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertStampsTheDeclaredTitleAndBrokerGroup(t *testing.T) {
	content, err := os.ReadFile("testdata/order-event.yaml")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert(ImportRequest{Name: "order-event.yaml", Content: string(content)})
	if err != nil {
		t.Fatal(err)
	}
	for _, script := range result.Scripts {
		document, item, declared, ok := dsl.ImportSourceIdentityOf(script.Metadata.Import)
		if !ok || document != "Synthetic order event fidelity fixture" || !declared || item == "" {
			t.Fatalf("identity = %q %q declared=%v ok=%v", document, item, declared, ok)
		}
	}
}
