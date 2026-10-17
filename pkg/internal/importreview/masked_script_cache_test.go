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
