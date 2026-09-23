package generatorrpc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestResourceSnapshotMetricsTransportRoundTrip(t *testing.T) {
	source := &protocol.ErrorSnapshot{RequestURL: "https://example.test/page", Resources: []protocol.ResourceSnapshot{{RequestURL: "https://example.test/resource", ResponseStatus: 200, ResponseBody: "fixture", ResponseBodyState: "captured", CacheHit: true}}}
	wire := snapshotJSON(source)
	var decoded protocol.ErrorSnapshot
	if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Resources) != 1 || decoded.Resources[0].ResponseBody != "fixture" || !decoded.Resources[0].CacheHit {
		t.Fatal(decoded)
	}
	for i := 0; i < 100; i++ {
		source.Resources = append(source.Resources, protocol.ResourceSnapshot{RequestURL: "https://example.test/large", ResponseBody: strings.Repeat("x", 8192)})
	}
	wire = snapshotJSON(source)
	if len(wire) > 32<<10 {
		t.Fatal("metrics cap bypassed")
	}
	if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.OmittedResources == 0 {
		t.Fatal("omitted resources invisible")
	}
}
