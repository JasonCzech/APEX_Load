package importreview

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// A UID-less import (Postman, LoadRunner, ...) has no import_action_uid, so the
// durable credential locators follow the step alignment. A client-supplied
// locator the engine cannot resolve must not break that alignment and drop the
// retained credential's locator after a deletion shifts the steps.
func TestPreparePersistentLibraryScriptKeepsLocatorsDespiteBogusClientLocator(t *testing.T) {
	const token = "Bearer postman-retained-session-token-xyz"
	rawPrior := &dsl.Script{
		ID: "postman-1", Name: "Postman Import", Version: "1.0.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{
			{Name: "home", Type: "request", Method: http.MethodGet, Target: "/home"},
			{
				Name: "verify", Type: "request", Method: http.MethodPost, Target: "/verify",
				HeaderList: []dsl.HeaderEntry{{Name: "Authorization", Value: token}},
				Metadata: map[string]any{
					dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
						{RequestKey: "actions[1]", Field: "header_list", Index: 0},
					},
				},
			},
		},
	}
	prior, err := PreparePersistentImportedLibraryScript(rawPrior, nil, "postman")
	if err != nil {
		t.Fatalf("PreparePersistentImportedLibraryScript: %v", err)
	}
	if dsl.IsValidImportActionUID(prior.Actions[1].Metadata[dsl.ImportActionUIDMetadataKey]) {
		t.Fatal("precondition: this scenario needs a UID-less import")
	}

	candidate := MaskScriptSecrets(prior)
	candidate.Actions = candidate.Actions[1:]
	candidate.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey] = []any{
		map[string]any{"field": "zzz"},
	}
	if MaskScriptSecrets(candidate) != nil {
		t.Fatal("precondition: the bogus client locator must make the projection fail")
	}

	saved, err := PreparePersistentLibraryScript(candidate, prior)
	if err != nil {
		t.Fatalf("PreparePersistentLibraryScript: %v", err)
	}
	if got := saved.Actions[0].HeaderList[0].Value; got != token {
		t.Fatalf("Authorization = %q, want the retained credential restored", got)
	}
	locators, _ := saved.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey].([]any)
	if len(locators) != 1 {
		t.Fatalf("retained credential lost its durable locator: %#v", saved.Actions[0].Metadata)
	}
	locator, _ := locators[0].(map[string]any)
	if locator["request_key"] != "actions[0]" || locator["field"] != "header_list" {
		t.Fatalf("restored locator = %#v, want the durable locator remapped to actions[0]", locator)
	}
}

// A credential located in an identity field (here an OpenAPI path segment) is
// masked only through its locator. A client that drops the locator and deletes
// an earlier step must not make the shifted step lose the durable locator: the
// next masked read would otherwise show the credential.
func TestPreparePersistentLibraryScriptKeepsIdentityFieldLocatorWhenClientDropsIt(t *testing.T) {
	const credentialValue = "path-password-7319"
	segment := "pre-" + credentialValue + "-post"
	start, end := len("pre-"), len("pre-")+len(credentialValue)
	shape := []byte(segment)
	for index := start; index < end; index++ {
		shape[index] = 0
	}
	shapeHash := sha256.Sum256(shape)
	pointer := fmt.Sprintf("openapi-path-v1:cGFzc3dvcmQ:%d:%x:%d-%d:%d-%d", len(segment), shapeHash, start, end, start, end)
	rawPrior := &dsl.Script{
		ID: "openapi-shift", Name: "OpenAPI shift", Version: "1", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://origin.example.test"},
		Actions: []dsl.Action{
			{Name: "home", Type: "request", Method: http.MethodGet, Target: "/home"},
			{
				Name: "auth", Type: "request", Method: http.MethodGet, Target: "/auth/" + segment,
				Metadata: map[string]any{
					dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{{
						"field": "url.path", "index": 2, "pointer": pointer,
					}},
				},
			},
		},
	}
	prior, err := PreparePersistentImportedLibraryScript(rawPrior, nil, "test")
	if err != nil {
		t.Fatalf("PreparePersistentImportedLibraryScript: %v", err)
	}
	candidate := MaskScriptSecrets(prior)
	if candidate == nil || strings.Contains(candidate.Actions[1].Target, credentialValue) {
		t.Fatalf("precondition: the path credential must be masked: %#v", candidate)
	}
	candidate.Actions = candidate.Actions[1:]
	delete(candidate.Actions[0].Metadata, dsl.ImportPreservedCredentialTargetsMetadataKey)

	saved, err := PreparePersistentLibraryScript(candidate, prior)
	if err != nil {
		t.Fatalf("PreparePersistentLibraryScript: %v", err)
	}
	if saved.Actions[0].Target != rawPrior.Actions[1].Target {
		t.Fatalf("target = %q, want the stored executable target", saved.Actions[0].Target)
	}
	if _, kept := saved.Actions[0].Metadata[dsl.ImportPreservedCredentialTargetsMetadataKey]; !kept {
		t.Fatalf("shifted step lost its durable locator: %#v", saved.Actions[0].Metadata)
	}
	if reread := MaskScriptSecrets(saved); reread == nil || strings.Contains(reread.Actions[0].Target, credentialValue) {
		t.Fatalf("the next read discloses the path credential: %#v", reread)
	}
}

// A step whose credential body a read replaced with a whole-body sentinel and
// the transient script_projection gate can only be restored from its matched
// stored step. Changing its identity, alone or with a deletion that shifts the
// steps, must be refused rather than persist the sentinel body and the gate.
func TestReconcileRefusesIdentityChangeOfProjectionGatedStep(t *testing.T) {
	const boundary = "gated-boundary"
	unsafeBody := "--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"payload\"\r\n" +
		"Content-Type: application/json\r\n\r\n" +
		`{"password":7}` + "\r\n" +
		"--" + boundary + "--\r\n"
	newPrior := func(t *testing.T, withUIDs bool) *dsl.Script {
		t.Helper()
		prior := &dsl.Script{
			ID: "gated", Name: "Gated", Version: "1.0.0", Protocol: "http",
			Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"},
			Actions: []dsl.Action{
				{Name: "home", Type: "request", Method: http.MethodGet, Target: "/home"},
				{
					Name: "upload", Type: "request", Method: http.MethodPost, Target: "/upload",
					Headers: map[string]string{"Content-Type": "multipart/form-data; boundary=" + boundary},
					Body:    unsafeBody,
				},
			},
		}
		if withUIDs {
			EnsureScriptImportActionUIDs(prior)
		}
		projected := MaskScriptSecrets(prior)
		if projected == nil || projected.Actions[1].Body != MaskedScriptSecret ||
			!HasScriptProjectionResponseGate(mustActionJSON(t, projected, 1)) {
			t.Fatalf("precondition: step 1 must carry the transient projection gate: %#v", projected)
		}
		return prior
	}

	for _, withUIDs := range []bool{true, false} {
		name := "legacy"
		if withUIDs {
			name = "uid"
		}
		t.Run(name+"/unchanged round trip restores the body", func(t *testing.T) {
			prior := newPrior(t, withUIDs)
			incoming := MaskScriptSecrets(prior)
			incoming.Actions = incoming.Actions[1:]
			if err := ReconcileMaskedScriptSecrets(incoming, prior); err != nil {
				t.Fatalf("ReconcileMaskedScriptSecrets: %v", err)
			}
			if incoming.Actions[0].Body != unsafeBody {
				t.Fatalf("body = %q, want the stored body restored", incoming.Actions[0].Body)
			}
			if HasScriptProjectionResponseGate(mustActionJSON(t, incoming, 0)) {
				t.Fatal("the response-only gate became durable")
			}
		})
		t.Run(name+"/retarget is refused", func(t *testing.T) {
			prior := newPrior(t, withUIDs)
			incoming := MaskScriptSecrets(prior)
			incoming.Actions[1].Target = "/upload-v2"
			if err := ReconcileMaskedScriptSecrets(incoming, prior); err == nil {
				t.Fatalf("retargeting a projection-gated step was accepted; body = %q", incoming.Actions[1].Body)
			}
		})
		t.Run(name+"/delete and retarget is refused", func(t *testing.T) {
			prior := newPrior(t, withUIDs)
			incoming := MaskScriptSecrets(prior)
			incoming.Actions = incoming.Actions[1:]
			incoming.Actions[0].Target = "/upload-v2"
			if err := ReconcileMaskedScriptSecrets(incoming, prior); err == nil {
				t.Fatalf("deleting and retargeting a projection-gated step was accepted; body = %q", incoming.Actions[0].Body)
			}
		})
	}
}

// A redacted <messageId> reads as the sentinel-free {{fn.uuid()}}. A step that
// changes identity no longer matches its stored step, so that value cannot be
// restored; the save must be refused instead of replacing the recorded id with
// the placeholder. A copy of a step that still matches keeps the placeholder.
func TestReconcileRefusesUnmatchedStepCarryingSentinelFreeProjection(t *testing.T) {
	const rawBody = `<Envelope><Header><mes:messageId>AbCdEf0123456789GhIjKlMnOpQrStUv</mes:messageId></Header><Body><Order>123</Order></Body></Envelope>`
	newStored := func(t *testing.T, withUIDs bool) *dsl.Script {
		t.Helper()
		stored := &dsl.Script{
			Name: "soap-order", Version: "1.0.0", Protocol: "http",
			Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
			Actions: []dsl.Action{
				{Name: "Login", Type: "request", Method: http.MethodGet, Target: "/login"},
				{Name: "Submit", Type: "request", Method: http.MethodPost, Target: "/soap", Body: rawBody},
			},
		}
		if withUIDs {
			EnsureScriptImportActionUIDs(stored)
		}
		projected := MaskScriptSecrets(stored).Actions[1].Body
		if projected == rawBody || strings.Contains(projected, MaskedScriptSecret) ||
			!strings.Contains(projected, ProjectedXMLMessageID) {
			t.Fatalf("precondition: projected body %q must be a sentinel-free rewrite", projected)
		}
		return stored
	}

	for _, withUIDs := range []bool{true, false} {
		name := "legacy"
		if withUIDs {
			name = "uid"
		}
		t.Run(name+"/retarget is refused", func(t *testing.T) {
			stored := newStored(t, withUIDs)
			incoming := MaskScriptSecrets(stored)
			incoming.Actions[1].Target = "/soap-v2"
			if err := ReconcileMaskedScriptSecrets(incoming, stored); err == nil {
				t.Fatalf("retarget persisted the placeholder: body = %q", incoming.Actions[1].Body)
			}
		})
		t.Run(name+"/edited body on a retargeted step is kept", func(t *testing.T) {
			stored := newStored(t, withUIDs)
			incoming := MaskScriptSecrets(stored)
			incoming.Actions[1].Target = "/soap-v2"
			incoming.Actions[1].Body = `<Envelope><Body><Order>999</Order></Body></Envelope>`
			if err := ReconcileMaskedScriptSecrets(incoming, stored); err != nil {
				t.Fatalf("ReconcileMaskedScriptSecrets: %v", err)
			}
		})
	}
	t.Run("uid/copy keeps the placeholder and the original its stored id", func(t *testing.T) {
		stored := newStored(t, true)
		incoming := MaskScriptSecrets(stored)
		incoming.Actions = append(incoming.Actions, CloneScriptDefinition(incoming).Actions[1])
		if err := ReconcileMaskedScriptSecrets(incoming, stored); err != nil {
			t.Fatalf("ReconcileMaskedScriptSecrets: %v", err)
		}
		if incoming.Actions[1].Body != rawBody {
			t.Fatalf("original body = %q, want the stored body restored", incoming.Actions[1].Body)
		}
	})
}

// Kafka and JMS sends deliver source_transaction (and every other non-provenance
// top-level string entry) as a record header or message property, so a
// [REDACTED] sentinel there is an orphan executable credential, not provenance.
func TestMessagingSentMetadataIsNotImportProvenance(t *testing.T) {
	newStored := func(actionType string) *dsl.Script {
		return &dsl.Script{
			Name: "messaging", Version: "1.0.0", Protocol: "kafka",
			Actions: []dsl.Action{{
				Name: "send", Type: actionType, Target: "orders",
				Metadata: map[string]any{
					"source_transaction": "Checkout",
					"source_path":        "flows/checkout.json",
				},
			}},
		}
	}
	for _, actionType := range []string{"kafka_produce", "kafka_request_reply", "jms_publish", "jms_request_reply"} {
		t.Run(actionType, func(t *testing.T) {
			stored := newStored(actionType)
			incoming := MaskScriptSecrets(stored)
			incoming.Actions[0].Metadata["source_transaction"] = MaskedScriptSecret
			if err := ReconcileMaskedScriptSecrets(incoming, stored); err == nil {
				t.Fatalf("sent source_transaction accepted a %s sentinel", MaskedScriptSecret)
			}

			sentUID := newStored(actionType)
			sentUID.Actions[0].Metadata[dsl.ImportActionUIDMetadataKey] = "uid-1"
			sentUID.Actions[0].Metadata["source_transaction"] = MaskedScriptSecret
			if reqs := DiscoverSecretBindingRequirements(sentUID, 0); len(reqs) != 1 {
				t.Fatalf("sent source_transaction sentinel raised %d binding requirements, want 1", len(reqs))
			}

			provenance := newStored(actionType)
			provenance.Actions[0].Metadata["source_path"] = MaskedScriptSecret
			if reqs := DiscoverSecretBindingRequirements(provenance, 0); len(reqs) != 0 {
				t.Fatalf("messaging provenance source_path raised %d binding requirements, want 0", len(reqs))
			}
		})
	}
	t.Run("http source_transaction stays provenance", func(t *testing.T) {
		script := &dsl.Script{
			Name: "http", Version: "1.0.0", Protocol: "http",
			Actions: []dsl.Action{{
				Name: "get", Type: "request", Method: http.MethodGet, Target: "/",
				Metadata: map[string]any{"source_transaction": MaskedScriptSecret},
			}},
		}
		if reqs := DiscoverSecretBindingRequirements(script, 0); len(reqs) != 0 {
			t.Fatalf("HTTP source_transaction raised %d binding requirements, want 0", len(reqs))
		}
	})
}

func mustActionJSON(t *testing.T, script *dsl.Script, index int) map[string]any {
	t.Helper()
	value, err := ScriptAsJSONValue(script)
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := value.(map[string]any)["actions"].([]any)
	action, _ := actions[index].(map[string]any)
	return action
}
