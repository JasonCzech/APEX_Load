// Command swaggergen regenerates docs/swagger.yaml, docs/api-endpoint-catalog.json,
// and the embedded copies served by the controller docs API from the controller
// routing layer. It is invoked via `go generate ./pkg/api` (see pkg/api/openapi_gen.go);
// freshness is test-enforced in pkg/api, mirroring the DSL schema/reference generation
// in pkg/dsl.
//
// Usage:
//
//	go run ./cmd/swaggergen -server pkg/api/server.go -out docs/swagger.yaml -catalog-out docs/api-endpoint-catalog.json -embed-out pkg/api/apidocs/swagger.yaml -catalog-embed-out pkg/api/apidocs/api-endpoint-catalog.json
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jasonczech/apexload/pkg/api"
)

func main() {
	server := flag.String("server", "pkg/api/server.go", "path to the controller routing source (server.go)")
	out := flag.String("out", "docs/swagger.yaml", "output path for the generated OpenAPI spec")
	catalogOut := flag.String("catalog-out", "docs/api-endpoint-catalog.json", "output path for the generated endpoint catalog JSON; empty disables catalog generation")
	embedOut := flag.String("embed-out", "", "optional output path for an embedded copy of the generated OpenAPI spec")
	catalogEmbedOut := flag.String("catalog-embed-out", "", "optional output path for an embedded copy of the generated endpoint catalog JSON")
	flag.Parse()

	data, err := api.GenerateOpenAPIYAML(*server)
	if err != nil {
		fmt.Fprintln(os.Stderr, "swaggergen:", err)
		os.Exit(1)
	}
	writeGeneratedFile(*out, data)
	if *embedOut != "" {
		writeGeneratedFile(*embedOut, data)
	}

	if *catalogOut != "" {
		catalogData, err := api.GenerateEndpointCatalogJSON(*server)
		if err != nil {
			fmt.Fprintln(os.Stderr, "swaggergen:", err)
			os.Exit(1)
		}
		writeGeneratedFile(*catalogOut, catalogData)
		if *catalogEmbedOut != "" {
			writeGeneratedFile(*catalogEmbedOut, catalogData)
		}
	}
}

func writeGeneratedFile(path string, data []byte) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "swaggergen:", err)
			os.Exit(1)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "swaggergen:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", path)
}
