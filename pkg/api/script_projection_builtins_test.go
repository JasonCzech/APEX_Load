package api

import "testing"

func TestPortableZeroArgumentBuiltinReferences(t *testing.T) {
	for _, expr := range []string{"fn.uuid", "fn.uuid.v4", "fake.uuid", "fake.email", "fn.timestamp.unix", "fn.timestamp.unix_ms", "fn.timestamp.iso8601", "fn.timestamp.rfc3339"} {
		for _, suffix := range []string{"", "()"} {
			if !isPortableScriptTemplateReference("{{" + expr + suffix + "}}") {
				t.Errorf("rejected %q", expr+suffix)
			}
		}
	}
	for _, expr := range []string{"fn.now()", "fn.timestamp.unix(1)"} {
		if isPortableScriptTemplateReference("{{" + expr + "}}") {
			t.Errorf("accepted %q", expr)
		}
	}
}
