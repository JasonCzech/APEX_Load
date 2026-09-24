package kafka

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
)

func TestBuildRecordPreservesEmptyKeyAndOmitsSourceProvenance(t *testing.T) {
	k := &Kafka{cfg: config{Partitions: 1}}
	for _, c := range []struct {
		params map[string]any
		want   bool
	}{{nil, false}, {map[string]any{"key": ""}, true}, {map[string]any{"key": "value"}, true}} {
		rec := k.buildRecord(&protocol.Action{Params: c.params, Metadata: map[string]any{"source_node_id": "plan:node", "jmeter_source": "source", "business": "kept"}}, "events")
		if rec.KeyPresent != c.want {
			t.Fatalf("key presence=%v want %v", rec.KeyPresent, c.want)
		}
		if _, ok := rec.Headers["source_node_id"]; ok {
			t.Fatal("source identity leaked onto wire")
		}
		if _, ok := rec.Headers["jmeter_source"]; ok {
			t.Fatal("source metadata leaked onto wire")
		}
		if rec.Headers["business"] != "kept" {
			t.Fatal("existing user metadata changed")
		}
	}
}
