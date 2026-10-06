package enterprisearchive

import (
	"strings"
	"testing"
)

func runLogicArchive(t *testing.T, usp string) []byte {
	t.Helper()
	action := func(name, url string) string {
		return name + "()\n{\n\tweb_url(\"" + name + "\", \"URL=https://example.test/" + url + "\", LAST);\n\treturn 0;\n}\n"
	}
	return zipFilesForTest(t, map[string]string{
		"Flow/Flow.usr": "[General]\nType=Multi\nActiveTypes=QTWeb\nDefaultCfg=default.cfg\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nOptional=Optional.c\nvuser_end=vuser_end.c\n" +
			"[CfgFiles]\nDefault Profile=default.cfg\n[RunLogicFiles]\nDefault Profile=default.usp\n",
		"Flow/default.cfg":  "[General]\nDefaultRunLogic=default.usp\n",
		"Flow/default.usp":  usp,
		"Flow/vuser_init.c": "vuser_init()\n{\n\treturn 0;\n}\n",
		"Flow/Action.c":     action("Action", "main"),
		"Flow/Optional.c":   action("Optional", "optional"),
		"Flow/vuser_end.c":  "vuser_end()\n{\n\treturn 0;\n}\n",
	})
}

func importedTargets(t *testing.T, archive []byte) ([]string, []string) {
	t.Helper()
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip: %v", err)
	}
	var targets []string
	for _, action := range result.Script.Actions {
		if action.Target != "" {
			targets = append(targets, action.Target)
		}
	}
	return targets, result.Warnings
}

func TestConvertZipSkipsActionsTheRunLogicNeverRuns(t *testing.T) {
	targets, warnings := importedTargets(t, runLogicArchive(t, `[Profile Actions]
Profile Actions name=vuser_init,Action,Optional,vuser_end
[RunLogicRunRoot]
Name="Run"
RunLogicActionOrder="Action"
RunLogicObjectKind="Group"
RunLogicRunMode="Sequential"
RunLogicNumOfIterations="1"
[RunLogicRunRoot:Action]
Name="Action"
RunLogicObjectKind="Action"
`))
	if len(targets) != 1 || !strings.HasSuffix(targets[0], "/main") {
		t.Fatalf("targets = %v, want only the scheduled Action request", targets)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "The run logic never runs Optional") {
		t.Fatalf("warnings = %#v, want the unscheduled-action warning", warnings)
	}
}

func TestConvertZipKeepsActionsScheduledInsideRunBlocks(t *testing.T) {
	targets, warnings := importedTargets(t, runLogicArchive(t, `[RunLogicRunRoot]
Name="Run"
RunLogicActionOrder="Block0"
RunLogicObjectKind="Group"
[RunLogicRunRoot:Block0]
Name="Block0"
RunLogicActionOrder="Action,Optional"
RunLogicObjectKind="Group"
RunLogicRunMode="Random"
[RunLogicRunRoot:Block0:Action]
Name="Action"
RunLogicObjectKind="Action"
[RunLogicRunRoot:Block0:Optional]
Name="Optional"
RunLogicObjectKind="Action"
`))
	if len(targets) != 2 {
		t.Fatalf("targets = %v, want both block actions", targets)
	}
	if strings.Contains(strings.Join(warnings, "\n"), "The run logic never runs") {
		t.Fatalf("warnings = %#v, want no unscheduled-action warning", warnings)
	}
}
