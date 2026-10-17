package enterprisearchive

import (
	"strings"
	"testing"
)

func TestParseParameterFileAllocatesUniqueRowsWithoutAllocationKeys(t *testing.T) {
	for name, allocationLines := range map[string]string{
		"no allocation keys":            "",
		"explicit block flag, no block": "auto_allocate_block_size=0\n",
	} {
		t.Run(name, func(t *testing.T) {
			parsed := ParseParameterFile("params.prm", "[parameter:Account]\nParamName=Account\nType=Table\nValueList=A1,A2,A3,A4,A5,A6\nSelectNextRow=Unique\n"+allocationLines, nil)
			if len(parsed.Parameters) != 1 {
				t.Fatalf("parameters = %#v, want one", parsed.Parameters)
			}
			param := parsed.Parameters[0]
			if !param.AutoAllocate || param.BlockSize != 0 || param.Selection != "unique" {
				t.Fatalf("parameter = %+v, want automatic unique allocation", param)
			}
			if len(parsed.Warnings) != 1 || !strings.Contains(parsed.Warnings[0], "allocated automatically per VUser") {
				t.Fatalf("warnings = %#v, want one allocation note", parsed.Warnings)
			}
		})
	}
}

func TestTruClientUniqueParameterWithoutAllocationKeysImportsAutoAllocated(t *testing.T) {
	result, err := ConvertZip(truClientReviewZIP(t), ConvertOptions{PreserveTransactions: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Script == nil || len(result.Script.Parameters) != 1 {
		t.Fatalf("script parameters missing: %+v", result.Script)
	}
	if param := result.Script.Parameters[0]; !param.AutoAllocate || param.BlockSize != 0 {
		t.Fatalf("AccountID = %+v, want automatic allocation", param)
	}
	found := false
	for _, warning := range result.Warnings {
		found = found || strings.Contains(warning, "parameter accountid uses Unique selection without a block size")
	}
	if !found {
		t.Fatalf("warnings = %#v, want the allocation note", result.Warnings)
	}
}
