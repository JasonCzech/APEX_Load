package eventhubscapture

import (
	"strings"
	"testing"
)

func TestContainerRejectsNonAvroBeforeDecoder(t *testing.T) {
	for _, input := range []string{"", `{"records":[]}`, "plain text", "O", "Obj", "Obj\x02", "\x00raw datum"} {
		t.Run(input, func(t *testing.T) {
			_, err := openContainer([]byte(input), 10)
			if err == nil || !strings.Contains(err.Error(), "not an Avro Object Container File") || strings.Contains(err.Error(), "map[") {
				t.Fatalf("format error = %v", err)
			}
		})
	}
	_, err := openContainer([]byte("Obj\x01"), 10)
	if err == nil || strings.Contains(err.Error(), "not an Avro Object Container File") {
		t.Fatalf("recognized but truncated header error = %v", err)
	}
}
