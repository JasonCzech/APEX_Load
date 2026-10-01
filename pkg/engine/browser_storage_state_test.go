package engine

import (
	"slices"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestBrowserStorageStateSecretResolutionAndLeafRedaction(t *testing.T) {
	raw := `{"cookies":[{"name":"sid","value":"secret-cookie","domain":"example.test","path":"/"}],"origins":[{"origin":"https://example.test","localStorage":[{"name":"token","value":"secret-local"},{"name":"auth","value":"{\"accessToken\":\"nested-secret\"}"}]}]}`
	script := &dsl.Script{Protocol: "browser", Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{StorageState: "{{secret.SESSION}}"}}}
	resolver := SecretResolverFunc(func(name string) (string, bool) { return raw, name == "SESSION" })
	cfg := buildProtoConfigWithSecretResolver(script, resolver)
	if cfg["browser_storage_state"] != raw {
		t.Fatal("storage state not resolved")
	}
	values := runtimeProtocolAuthCompositeValues(script, resolver)
	for _, v := range []string{"secret-cookie", "secret-local", "nested-secret"} {
		if !slices.Contains(values, v) {
			t.Fatalf("leaf not protected: %s", v)
		}
	}
	script.Config.Browser.StorageState = raw
	if values := runtimeProtocolAuthCompositeValues(script, resolver); len(values) > 0 {
		t.Fatal("authored literal treated as store-managed")
	}
}
