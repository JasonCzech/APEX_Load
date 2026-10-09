package enterprisearchive

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func importRunLogicArchive(t *testing.T, usp string) *ConvertResult {
	t.Helper()
	archive := zipFilesForTest(t, map[string]string{
		"S.usr":        "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nvuser_init=vuser_init.c\nBrowse=Browse.c\nBuy=Buy.c\nvuser_end=vuser_end.c\n[RunLogicFiles]\nDefault Profile=default.usp\n",
		"default.cfg":  "[General]\n",
		"default.usp":  usp,
		"vuser_init.c": "vuser_init()\n{\n\treturn 0;\n}\n",
		"vuser_end.c":  "vuser_end()\n{\n\treturn 0;\n}\n",
		"Browse.c":     "Browse()\n{\n\tweb_url(\"browse\", \"URL=https://shop.test/browse\", LAST);\n\tweb_url(\"item\", \"URL=https://shop.test/item\", LAST);\n\treturn 0;\n}\n",
		"Buy.c":        "Buy()\n{\n\tweb_url(\"buy\", \"URL=https://shop.test/buy\", LAST);\n\treturn 0;\n}\n",
	})
	result, err := ConvertZip(archive, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script == nil {
		t.Fatalf("no script: %v", result.Warnings)
	}
	return result
}

func TestImportRunLogicPercentageAndBlocks(t *testing.T) {
	result := importRunLogicArchive(t, `[RunLogicRunRoot]
MercIniTreeFather=""
MercIniTreeSectionName="RunLogicRunRoot"
RunLogicNumOfIterations="10"
RunLogicObjectKind="Group"
RunLogicRunMode="Random"
MercIniTreeSons="Browse,Checkout"
RunLogicPaceType="Asap"
[RunLogicRunRoot:Browse]
MercIniTreeFather="RunLogicRunRoot"
MercIniTreeSectionName="Browse"
RunLogicObjectKind="Action"
RunLogicActionPercentage="70"
[RunLogicRunRoot:Checkout]
MercIniTreeFather="RunLogicRunRoot"
MercIniTreeSectionName="Checkout"
RunLogicObjectKind="Group"
RunLogicRunMode="Sequential"
RunLogicNumOfIterations="2"
MercIniTreeSons="Browse,Buy"
RunLogicActionPercentage="30"
[RunLogicRunRoot:Checkout:Browse]
MercIniTreeFather="RunLogicRunRoot:Checkout"
RunLogicObjectKind="Action"
[RunLogicRunRoot:Checkout:Buy]
MercIniTreeFather="RunLogicRunRoot:Checkout"
RunLogicObjectKind="Action"
`)
	runLogic := result.Script.Config.RunLogic
	if runLogic.Iterations != 10 || len(runLogic.Blocks) != 1 {
		t.Fatalf("run logic = %+v", runLogic)
	}
	root := runLogic.Blocks[0]
	if root.Mode != dsl.RunLogicPercentage || len(root.Children) != 2 || root.Children[0].Action != "Browse" || root.Children[0].Weight != 70 || root.Children[1].Weight != 30 {
		t.Fatalf("root = %+v", root)
	}
	checkout := root.Children[1].Block
	if checkout == nil || checkout.Mode != dsl.RunLogicSequential || checkout.Iterations != 2 || len(checkout.Children) != 2 || checkout.Children[1].Action != "Buy" {
		t.Fatalf("checkout = %+v", checkout)
	}
	groups := dsl.RunLogicActionGroups(result.Script.Actions)
	if len(groups["browse"]) != 2 || len(groups["buy"]) != 1 {
		t.Fatalf("steps must carry their run-logic action: %v", groups)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
		t.Fatalf("the imported run logic must validate: %v", issues)
	}
}

func TestImportRunLogicSingleActionNeedsNoBlocks(t *testing.T) {
	result := importRunLogicArchive(t, `[RunLogicRunRoot]
RunLogicNumOfIterations="1"
RunLogicObjectKind="Group"
RunLogicRunMode="Sequential"
MercIniTreeSons="Browse"
[RunLogicRunRoot:Browse]
RunLogicObjectKind="Action"
`)
	if len(result.Script.Config.RunLogic.Blocks) != 0 {
		t.Fatalf("a single-action run logic needs no blocks: %+v", result.Script.Config.RunLogic.Blocks)
	}
}

func TestImportRunLogicRandomWithoutPercentagesIsUniform(t *testing.T) {
	result := importRunLogicArchive(t, `[RunLogicRunRoot]
RunLogicNumOfIterations="1"
RunLogicObjectKind="Group"
RunLogicRunMode="Random"
MercIniTreeSons="Browse,Buy"
[RunLogicRunRoot:Browse]
RunLogicObjectKind="Action"
[RunLogicRunRoot:Buy]
RunLogicObjectKind="Action"
`)
	blocks := result.Script.Config.RunLogic.Blocks
	if len(blocks) != 1 || blocks[0].Mode != dsl.RunLogicRandom {
		t.Fatalf("blocks = %+v", blocks)
	}
}
