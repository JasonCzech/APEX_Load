package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"
)

// Recorded pages routinely list more than 64 EXTRARES entries.
func TestExtraResourcesUpToThePageLimitAreNotGated(t *testing.T) {
	var items strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&items, "\t\t\"Url=/static/r%d.js\", ENDITEM,\n", i)
	}
	source := "Action()\n{\n\tweb_url(\"home\", \"URL=https://example.test/\", EXTRARES,\n" + items.String() + "\t\tLAST);\n\treturn 0;\n}\n"
	parsed, err := ParseActionFile("Action.c", source)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "Resources"})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	action := result.Script.Actions[0]
	if resources, _ := action.Params["resources"].([]any); len(resources) != 100 {
		t.Fatalf("resources = %d, want 100", len(resources))
	}
	if blocker, _ := action.Metadata["execution_blocker"].(string); blocker != "" {
		t.Fatalf("execution_blocker = %q, want no gate under the page limit", blocker)
	}
}
