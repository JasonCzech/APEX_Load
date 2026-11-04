// Command agentschemagen regenerates the agent-v1 contract's JSON Schemas in
// docs/contracts/agent-v1 from the Go types the controller serves:
//
//	go run ./cmd/agentschemagen            # rewrite the schemas
//	go run ./cmd/agentschemagen -check     # exit non-zero if they are stale
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jasonczech/apexload/pkg/api"
)

func main() {
	out := flag.String("out", "docs/contracts/agent-v1", "directory for the generated schemas")
	check := flag.Bool("check", false, "verify the committed schemas instead of writing")
	flag.Parse()

	schemas, err := api.AgentContractSchemas()
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate schemas: %v\n", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	stale := false
	for _, name := range names {
		path := filepath.Join(*out, name)
		if *check {
			existing, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(existing, schemas[name]) {
				fmt.Fprintf(os.Stderr, "%s is stale — run `go generate ./pkg/api`\n", path)
				stale = true
			}
			continue
		}
		if err := os.WriteFile(path, schemas[name], 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s\n", path)
	}
	if stale {
		os.Exit(1)
	}
}
