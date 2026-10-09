package enterprisearchive

import (
	"strings"
	"testing"
)

func TestRecordingSnapshotNote(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", "Action()\n{\n\tweb_url(\"home\", \"URL=https://shop.test/\", \"Snapshot=t1.inf\", LAST);\n\tweb_url(\"next\", \"URL=https://shop.test/next\", \"Snapshot=t2.inf\", LAST);\n\treturn 0;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	actions := []*ParsedAction{parsed}
	if note := recordingSnapshotNote(map[string]string{"Action.c": ""}, actions); !strings.Contains(note, "no VuGen recording snapshots") || !strings.Contains(note, "2 recorded step(s)") || !strings.Contains(note, "not affected") {
		t.Fatalf("missing snapshots: %q", note)
	}
	if note := recordingSnapshotNote(map[string]string{"Action.c": "", "data/t1.inf": "", "data/t2.inf": ""}, actions); !strings.Contains(note, "2 file(s) under data/") || !strings.Contains(note, "not read yet") {
		t.Fatalf("present snapshots: %q", note)
	}
	plain, _ := ParseActionFile("Action.c", "Action()\n{\n\tweb_url(\"home\", \"URL=https://shop.test/\", LAST);\n\treturn 0;\n}\n")
	if note := recordingSnapshotNote(nil, []*ParsedAction{plain}); note != "" {
		t.Fatalf("a script without snapshot references needs no note: %q", note)
	}
}
