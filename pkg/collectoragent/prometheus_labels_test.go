package collectoragent

import (
	"strings"
	"testing"
)

func TestPrometheusBoundedDimensionSets(t *testing.T) {
	pack, err := parsePrometheusScalarSet("# TYPE rpc_seconds gauge\nrpc_seconds{method=\"get value\",code=\"200\"} 0.02\nrpc_seconds{code=\"500\",method=\"get value\"} NaN\n", true)
	if err != nil || len(pack.Samples) != 2 {
		t.Fatalf("dimensions: %+v %v", pack, err)
	}
	missing := 0
	for key, metadata := range pack.SampleMetadata {
		if metadata.Name != "rpc_seconds" || metadata.Unit != "s" || metadata.Dimensions["method"] != "get value" || len(key) > 256 {
			t.Fatalf("metadata: %+v", metadata)
		}
		if metadata.Quality == "missing" {
			missing++
		}
	}
	if missing != 1 {
		t.Fatalf("missing=%d", missing)
	}
	for _, line := range []string{
		`m{a="1",a="2"} 1`, `m{token="secret"} 1`, `m{a="unterminated} 1`, `m{a="x"b="y"} 1`, `m{a="bad\u0000"} 1`, `m{a="x"}junk 1`, `m{a="` + strings.Repeat("x", 129) + `"} 1`,
	} {
		if _, err := parsePrometheusScalarSet(line, true); err == nil {
			t.Errorf("accepted %q", line)
		}
	}
	if _, err := parsePrometheusScalarSet("# TYPE m gauge\nm{a=\"1\",b=\"2\"} 1\nm{b=\"2\",a=\"1\"} 2\n", true); err == nil {
		t.Fatal("accepted duplicate label identity")
	}
}
