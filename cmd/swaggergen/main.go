// Command swaggergen regenerates docs/swagger.yaml and docs/api-endpoint-catalog.json
// from the controller routing layer. It is invoked via `go generate ./pkg/api` (see
// pkg/api/openapi_gen.go); freshness is test-enforced in pkg/api, mirroring the DSL
// schema/reference generation in pkg/dsl.
//
// Usage:
//
//	go run ./cmd/swaggergen -server pkg/api/server.go -out docs/swagger.yaml -catalog-out docs/api-endpoint-catalog.json
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jasonczech/apexload/pkg/api"
)

func main() {
	server := flag.String("server", "pkg/api/server.go", "path to the controller routing source (server.go)")
	out := flag.String("out", "docs/swagger.yaml", "output path for the generated OpenAPI spec")
	catalogOut := flag.String("catalog-out", "docs/api-endpoint-catalog.json", "output path for the generated endpoint catalog JSON; empty disables catalog generation")
	flag.Parse()

	data, err := api.GenerateOpenAPIYAML(*server)
	if err != nil {
		fmt.Fprintln(os.Stderr, "swaggergen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "swaggergen:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", *out)

	if *catalogOut != "" {
		catalogData, err := api.GenerateEndpointCatalogJSON(*server)
		if err != nil {
			fmt.Fprintln(os.Stderr, "swaggergen:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*catalogOut, catalogData, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "swaggergen:", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s\n", *catalogOut)
	}
}
