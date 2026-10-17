package api

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/importartifact"
)

func TestNativeArtifactStorageDoesNotInstallLegacyExecutionResolver(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfigured", true: "external integration"}[external], func(t *testing.T) {
			var resolver importartifact.Resolver
			if external {
				resolver = importartifact.ResolverFunc(func(context.Context, importartifact.Request) ([]byte, error) { return []byte("external"), nil })
			}
			restore := importartifact.SetResolver(resolver)
			defer restore()
			server, _, _ := newBrunoImportAPITestServer(t)
			closeBatchTestServer(t, server)
			first, err := server.brunoArtifactStore()
			if err != nil || first == nil {
				t.Fatalf("initialize native source store: %v", err)
			}
			second, err := server.brunoArtifactStore()
			if err != nil || first != second {
				t.Fatal("native source store was not reused")
			}
			if importartifact.ResolverAvailable() != external {
				t.Fatal("native source storage installed an execution resolver")
			}
			if external {
				got, err := importartifact.Resolve(context.Background(), importartifact.Request{Reference: "artifact://external"})
				if err != nil || string(got) != "external" {
					t.Fatalf("native source storage replaced external resolver: %q, %v", got, err)
				}
			}
		})
	}
}
