package duckdb

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestWorkerIdentityCollectionBoundsBeforeTypedDecode(t *testing.T) {
	b := workerTestBootstrap(t)
	request := workerTestRequest(b)
	request.Normalize.Mapping.Version = 2
	request.Normalize.Mapping.Identity = &analysis.ParquetIdentityMapping{Source: Column{"source"}, Dimensions: []analysis.ParquetDimensionMapping{{Name: "group", Value: Column{"group"}}}}
	frame := workerFrame{Identity: b.Identity, Kind: "request", Request: &request}
	if request.validate(b) != nil {
		t.Fatal("bounded v2 identity mapping rejected")
	}
	body, err := marshalWorkerJSON(frame)
	if err != nil {
		t.Fatal(err)
	}
	var decoded workerFrame
	if decodeWorkerJSON(body, &decoded) != nil || decoded.Request.validate(b) != nil {
		t.Fatal("identity map lost through IPC")
	}
	decoded.Request.Normalize.Mapping.Identity.Source[0] = "changed"
	if request.Normalize.Mapping.Identity.Source[0] != "source" {
		t.Fatal("decoded source aliases parent request")
	}
	// Build lexical shape using encoding/json solely in the hostile fixture. The
	// production decoder must reject 17 labels and 17 path components before the
	// typed destination changes; the raw frame itself remains within 8 MiB.
	for _, kind := range []string{"dimensions", "host_path"} {
		t.Run(kind, func(t *testing.T) {
			copy := request
			mapping := copy.Normalize.Mapping
			identity := *mapping.Identity
			if kind == "dimensions" {
				identity.Dimensions = make([]analysis.ParquetDimensionMapping, 17)
				for i := range identity.Dimensions {
					identity.Dimensions[i] = analysis.ParquetDimensionMapping{Name: string(rune('a' + i)), Value: Column{"group"}}
				}
			} else {
				identity.Host = make(Column, 17)
				for i := range identity.Host {
					identity.Host[i] = "host"
				}
			}
			mapping.Identity = &identity
			op := *copy.Normalize
			op.Mapping = mapping
			copy.Normalize = &op
			raw, _ := json.Marshal(workerFrame{Identity: b.Identity, Kind: "request", Request: &copy})
			destination := workerFrame{Kind: "sentinel"}
			before := destination
			if decodeWorkerJSON(raw, &destination) == nil || !reflect.DeepEqual(destination, before) {
				t.Fatal("excessive identity collection allocated/changed destination")
			}
		})
	}
	frame.Request.Normalize.Mapping.Identity.Dimensions[0].Name = strings.Repeat("x", 129)
	if frame.Request.validate(b) == nil {
		t.Fatal("identity dimension key escaped semantic bound")
	}
}
