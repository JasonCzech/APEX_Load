package importreview

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestMaskScriptSecretsReusesTheMaskedResultAsIndependentCopies(t *testing.T) {
	script := &dsl.Script{Name: "masked cache", Version: "1.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{
		Name: "login", Type: "request", Method: "POST", Target: "/login",
		Headers: map[string]string{"Authorization": "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.c2lnbmF0dXJlLXZhbHVl"},
		Body:    `{"user":"demo","password":"hunter2-private-value"}`,
	}}}
	payload, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	first := MaskScriptSecrets(script)
	if first == nil {
		t.Fatal("first mask failed")
	}
	if MaskedScripts.Get(sha256.Sum256(payload)) == nil {
		t.Fatal("masked result was not kept")
	}
	second := MaskScriptSecrets(script)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("cached mask differs:\n%#v\n%#v", first, second)
	}
	for _, masked := range []*dsl.Script{first, second} {
		encoded, _ := json.Marshal(masked)
		if bytes.Contains(encoded, []byte("hunter2-private-value")) || bytes.Contains(encoded, []byte("c2lnbmF0dXJlLXZhbHVl")) {
			t.Fatalf("masked script still carries a secret: %s", encoded)
		}
	}
	second.Actions[0].Name = "changed by a caller"
	if third := MaskScriptSecrets(script); third.Actions[0].Name != "login" {
		t.Fatalf("a caller's change leaked into the cache: %q", third.Actions[0].Name)
	}
}

func TestMaskedScriptCacheIsBoundedByEntriesAndBytes(t *testing.T) {
	cache := NewMaskedScriptCache(2, 10)
	key := func(b byte) [32]byte { return [32]byte{b} }
	cache.Put(key(1), []byte("aaaa"))
	cache.Put(key(2), []byte("bbbb"))
	cache.Get(key(1)) // most recently used
	cache.Put(key(3), []byte("cccc"))
	if cache.Get(key(2)) != nil || cache.Get(key(1)) == nil || cache.Get(key(3)) == nil {
		t.Fatal("least recently used entry was not the one evicted")
	}
	cache.Put(key(4), []byte("dddddddd"))
	if cache.bytes > 10 || cache.order.Len() > 2 {
		t.Fatalf("cache holds %d entries, %d bytes", cache.order.Len(), cache.bytes)
	}
	cache.Put(key(5), []byte("an entry larger than the whole cache"))
	if cache.Get(key(5)) != nil {
		t.Fatal("an oversized entry was kept")
	}
}

func TestMaskScriptSecretsKeepsAuraCacheKeysOnly(t *testing.T) {
	const appKey = "4385_ZsFss-efN4RuL6razjopzA"
	script := &dsl.Script{Name: "aura", Protocol: "http", Actions: []dsl.Action{{
		Type: "request", Name: "component defs", Method: "GET",
		Target: "https://b.static.lightning.force.com/x/auraCmpDef?_au=" + appKey + "&_uid=" + appKey,
		Metadata: map[string]any{"har.request.query_string": []any{
			map[string]any{"name": "_au", "value": appKey},
			map[string]any{"name": "_uid", "value": appKey},
			map[string]any{"name": "other", "value": appKey},
		}},
	}}}
	requirements := DiscoverSecretBindingRequirements(MaskScriptSecrets(script), 0)
	if len(requirements) != 1 || requirements[0].Path != "/actions/0/metadata/har.request.query_string/2/value" {
		t.Fatalf("requirements = %#v, want only the lookalike token under another name", requirements)
	}
}

func TestMaskScriptSecretsKeepsGeneratedSetCookieExtractorName(t *testing.T) {
	// The Salesforce aura.token producer reads the __Host-ERIC_PROD<digits>
	// Set-Cookie of /one/one.app. Masking that long name broke the regex, and
	// every Aura request then sent a stale token.
	expression := `(?m)^(?i:Set-Cookie):[ \t]*__Host-ERIC_PROD6073380544767438430=[ \t]*"?([^";\s,]+)"?(?:[ \t]*;|[ \t]*$)`
	script := &dsl.Script{Name: "aura", Protocol: "http", Actions: []dsl.Action{{
		Type: "request", Name: "one.app", Method: "GET", Target: "https://example.lightning.force.com/one/one.app",
		Extractors: []dsl.Extractor{{Type: "regex", Source: "headers", Expression: expression, Name: "corr_cookie_host_eric_prod"}},
	}}}
	projected := MaskScriptSecrets(script)
	if got := projected.Actions[0].Extractors[0].Expression; got != expression {
		t.Fatalf("generated Set-Cookie extractor was masked: %s", got)
	}
	if requirements := DiscoverSecretBindingRequirements(projected, 0); len(requirements) != 0 {
		t.Fatalf("requirements = %#v, want none", requirements)
	}
}

func TestMaskScriptSecretsCacheKeyExemptionIsScoped(t *testing.T) {
	const appKey = "4385_ZsFss-efN4RuL6razjopzA"
	script := &dsl.Script{Name: "aura", Protocol: "http", Actions: []dsl.Action{{
		Type: "request", Name: "defs", Method: "GET",
		Target:  "https://b.static.lightning.force.com/x/auraCmpDef?_uid=" + appKey,
		Headers: map[string]string{"X-Api-Key": appKey},
		Metadata: map[string]any{
			// A cookie map is not a recorded query parameter.
			"har.response.cookies": []any{map[string]any{"name": "_uid", "value": "9999_k3Jq9LmN8vR3sT6wY1uE5i"}},
			// The same value is a known credential elsewhere in the script.
			"har.request.query_string": []any{map[string]any{"name": "_uid", "value": appKey}},
		},
	}}}
	projected := MaskScriptSecrets(script)
	metadata := projected.Actions[0].Metadata
	cookie := metadata["har.response.cookies"].([]any)[0].(map[string]any)["value"]
	query := metadata["har.request.query_string"].([]any)[0].(map[string]any)["value"]
	if cookie == "9999_k3Jq9LmN8vR3sT6wY1uE5i" {
		t.Fatal("a cookie map took the query-parameter cache-key exemption")
	}
	if query == appKey {
		t.Fatal("a cache-key value that is a known credential elsewhere was kept")
	}
}

func TestMaskScriptSecretsKeepsLightningWorkspacePaths(t *testing.T) {
	// Lightning sends the open workspace as a site-relative path. Both
	// projections swap the bound record id for a placeholder before scrubbing;
	// the path must still read as a path, not one high-entropy token.
	for _, record := range []string{"{{var.corr_id_2}}", "a1B8c00000ABCDeEAP"} {
		referer := "https://x.lightning.force.com/lightning/n/BuyFlow?c__objId={{var.corr_id_2}}&ws=%2Flightning%2Fr%2FInteraction__c%2F" + record + "%2Fview"
		script := &dsl.Script{Name: "ws", Protocol: "http", Actions: []dsl.Action{{
			Type: "request", Name: "aura", Method: "POST", Target: "https://x.lightning.force.com/aura",
			Headers: map[string]string{"referer": referer},
		}}}
		if got := MaskScriptSecrets(script).Actions[0].Headers["referer"]; got != referer {
			t.Fatalf("workspace path masked: %s", got)
		}
	}
}
