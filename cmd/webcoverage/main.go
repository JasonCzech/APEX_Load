// Command webcoverage generates docs/LOADRUNNER_WEB_FUNCTION_COVERAGE.md from the
// embedded Web (HTTP/HTML) function registry and the code-owned coverage detail
// map in pkg/convert/enterprisearchive. Run via `go generate ./pkg/convert/enterprisearchive`.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
)

func main() {
	out := flag.String("out", "docs/LOADRUNNER_WEB_FUNCTION_COVERAGE.md", "output markdown path")
	flag.Parse()

	content := enterprisearchive.RenderWebHTTPFunctionCoverageMarkdown()
	if err := os.WriteFile(*out, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "webcoverage: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", *out)
}
