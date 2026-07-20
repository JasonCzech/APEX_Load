// Command openapits generates the dashboard's TypeScript transport contract
// from the controller's generated OpenAPI document.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	inputPath := flag.String("in", "docs/swagger.yaml", "path to the generated OpenAPI document")
	outputPath := flag.String("out", "dashboard/src/types/generated/openapi.ts", "path to the generated TypeScript contract")
	check := flag.Bool("check", false, "verify that the output is current without writing it")
	flag.Parse()

	input, err := os.ReadFile(*inputPath)
	if err != nil {
		fail("read OpenAPI document", err)
	}
	generated, err := generateTransportTypes(input)
	if err != nil {
		fail("generate TypeScript contract", err)
	}

	if *check {
		current, err := os.ReadFile(*outputPath)
		if err != nil {
			fail("read generated TypeScript contract", err)
		}
		if !bytes.Equal(current, generated) {
			fmt.Fprintf(os.Stderr, "%s is stale; run openapits without -check\n", *outputPath)
			os.Exit(1)
		}
		fmt.Printf("verified %s\n", *outputPath)
		return
	}

	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
		fail("create output directory", err)
	}
	if err := os.WriteFile(*outputPath, generated, 0o644); err != nil {
		fail("write generated TypeScript contract", err)
	}
	fmt.Printf("wrote %s\n", *outputPath)
}

func fail(action string, err error) {
	fmt.Fprintf(os.Stderr, "openapits: %s: %v\n", action, err)
	os.Exit(1)
}
