//go:build postgres_integration

package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// On shared PostgreSQL, a replica that has not indexed a script another
// replica created still gives a copy of it a key of its own: the store
// refuses the duplicate key and the create retries with the next one.
func TestPostgresCreateScriptCopyGetsItsOwnRefKeyAcrossReplicas(t *testing.T) {
	dsn := newPostgresScriptAuthoringAPIFixture(t)
	owner, _ := openPostgresScriptAuthoringAPI(t, dsn)
	peer, _ := openPostgresScriptAuthoringAPI(t, dsn)
	original, err := owner.runner.CreateScriptAuthoringDurable(authoringAPIScript("pg-refkey-original"))
	if err != nil {
		t.Fatal(err)
	}
	peer.runner.mu.Lock()
	peer.runner.removeRefKeyLocked(original.RefKey)
	peer.runner.mu.Unlock()

	copied := authoringAPIScript("pg-refkey-copy")
	copied.RefKey = original.RefKey
	response := authoringAPIRequest(t, peer, http.MethodPost, "/api/v1/scripts", map[string]any{"script": copied}, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("copy create on peer: %d %s", response.Code, response.Body.String())
	}
	var created StoredScript
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.RefKey == "" || created.RefKey == original.RefKey {
		t.Fatalf("copy ref key = %q, want a new key besides %q", created.RefKey, original.RefKey)
	}
}
