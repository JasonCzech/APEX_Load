package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jasonczech/apexload/pkg/refkey"
)

// A copy of a stored script's definition carries the original's reference
// key. Creating it must give the copy a key of its own rather than failing
// on the unique index.
func TestCreateScriptGivesACopyItsOwnRefKey(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	original, err := server.runner.CreateScriptAuthoringDurable(authoringAPIScript("refkey-original"))
	if err != nil {
		t.Fatal(err)
	}
	if original.RefKey == "" {
		t.Fatal("original has no reference key")
	}

	create := func(id, refKey string) StoredScript {
		t.Helper()
		copied := authoringAPIScript(id)
		copied.RefKey = refKey
		response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts", map[string]any{"script": copied}, "")
		if response.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", id, response.Code, response.Body.String())
		}
		var created StoredScript
		if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		return created
	}

	copied := create("refkey-copy", original.RefKey)
	if copied.RefKey == "" || copied.RefKey == original.RefKey {
		t.Fatalf("copy ref key = %q, want a new key besides %q", copied.RefKey, original.RefKey)
	}

	// A replica that has not indexed the original yet still gets a fresh key:
	// the store refuses the duplicate and the create retries with the next one.
	server.runner.mu.Lock()
	server.runner.removeRefKeyLocked(original.RefKey)
	server.runner.mu.Unlock()
	stale := create("refkey-stale", original.RefKey)
	if stale.RefKey == "" || stale.RefKey == original.RefKey || stale.RefKey == copied.RefKey {
		t.Fatalf("stale-index copy ref key = %q, want one of its own", stale.RefKey)
	}

	// Another revision of a live script's key (an export taken before or after
	// later edits) still names that script.
	parts, ok := refkey.Parse(original.RefKey)
	if !ok {
		t.Fatalf("original ref key %q does not parse", original.RefKey)
	}
	revision := create("refkey-revision", refkey.Script(parts.Env, parts.Seq, parts.Version+5))
	if revisionParts, ok := refkey.Parse(revision.RefKey); !ok || revisionParts.Seq == parts.Seq {
		t.Fatalf("other-revision copy ref key = %q, want another sequence than %q", revision.RefKey, original.RefKey)
	}

	// A key no script holds is kept.
	free := create("refkey-free", "OTH-SCR-000042-v1")
	if free.RefKey != "OTH-SCR-000042-v1" {
		t.Fatalf("free ref key = %q, want it kept", free.RefKey)
	}

	// The library save path does the same for a new script.
	saveCopy := authoringAPIScript("refkey-saved")
	saveCopy.RefKey = original.RefKey
	saved, err := server.runner.StoreScriptDurable(saveCopy)
	if err != nil {
		t.Fatal(err)
	}
	if saved.RefKey == "" || saved.RefKey == original.RefKey {
		t.Fatalf("saved copy ref key = %q, want a new key", saved.RefKey)
	}
}
