package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFlattenSnapshotHeadersPreservesRepeatedValuesAndBounds(t *testing.T) {
	headers := map[string][]string{"X-Multi": {"one", "two", "three"}, "X-Large": {strings.Repeat("x", 32<<10)}}
	for i := 0; i < 300; i++ {
		headers[fmt.Sprintf("Z-%03d", i)] = []string{"value"}
	}
	got, omitted := FlattenSnapshotHeaders(headers)
	if got["X-Multi"] != "one\ntwo\nthree" {
		t.Fatalf("repeated values = %q", got["X-Multi"])
	}
	if !omitted || len(got["X-Large"]) > 16<<10 || !strings.HasSuffix(got["X-Large"], "...[truncated]") || len(got) > 256 {
		t.Fatalf("header bounds failed: value=%d names=%d", len(got["X-Large"]), len(got))
	}
	headers["X-Multi"][0] = "changed"
	if got["X-Multi"] != "one\ntwo\nthree" {
		t.Fatal("projection aliases source")
	}
}

func TestFlattenGRPCSnapshotMetadataBinaryValues(t *testing.T) {
	raw := map[string][]string{"payload-bin": {string([]byte{0xff, 0x00, 0xfe}), string([]byte{0x80, 0x01})}, "x-plain": {"hello"}}
	headers, omitted := FlattenGRPCSnapshotMetadata(raw)
	if omitted || headers["payload-bin"] != "/wD+\ngAE" || headers["x-plain"] != "hello" {
		t.Fatalf("headers=%#v omitted=%t", headers, omitted)
	}
	raw["payload-bin"] = []string{strings.Repeat("x", 1<<20)}
	headers, omitted = FlattenGRPCSnapshotMetadata(raw)
	if !omitted || len(headers["payload-bin"]) > 16<<10 || !strings.HasSuffix(headers["payload-bin"], "...[truncated]") {
		t.Fatalf("omitted=%t length=%d", omitted, len(headers["payload-bin"]))
	}
}

func TestSnapshotHeaderNormalizationAndCollisions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flatten func(map[string][]string) (map[string]string, bool)
	}{
		{"websocket-fallback", FlattenSnapshotHeaders},
		{"grpc-metadata", FlattenGRPCSnapshotMetadata},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := "X-" + string([]byte{0xfe})
			second := "X-" + string([]byte{0xff})
			headers := map[string][]string{first: {""}, second: {"must-not-overwrite"}, "valid": {"before" + string([]byte{0xff}) + "after"}, "ordinary": {"ok"}}
			for i := 0; i < 20; i++ {
				got, omitted := tc.flatten(headers)
				if !omitted || got["X-�"] != "" || got["valid"] != "before�after" || got["ordinary"] != "ok" || len(got) != 3 {
					t.Fatalf("iteration=%d omitted=%t headers=%#v", i, omitted, got)
				}
				encoded, err := json.Marshal(got)
				if err != nil || !utf8.Valid(encoded) {
					t.Fatalf("json=%q err=%v", encoded, err)
				}
			}
			large := make(map[string][]string)
			for i := 0; i < 300; i++ {
				large[fmt.Sprintf("header-%03d", i)] = []string{strings.Repeat("z", 20<<10)}
			}
			bounded, receipt := tc.flatten(large)
			size := 0
			for key, value := range bounded {
				size += len(key) + len(value)
			}
			if !receipt || len(bounded) > 256 || size > 256<<10 {
				t.Fatalf("receipt=%t names=%d bytes=%d", receipt, len(bounded), size)
			}
			longName := strings.Repeat("n", (4<<10)-1) + string([]byte{0xff})
			input := map[string][]string{longName: {strings.Repeat("v", (16<<10)-3) + string([]byte{0xff}) + "xy"}}
			got, omitted := tc.flatten(input)
			if !omitted || len(got) != 1 {
				t.Fatalf("omitted=%t headers=%#v", omitted, got)
			}
			for key, value := range got {
				if len(key) > 4<<10 || len(value) > 16<<10 || !utf8.ValidString(key) || !utf8.ValidString(value) {
					t.Fatalf("key length=%d value length=%d", len(key), len(value))
				}
			}
		})
	}
}
