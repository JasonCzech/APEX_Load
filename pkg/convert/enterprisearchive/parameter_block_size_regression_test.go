package enterprisearchive

import (
	"strings"
	"testing"
)

func TestParseParameterFileSeparatesAutomaticAndExplicitBlockSize(t *testing.T) {
	tests := []struct {
		name         string
		selection    string
		allocation   string
		explicit     string
		wantBlock    int
		wantWarnings int
	}{
		{name: "random ignores allocation and explicit block", selection: "Random", allocation: "3", explicit: "4", wantBlock: 0},
		{name: "sequential ignores automatic allocation", selection: "Sequential", allocation: "1", wantBlock: 0},
		{name: "unique automatic remains unset", selection: "Unique", allocation: "1", explicit: "4", wantBlock: 0, wantWarnings: 0},
		{name: "unique explicit BlockSize", selection: "Unique", allocation: "0", explicit: "4", wantBlock: 4},
		{name: "unique invalid automatic flag is never numeric", selection: "Unique", allocation: "3", wantBlock: 0, wantWarnings: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			blockLine := ""
			if test.explicit != "" {
				blockLine = "\nBlockSize=" + test.explicit
			}
			parsed := ParseParameterFile("params.prm", `[parameter:User]
ParamName=User
Type=Table
ValueList=alice,bob,carol,dave
SelectNextRow=`+test.selection+`
auto_allocate_block_size=`+test.allocation+blockLine+`
`, nil)
			if len(parsed.Parameters) != 1 {
				t.Fatalf("parameters = %#v, want one", parsed.Parameters)
			}
			if test.selection == "Unique" && test.allocation == "1" && !parsed.Parameters[0].AutoAllocate {
				t.Fatal("automatic allocation lost")
			}
			if got := parsed.Parameters[0].BlockSize; got != test.wantBlock {
				t.Fatalf("block_size = %d, want %d", got, test.wantBlock)
			}
			if len(parsed.Warnings) != test.wantWarnings {
				t.Fatalf("warnings = %#v, want %d", parsed.Warnings, test.wantWarnings)
			}
			if test.wantWarnings > 0 && !strings.Contains(parsed.Warnings[0], "block_size was left unset") &&
				!strings.Contains(parsed.Warnings[0], "allocation requires review") {
				t.Fatalf("warning = %q, want automatic-allocation review evidence", parsed.Warnings[0])
			}
		})
	}
}

func TestParseParameterFilePreservesExplicitBlockSizeAliasForUnique(t *testing.T) {
	parsed := ParseParameterFile("params.prm", `[parameter:User]
ParamName=User
Type=Table
ValueList=alice,bob,carol,dave
SelectNextRow=Unique
auto_allocate_block_size=0
block_size=2
`, nil)
	if len(parsed.Parameters) != 1 || parsed.Parameters[0].BlockSize != 2 {
		t.Fatalf("parameters = %#v, want explicit block_size=2", parsed.Parameters)
	}
}
