// Command webcoverage generates the LoadRunner import coverage docs from the
// embedded function registries and the code-owned coverage detail maps in
// pkg/convert/enterprisearchive. Run via `go generate ./pkg/convert/enterprisearchive`.
//
//	-set web        (default) → docs/LOADRUNNER_WEB_FUNCTION_COVERAGE.md
//	-set java                 → docs/LOADRUNNER_JAVA_LR_FUNCTION_COVERAGE.md
//	-set trueclient           → docs/LOADRUNNER_TRUCLIENT_STEP_COVERAGE.md
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
)

func main() {
	set := flag.String("set", "web", "coverage set to render: web | java | trueclient")
	out := flag.String("out", "", "output markdown path (defaults per set)")
	flag.Parse()

	var content, defaultOut string
	switch *set {
	case "web":
		content = enterprisearchive.RenderWebHTTPFunctionCoverageMarkdown()
		defaultOut = "docs/LOADRUNNER_WEB_FUNCTION_COVERAGE.md"
	case "java":
		content = enterprisearchive.RenderJavaLRFunctionCoverageMarkdown()
		defaultOut = "docs/LOADRUNNER_JAVA_LR_FUNCTION_COVERAGE.md"
	case "trueclient":
		content = trueclient.RenderStepCoverageMarkdown()
		defaultOut = "docs/LOADRUNNER_TRUCLIENT_STEP_COVERAGE.md"
	default:
		fmt.Fprintf(os.Stderr, "webcoverage: unknown -set %q (want web|java|trueclient)\n", *set)
		os.Exit(2)
	}

	path := *out
	if path == "" {
		path = defaultOut
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "webcoverage: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", path)
}
