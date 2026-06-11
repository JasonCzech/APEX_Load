// Command dslschema regenerates the published DSL artifacts from the action
// catalog in pkg/dsl: docs/dsl.schema.json (JSON Schema 2020-12) and
// docs/DSL_ACTION_REFERENCE.md. Run from the repository root:
//
//	go run ./cmd/dslschema           # rewrite the artifacts
//	go run ./cmd/dslschema -check    # exit non-zero if artifacts are stale
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/jasonczech/apexload/pkg/dsl/schemagen"
)

func main() {
	schemaPath := flag.String("schema", "docs/dsl.schema.json", "output path for the generated JSON Schema")
	referencePath := flag.String("reference", "docs/DSL_ACTION_REFERENCE.md", "output path for the generated action reference")
	check := flag.Bool("check", false, "verify the committed artifacts match the catalog instead of writing")
	flag.Parse()

	schema, err := schemagen.GenerateSchema()
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate schema: %v\n", err)
		os.Exit(1)
	}
	schema = append(schema, '\n')
	reference := schemagen.GenerateReference()

	if *check {
		stale := false
		for _, artifact := range []struct {
			path    string
			content []byte
		}{
			{*schemaPath, schema},
			{*referencePath, reference},
		} {
			existing, err := os.ReadFile(artifact.path)
			if err != nil || !bytes.Equal(existing, artifact.content) {
				fmt.Fprintf(os.Stderr, "%s is stale — run `go run ./cmd/dslschema`\n", artifact.path)
				stale = true
			}
		}
		if stale {
			os.Exit(1)
		}
		return
	}

	if err := os.WriteFile(*schemaPath, schema, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *schemaPath, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*referencePath, reference, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *referencePath, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s and %s\n", *schemaPath, *referencePath)
}
