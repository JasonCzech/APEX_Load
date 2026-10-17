package importartifact_test

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/importartifact/legacyresolver"
)

func TestLegacyResolverFacadePreservesExternalSourceCompatibility(t *testing.T) {
	restore := importartifact.SetResolver(nil)
	defer restore()
	if importartifact.ResolverAvailable() {
		t.Fatal("unexpected resolver")
	}
	installed := importartifact.SetResolver(importartifact.ResolverFunc(func(_ context.Context, request importartifact.Request) ([]byte, error) {
		return []byte(request.ProjectID + ":" + request.Reference), nil
	}))
	defer installed()
	for name, resolve := range map[string]func(context.Context, importartifact.Request) ([]byte, error){
		"compatibility facade": importartifact.Resolve,
		"legacy owner":         legacyresolver.Resolve,
	} {
		got, err := resolve(context.Background(), importartifact.Request{Reference: "artifact://bundle", ProjectID: "project"})
		if err != nil || string(got) != "project:artifact://bundle" {
			t.Fatalf("%s = %q, %v", name, got, err)
		}
	}
}
