package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"
)

func TestJavaAnalysisSourceLimitsFailClosed(t *testing.T) {
	t.Run("per_file", func(t *testing.T) {
		class, err := parseJavaFile(
			"Actions.java",
			strings.Repeat(" ", maxJavaAnalysisFileBytes+1),
		)
		if err == nil || !strings.Contains(err.Error(), "file-size limit") {
			t.Fatalf("parseJavaFile error = %v, want file-size limit", err)
		}
		if len(class.Unparsed) != 1 {
			t.Fatalf("unparsed regions = %d, want fail-closed marker", len(class.Unparsed))
		}
	})

	t.Run("aggregate", func(t *testing.T) {
		total := 0
		for index := 0; index < 4; index++ {
			next, reason := reserveJavaAnalysisSource(
				fmt.Sprintf("Part%d.java", index),
				maxJavaAnalysisFileBytes,
				total,
			)
			if reason != "" {
				t.Fatalf("reserve part %d: %s", index, reason)
			}
			total = next
		}
		next, reason := reserveJavaAnalysisSource("Overflow.java", 1, total)
		if next != total || !strings.Contains(reason, "aggregate analysis limit") {
			t.Fatalf("reserve overflow = (%d, %q), want total %d and aggregate limit", next, reason, total)
		}
	})
}

func TestJavaAnalysisRejectsExcessiveNestingBeforeStatementRecursion(t *testing.T) {
	source := `public class Actions { public int action() {` +
		strings.Repeat("{", maxJavaAnalysisNesting+1) +
		strings.Repeat("}", maxJavaAnalysisNesting+1) +
		`return 0; } }`
	class, err := parseJavaFile("Actions.java", source)
	if err == nil || !strings.Contains(err.Error(), "nesting limit") {
		t.Fatalf("parseJavaFile error = %v, want nesting limit", err)
	}
	if len(class.Unparsed) != 1 {
		t.Fatalf("unparsed regions = %d, want fail-closed marker", len(class.Unparsed))
	}
}

func TestJavaAnalysisRejectsUnmatchedOuterBraces(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "missing_class_close",
			source: `public class Actions { public int action() { return 0; }`,
			want:   "unmatched opening brace",
		},
		{
			name:   "stray_close",
			source: `public class Actions { public int action() { return 0; } } }`,
			want:   "unmatched closing brace",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			class, err := parseJavaFile("Actions.java", test.source)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseJavaFile error = %v, want %q", err, test.want)
			}
			if len(class.Unparsed) != 1 {
				t.Fatalf("unparsed regions = %d, want fail-closed marker", len(class.Unparsed))
			}
		})
	}
}

func TestJavaAnalysisCapsFlatBraceMetadata(t *testing.T) {
	source := `public class Actions { public int action() {` +
		strings.Repeat("{}", maxJavaAnalysisBraceTokens/2+1) +
		`return 0; } }`
	class, err := parseJavaFile("Actions.java", source)
	if err == nil || !strings.Contains(err.Error(), "brace-metadata limit") {
		t.Fatalf("parseJavaFile error = %v, want brace-metadata limit", err)
	}
	if len(class.Unparsed) != 1 {
		t.Fatalf("unparsed regions = %d, want fail-closed marker", len(class.Unparsed))
	}
}

func TestJavaAnalysisRejectsExcessiveMethodWorkBeforeBodyParsing(t *testing.T) {
	var source strings.Builder
	source.WriteString("public class Actions {\n")
	for index := 0; index <= maxJavaAnalysisMethodCount; index++ {
		fmt.Fprintf(&source, "void method%d() {}\n", index)
	}
	source.WriteString("}\n")

	class, err := parseJavaFile("Actions.java", source.String())
	if err == nil || !strings.Contains(err.Error(), "method limit") {
		t.Fatalf("parseJavaFile error = %v, want method limit", err)
	}
	if len(class.Unparsed) != 1 {
		t.Fatalf("unparsed regions = %d, want fail-closed marker", len(class.Unparsed))
	}
}

func TestJavaAnalysisBoundsManyTopLevelClassScans(t *testing.T) {
	var source strings.Builder
	const importCount = 256
	for index := 0; index < importCount; index++ {
		fmt.Fprintf(&source, "import example.pkg%d.Type%d;\n", index, index)
	}
	source.WriteString("public class Actions { public int action() { return 0; } }\n")
	for index := 1; index < maxJavaAnalysisTypeCount; index++ {
		fmt.Fprintf(&source, "class Helper%d {}\n", index)
	}
	content := source.String()
	context := newJavaParseContext(content)
	selected, err := parseJavaFileWithContext("Actions.java", content, context)
	if err != nil {
		t.Fatalf("parse selected class: %v", err)
	}
	if selected.Name != "Actions" {
		t.Fatalf("selected class = %q, want Actions", selected.Name)
	}
	auxiliary, warnings := parseJavaAuxiliaryClassesWithContext(
		"Actions.java",
		content,
		context,
	)
	if len(warnings) != 0 {
		t.Fatalf("auxiliary warnings = %v", warnings)
	}
	if len(auxiliary) != maxJavaAnalysisTypeCount-1 {
		t.Fatalf(
			"auxiliary classes = %d, want %d",
			len(auxiliary),
			maxJavaAnalysisTypeCount-1,
		)
	}
	if len(selected.Imports) != importCount {
		t.Fatalf("selected imports = %d, want %d", len(selected.Imports), importCount)
	}
	for index, class := range auxiliary {
		if len(class.Imports) != importCount {
			t.Fatalf("auxiliary %d imports = %d, want %d", index, len(class.Imports), importCount)
		}
		if &class.Imports[0] != &selected.Imports[0] {
			t.Fatalf("auxiliary %d cloned immutable file import metadata", index)
		}
	}
	if context.structuralScanBytes > len(content)*9 {
		t.Fatalf(
			"structural scan work = %d bytes for %d-byte source; want interval-bounded work",
			context.structuralScanBytes,
			len(content),
		)
	}
}

func TestJavaAnalysisBoundsAndDeduplicatesImports(t *testing.T) {
	t.Run("limit", func(t *testing.T) {
		var source strings.Builder
		for index := 0; index <= maxJavaAnalysisImportCount; index++ {
			fmt.Fprintf(&source, "import example.pkg%d.Type%d;\n", index, index)
		}
		source.WriteString("public class Actions { public int action() { return 0; } }\n")
		class, err := parseJavaFile("Actions.java", source.String())
		if err == nil || !strings.Contains(err.Error(), "import limit") {
			t.Fatalf("parseJavaFile error = %v, want import limit", err)
		}
		if len(class.Unparsed) != 1 {
			t.Fatalf("unparsed regions = %d, want fail-closed marker", len(class.Unparsed))
		}
	})

	t.Run("dedupe", func(t *testing.T) {
		class, err := parseJavaFile("Actions.java", `import java.util.List;
import java.util.List;
public class Actions { public int action() { return 0; } }`)
		if err != nil {
			t.Fatalf("parseJavaFile: %v", err)
		}
		if len(class.Imports) != 1 || class.Imports[0] != "java.util.List" {
			t.Fatalf("imports = %#v, want one deduplicated import", class.Imports)
		}
	})
}

func TestJavaAnalysisArchiveQuotasSpanFiles(t *testing.T) {
	var first strings.Builder
	first.WriteString("public class Actions { public int action() { return 0; } }\n")
	for index := 1; index < maxJavaAnalysisTypeCount/2; index++ {
		fmt.Fprintf(&first, "class First%d {}\n", index)
	}
	var second strings.Builder
	for index := 0; index <= maxJavaAnalysisTypeCount/2; index++ {
		fmt.Fprintf(&second, "class Second%d {}\n", index)
	}

	program, warnings := parseJavaProgram(map[string]string{
		"Actions.java": first.String(),
		"Second.java":  second.String(),
	})
	if !strings.Contains(strings.Join(warnings, "\n"), "aggregate retained-type limit") {
		t.Fatalf("warnings = %v, want aggregate retained-type limit", warnings)
	}
	if len(program.Unparsed) == 0 {
		t.Fatal("aggregate quota must leave fail-closed unparsed evidence")
	}
	if len(program.Classes) > maxJavaAnalysisTypeCount+1 {
		t.Fatalf("retained classes = %d, want at most quota plus one marker", len(program.Classes))
	}
}

func TestJavaAnalysisArchiveBudgetCoversMethodsStatementsAndParserWork(t *testing.T) {
	t.Run("methods", func(t *testing.T) {
		budget := javaProgramAnalysisBudget{methods: maxJavaAnalysisMethodCount - 1}
		class := javaClass{Constructors: []javaMethod{{}, {}}}
		if reason := budget.reserveClass(class); !strings.Contains(reason, "retained-method limit") {
			t.Fatalf("reserveClass reason = %q, want retained-method limit", reason)
		}
	})

	t.Run("statements", func(t *testing.T) {
		budget := javaProgramAnalysisBudget{statements: maxJavaAnalysisStatementCount - 1}
		class := javaClass{Fields: []javaStatement{{}, {}}}
		if reason := budget.reserveClass(class); !strings.Contains(reason, "retained-statement limit") {
			t.Fatalf("reserveClass reason = %q, want retained-statement limit", reason)
		}
	})

	t.Run("brace_metadata", func(t *testing.T) {
		budget := javaProgramAnalysisBudget{braceTokens: maxJavaAnalysisTotalBraceTokens - 1}
		context := &javaParseContext{scan: javaSourceScan{braceOffsets: []int{0, 1}}}
		if reason := budget.reserveContext(context); !strings.Contains(reason, "aggregate brace-metadata limit") {
			t.Fatalf("reserveContext reason = %q, want aggregate brace-metadata limit", reason)
		}
	})

	t.Run("retained_imports", func(t *testing.T) {
		budget := javaProgramAnalysisBudget{imports: maxJavaAnalysisTotalImports - 1}
		context := &javaParseContext{imports: []string{"example.One", "example.Two"}}
		if reason := budget.reserveContext(context); !strings.Contains(reason, "aggregate retained-import limit") {
			t.Fatalf("reserveContext reason = %q, want aggregate retained-import limit", reason)
		}
	})

	t.Run("scan_work", func(t *testing.T) {
		budget := javaProgramAnalysisBudget{scanBytes: maxJavaAnalysisTotalScanBytes - 1}
		context := &javaParseContext{structuralScanBytes: 2}
		if reason := budget.reserveContext(context); !strings.Contains(reason, "aggregate structural scan-work limit") {
			t.Fatalf("reserveContext reason = %q, want aggregate structural scan-work limit", reason)
		}
	})
}

func TestJavaAnalysisRejectsExcessiveStatementWork(t *testing.T) {
	var source strings.Builder
	source.WriteString("public class Actions { public int action() {\n")
	for index := 0; index <= maxJavaAnalysisStatementCount; index++ {
		source.WriteString("tick();\n")
	}
	source.WriteString("return 0; } }\n")

	class, err := parseJavaFile("Actions.java", source.String())
	if err == nil || !strings.Contains(err.Error(), "statement limit") {
		t.Fatalf("parseJavaFile error = %v, want statement limit", err)
	}
	if len(class.Unparsed) == 0 {
		t.Fatal("statement limit must leave a fail-closed unparsed marker")
	}
}

func TestJavaAnalysisNormalFixtureRemainsParsed(t *testing.T) {
	class, err := parseJavaFile("Actions.java", `public class Actions {
  public int init() { return 0; }
  public int action() {
    if (enabled()) {
      tick();
    }
    return 0;
  }
  public int end() { return 0; }
}`)
	if err != nil {
		t.Fatalf("parseJavaFile: %v", err)
	}
	if len(class.Unparsed) != 0 {
		t.Fatalf("unparsed regions = %#v", class.Unparsed)
	}
	for _, name := range []string{"init", "action", "end"} {
		if _, ok := class.Methods[name]; !ok {
			t.Fatalf("method %q missing from %#v", name, class.Methods)
		}
	}
}
