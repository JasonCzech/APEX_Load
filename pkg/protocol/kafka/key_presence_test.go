package kafka

import (
	"encoding/base64"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
	"os"
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

func TestDIKafkaRecordMatchesPinnedJavaKeys(t *testing.T) {
	data, err := os.ReadFile("testdata/di_kafka_key_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Vectors [][]json.RawMessage `json:"vectors"`
	}
	if err = json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if len(oracle.Vectors) != 4 {
		t.Fatal("incomplete key vectors")
	}
	k := &Kafka{sharedProducer: true, cfg: config{Partitions: 3}}
	for _, v := range oracle.Vectors {
		var encoded string
		var present bool
		var partition int
		if len(v) != 3 {
			t.Fatal("invalid key vector")
		}
		if err = json.Unmarshal(v[0], &encoded); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(v[1], &present); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(v[2], &partition); err != nil {
			t.Fatal(err)
		}
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		record := k.buildRecord(&protocol.Action{Params: map[string]any{"key": string(key), "partition": partition}}, "synthetic-topic")
		if record.KeyPresent != present || record.Key != string(key) || record.Partition != int32(partition) || !record.ManualPartition {
			t.Fatal("record differs from pinned DI sampler")
		}
	}
}
func TestNativeKafkaWhitespaceKeyIsNotCollapsed(t *testing.T) {
	k := &Kafka{cfg: config{Partitions: 1}}
	record := k.buildRecord(&protocol.Action{Params: map[string]any{"key": " "}}, "topic")
	if !record.KeyPresent || record.Key != " " {
		t.Fatal("whitespace key changed")
	}
}
