package engine

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"strings"
	"testing"
)

func TestReplayHARBodyRedactionAndSnapshotBounds(t *testing.T) {
	h := protocol.NewReplayHARExchange()
	h.RequestBody = &protocol.ReplayHARBody{Text: `{"password":"private-value"}`, State: "retained"}
	h.ResponseBody = &protocol.ReplayHARBody{Text: strings.Repeat("x", 2048), State: "retained"}
	entries := scrubSensitiveNetworkEntries([]protocol.NetworkEntry{{HAR: h}}, nil)
	if strings.Contains(entries[0].HAR.RequestBody.Text, "private-value") {
		t.Fatal("body not scrubbed")
	}
	if h.RequestBody.Text != `{"password":"private-value"}` {
		t.Fatal("scrub mutated source")
	}
	remaining := 1100
	bounded := takeSnapshotHAR(h, &remaining)
	if bounded.ResponseBody.Text != "" || bounded.ResponseBody.State != "total_limit" || remaining < 0 {
		t.Fatal("body escaped snapshot budget")
	}
}

func TestReplayHARBodiesApplyRuntimeAndCustomRedaction(t *testing.T) {
	base, err := NewRedactionConfig([]RedactionRule{{Pattern: "custom-private-value"}})
	if err != nil {
		t.Fatal(err)
	}
	config := WithSecretValueRedaction(base, []string{"runtime-private-value"})
	h := protocol.NewReplayHARExchange()
	h.ResponseBody = &protocol.ReplayHARBody{State: "retained", Text: "body runtime-private-value custom-private-value"}
	e := scrubSensitiveNetworkEntries([]protocol.NetworkEntry{{HAR: h}}, config)
	if strings.Contains(e[0].HAR.ResponseBody.Text, "runtime-private-value") || strings.Contains(e[0].HAR.ResponseBody.Text, "custom-private-value") {
		t.Fatal("runtime secret leaked")
	}
}
