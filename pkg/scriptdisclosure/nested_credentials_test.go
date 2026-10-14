package scriptdisclosure

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestNestedStructuredCredentialsMaskReplicasAndPreserveSafeWire(t *testing.T) {
	const secret = "synthetic-inner-credential"
	credentialJSON := `{"opaque":{"ordinary":"` + secret + `"}}`
	inner, _ := json.Marshal(map[string]any{"recordId": "synthetic-business", "apiToken": credentialJSON, "safe": "one two"})
	envelope, _ := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "synthetic-id", "descriptor": "synthetic://save", "params": map[string]any{"input": string(inner)}}}})
	for _, format := range []string{"json", "form", "base64-json", "base64-form"} {
		t.Run(format, func(t *testing.T) {
			body := "  \n" + string(envelope) + "\n "
			if strings.HasSuffix(format, "form") {
				body = "ordinary=one%20two&message=" + url.QueryEscape(string(envelope)) + "&ordinary=three+four&flag"
			}
			encoding := ""
			if strings.HasPrefix(format, "base64-") {
				body = base64.StdEncoding.EncodeToString([]byte(body))
				encoding = "base64"
			}
			script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Name: secret, Type: "POST", Target: "/aura", Body: body, BodyEncoding: encoding}, {Name: "safe", Type: "GET", Target: "/ordinary", Metadata: map[string]any{"ordinary": secret}}}}
			if strings.HasSuffix(format, "form") {
				script.Actions[0].Headers = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
			}
			safe, err := Project(script)
			if err != nil || safe == nil {
				t.Fatalf("projection failed: %v", err)
			}
			if safe.Name != credential.Redacted || safe.Actions[0].Name != credential.Redacted || safe.Actions[1].Metadata["ordinary"] != credential.Redacted {
				t.Fatal("credential container child replica survived projection")
			}
			wire := safe.Actions[0].Body
			if encoding != "" {
				decoded, err := base64.StdEncoding.DecodeString(wire)
				if err != nil {
					t.Fatal(err)
				}
				wire = string(decoded)
			}
			if strings.Contains(wire, secret) || wire == credential.Redacted {
				t.Fatal("nested credential projection lost its safe structure")
			}
			if strings.HasSuffix(format, "form") {
				if !strings.HasPrefix(wire, "ordinary=one%20two&message=") || !strings.HasSuffix(wire, "&ordinary=three+four&flag") {
					t.Fatal("unselected form spelling/order changed")
				}
				values, err := url.ParseQuery(wire)
				if err != nil {
					t.Fatal(err)
				}
				wire = values.Get("message")
			} else if !strings.HasPrefix(wire, "  \n{") || !strings.HasSuffix(wire, "}\n ") {
				t.Fatal("unselected outer JSON spans changed")
			}
			var outer struct {
				Actions []struct {
					Params struct {
						Input string `json:"input"`
					} `json:"params"`
				} `json:"actions"`
			}
			if err := json.Unmarshal([]byte(wire), &outer); err != nil {
				t.Fatal(err)
			}
			var business map[string]any
			if err := json.Unmarshal([]byte(outer.Actions[0].Params.Input), &business); err != nil {
				t.Fatal(err)
			}
			if business["recordId"] != "synthetic-business" || business["safe"] != "one two" || business["apiToken"] != credential.Redacted {
				t.Fatal("safe semantic sibling changed or credential survived")
			}
			if script.Actions[0].Body != body || script.Name != secret {
				t.Fatal("source executable bytes mutated")
			}
			if got := ProjectLabel(secret, script); got != credential.Redacted {
				t.Fatal("encoded credential was not known to label projection")
			}
		})
	}
}

func TestNestedCredentialDuplicateMembersAndEscapedKeys(t *testing.T) {
	const first, second = "synthetic-first-credential", "synthetic-second-credential"
	inner := `{"api\u0054oken":"` + first + `","apiToken":"` + second + `","recordId":"safe-business"}`
	encoded, _ := json.Marshal(inner)
	body := `{"payload":` + string(encoded) + `}`
	script := &dsl.Script{Name: first, Protocol: "http", Actions: []dsl.Action{{Name: second, Type: "POST", Target: "/", Body: body}}}
	safe, err := Project(script)
	if err != nil || safe == nil {
		t.Fatalf("projection: %v", err)
	}
	if safe.Name != credential.Redacted || safe.Actions[0].Name != credential.Redacted || strings.Contains(safe.Actions[0].Body, first) || strings.Contains(safe.Actions[0].Body, second) {
		t.Fatal("duplicate semantic credential occurrence was lost")
	}
	if script.Actions[0].Body != body {
		t.Fatal("source mutated")
	}
}

func TestJSONGraphQLCredentialKeepsCrossSurfaceCollection(t *testing.T) {
	const secret = "synthetic-graphql-credential"
	body, _ := json.Marshal(map[string]any{"query": `mutation { login(password: "` + secret + `") { ok } }`})
	script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Name: secret, Type: "POST", Target: "/graphql", Body: string(body)}}}
	safe, err := Project(script)
	if err != nil || safe == nil {
		t.Fatalf("projection: %v", err)
	}
	if safe.Name != credential.Redacted || safe.Actions[0].Name != credential.Redacted {
		t.Fatal("GraphQL envelope credential replica survived")
	}
	if got := ProjectLabel(secret, script); got != credential.Redacted {
		t.Fatal("GraphQL credential was not known to labels")
	}
}

func TestNestedCollectionLimitsAreSharedAndFailClosed(t *testing.T) {
	body := []byte(`{"payload":"{\"apiToken\":\"synthetic-credential\"}"}`)
	collector := newSecretCollector(nil, nil)
	collector.structuredBudget.bytes = 4 * len(body)
	collector.collectStructuredBody(body, "")
	if collector.structuredBudget.incomplete {
		t.Fatal("first bounded body did not fit")
	}
	collector.collectStructuredBody(body, "")
	if !collector.structuredBudget.incomplete {
		t.Fatal("second body acquired a fresh byte budget")
	}
	collector = newSecretCollector(nil, nil)
	collector.structuredBudget.values = 2
	collector.collectStructuredBody([]byte(`{"apiToken":"synthetic","ordinary":"value"}`), "")
	if !collector.structuredBudget.incomplete {
		t.Fatal("token count did not share value budget")
	}
	payload := `{"apiToken":"synthetic-credential"}`
	for i := 0; i < maxStructuredJSONStringLayers+1; i++ {
		quoted, _ := json.Marshal(payload)
		payload = `{"payload":` + string(quoted) + `}`
	}
	script := &dsl.Script{Name: "synthetic-credential", Protocol: "http", Actions: []dsl.Action{{Type: "POST", Target: "/", Body: payload}}}
	if safe, err := Project(script); err == nil || safe != nil {
		t.Fatal("incomplete nested collection returned a partial disclosure")
	}
	if got := ProjectLabel("synthetic-credential", script); got != credential.Redacted {
		t.Fatal("incomplete label collection exposed a replica")
	}
}

// A JSON body with a portable template where a value goes is still read, so
// its credentials are masked; an unquoted expression the collector cannot
// vouch for keeps the projection closed.
func TestCollectionReadsPortableTemplatesInJSONBodies(t *testing.T) {
	const secret = "synthetic-templated-credential"
	templated := `{"apiToken":"` + secret + `","events":[{"t":{{fn.timestamp.unix_ms}}}]}`
	script := &dsl.Script{Name: "templated", Protocol: "http", Actions: []dsl.Action{{Name: secret, Type: "POST", Target: "/events", Body: templated}}}
	safe, err := Project(script)
	if err != nil || safe == nil {
		t.Fatalf("projection: %v", err)
	}
	if safe.Actions[0].Name != credential.Redacted || strings.Contains(safe.Actions[0].Body, secret) {
		t.Fatalf("templated body credential survived: %+v", safe.Actions[0])
	}
	if !strings.Contains(safe.Actions[0].Body, "{{fn.timestamp.unix_ms}}") {
		t.Fatalf("template expression was not kept: %s", safe.Actions[0].Body)
	}

	literal := &dsl.Script{Name: "literal", Protocol: "http", Actions: []dsl.Action{{Type: "POST", Target: "/", Body: `{"apiToken":{{fn.base64("` + secret + `")}}}`}}}
	if safe, err := Project(literal); err == nil || safe != nil {
		t.Fatal("an unquoted expression with a literal argument was disclosed")
	}
}

func TestNestedCollectionRejectsJSONUnicodeSemanticLoss(t *testing.T) {
	for _, body := range []string{
		"{\"apiToken\":\"synthetic-" + string([]byte{0xff}) + "-credential\"}",
		`{"apiToken":"\ud800-synthetic-credential"}`,
		`{"apiToken":"\udc00-synthetic-credential"}`,
	} {
		script := &dsl.Script{Name: "synthetic-credential", Protocol: "http", Actions: []dsl.Action{{Type: "POST", Target: "/", Body: body}}}
		if safe, err := Project(script); err == nil || safe != nil {
			t.Fatal("semantic JSON string loss returned partial disclosure")
		}
		if got := ProjectLabel("synthetic-credential", script); got != credential.Redacted {
			t.Fatal("semantic JSON string loss returned partial label")
		}
	}
	valid := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "POST", Target: "/", Body: `{"ordinary":"\ud83d\ude00","recordId":"safe"}`}}}
	if safe, err := Project(valid); err != nil || safe == nil || safe.Actions[0].Body != valid.Actions[0].Body {
		t.Fatal("valid surrogate pair changed safe bytes")
	}
}

func TestMalformedNestedCredentialCollectionNeverPublishesReplicas(t *testing.T) {
	const secret = "synthetic-hidden-credential"
	for _, malformed := range []string{
		`{ordinary:invalid,"apiToken":"` + secret + `"}`,
		`{"ordinary":x,"apiToken:` + secret + `}`,
		`{"ordinary":x,"api\u0054oken:` + secret + `}`,
		`{"ordinary":"safe"} {"apiToken":"` + secret + `"}`,
		`{ordinary:invalid,"payload":"{\"apiToken\":\"` + secret + `\"}"}`,
	} {
		quoted, _ := json.Marshal(malformed)
		for _, body := range []string{"message=" + url.QueryEscape(malformed), `{"payload":` + string(quoted) + `}`} {
			script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Name: secret, Type: "POST", Target: "/", Body: body, Metadata: map[string]any{"note": secret}}}}
			if safe, err := Project(script); err == nil || safe != nil {
				t.Fatal("malformed collection published a credential replica")
			}
			if got := ProjectLabel(secret, script); got != credential.Redacted {
				t.Fatal("malformed collection published a credential label")
			}
		}
	}
}

// A portable template where a nested JSON value goes no longer makes that JSON
// unreadable; the credential beside it is still masked wherever it appears.
func TestNestedTemplatedJSONStillMasksItsCredentials(t *testing.T) {
	const secret = "synthetic-hidden-credential"
	nested := `{"ordinary":{{var.number}},"api\u0054oken":"` + secret + `"}`
	quoted, _ := json.Marshal(nested)
	for _, body := range []string{"message=" + url.QueryEscape(nested), `{"payload":` + string(quoted) + `}`} {
		script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Name: secret, Type: "POST", Target: "/", Body: body, Metadata: map[string]any{"note": secret}}}}
		if safe, err := Project(script); err == nil {
			encoded, _ := json.Marshal(safe)
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("templated nested JSON published its credential: %s", encoded)
			}
		}
		if got := ProjectLabel(secret, script); got != credential.Redacted {
			t.Fatal("templated nested JSON published a credential label")
		}
	}
}

func TestCredentialFreeMalformedTemplatesRemainReadable(t *testing.T) {
	for _, body := range []string{`{"ordinary":{{var.number}}}`, `{"ordinary":`, "message=" + url.QueryEscape(`{"ordinary":{{var.number}}}`)} {
		script := &dsl.Script{Name: "safe authoring", Protocol: "http", Actions: []dsl.Action{{Type: "POST", Target: "/", Body: body}}}
		if safe, err := Project(script); err != nil || safe == nil || safe.Name != script.Name {
			t.Fatal("credential-free authoring became undisclosable")
		}
	}
}

func TestAnonymousGraphQLEnvelopeKeepsCredentialReplicaCollection(t *testing.T) {
	const secret = "synthetic-anonymous-graphql"
	body, _ := json.Marshal(map[string]any{"query": `{ login(password:"` + secret + `") { ok } }`})
	script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Type: "POST", Target: "/graphql", Body: string(body)}}}
	safe, err := Project(script)
	if err != nil || safe == nil || safe.Name != credential.Redacted || strings.Contains(safe.Actions[0].Body, secret) {
		t.Fatal("anonymous GraphQL envelope lost safe credential collection")
	}
}

func TestMalformedCollectionHintConsumesSharedValueBudget(t *testing.T) {
	collector := newSecretCollector(nil, nil)
	collector.structuredBudget.values = 3
	collector.collectStructuredBody([]byte(`{! `+strings.Repeat(`"" `, 100)), "")
	if !collector.structuredBudget.incomplete {
		t.Fatal("malformed hint ignored shared token budget")
	}
}

func TestJSONInsideQueryFieldMasksNestedCredentialReplicas(t *testing.T) {
	const secret = "synthetic-query-json-credential"
	for _, query := range []string{`{"apiToken":"` + secret + `","recordId":"safe"}`, `[{"apiToken":"` + secret + `","recordId":"safe"}]`} {
		body, _ := json.Marshal(map[string]any{"query": query})
		script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Name: secret, Type: "POST", Target: "/", Body: string(body)}}}
		safe, err := Project(script)
		if err != nil || safe == nil || safe.Name != credential.Redacted || safe.Actions[0].Name != credential.Redacted || strings.Contains(safe.Actions[0].Body, secret) {
			t.Fatal("encoded JSON query bypassed credential collection/projection")
		}
		if got := ProjectLabel(secret, script); got != credential.Redacted {
			t.Fatal("encoded JSON query credential label exposed")
		}
	}
}

func TestMissingJSONTerminatorKeepsCompleteCredentialCollection(t *testing.T) {
	const secret = "synthetic-complete-scalar"
	for _, malformed := range []string{
		`{"apiToken":"` + secret + `"`,
		`{"ordinary":{"apiToken":"` + secret + `"}`,
		`[{"apiToken":"` + secret + `"}`,
	} {
		quoted, _ := json.Marshal(malformed)
		for _, body := range []string{malformed, "message=" + url.QueryEscape(malformed), `{"payload":` + string(quoted) + `}`} {
			script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Name: secret, Type: "POST", Target: "/", Body: body, Metadata: map[string]any{"note": secret}}}}
			safe, err := Project(script)
			if err != nil || safe == nil || safe.Name != credential.Redacted || safe.Actions[0].Name != credential.Redacted || safe.Actions[0].Metadata["note"] != credential.Redacted || strings.Contains(safe.Actions[0].Body, secret) {
				t.Fatal("complete malformed-body credential was not safely collected")
			}
			if script.Actions[0].Body != body || script.Name != secret {
				t.Fatal("disclosure changed executable source")
			}
			if ProjectLabel(secret, script) != credential.Redacted {
				t.Fatal("complete malformed-body credential survived label projection")
			}
		}
	}
}

func TestCredentialEOFBeforeCompleteScalarStillRejectsDisclosure(t *testing.T) {
	const secret = "synthetic-unread-scalar"
	for _, malformed := range []string{
		`{"apiToken":"` + secret,
		`{"apiToken":`,
		`{"ordinary":"safe","apiToken`,
		`{"apiToken":"` + secret + `",`,
		`{"apiToken":"known"} {"apiToken":"` + secret + `"}`,
	} {
		script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Type: "POST", Target: "/", Body: malformed}}}
		if safe, err := Project(script); err == nil || safe != nil {
			t.Fatal("unread malformed credential yielded partial disclosure")
		}
		if ProjectLabel(secret, script) != credential.Redacted {
			t.Fatal("unread malformed credential yielded partial label")
		}
	}
}

func TestUnknownCredentialTemplatesAndFullScalarReplicasAreMasked(t *testing.T) {
	for ordinal, literal := range []string{"{{raw-secret}}", "{{fn.not_a_builtin(raw-token)}}", "{{run.not_supported}}", "{{fn.random(2,1)}}", "{{fn.random_string(0)}}"} {
		for _, form := range []bool{false, true} {
			bodyBytes, _ := json.Marshal(map[string]string{"password": literal, "ordinary": "safe"})
			body := string(bodyBytes)
			if form {
				body = "password=" + url.QueryEscape(literal) + "&ordinary=safe"
			}
			script := &dsl.Script{Name: literal, Protocol: "http", Actions: []dsl.Action{{Name: literal, Type: "POST", Target: "/", Body: body, Metadata: map[string]any{"note": literal}}}}
			safe, err := Project(script)
			if err != nil || safe == nil || safe.Name != credential.Redacted || safe.Actions[0].Name != credential.Redacted || safe.Actions[0].Metadata["note"] != credential.Redacted || strings.Contains(safe.Actions[0].Body, literal) {
				t.Fatalf("unknown credential template or copied full scalar survived: ordinal=%d form=%t failed=%t name_masked=%t action_masked=%t note_masked=%t body_retained=%t", ordinal, form, err != nil || safe == nil, safe != nil && safe.Name == credential.Redacted, safe != nil && safe.Actions[0].Name == credential.Redacted, safe != nil && safe.Actions[0].Metadata["note"] == credential.Redacted, safe != nil && strings.Contains(safe.Actions[0].Body, literal))
			}
			if ProjectLabel(literal, script) != credential.Redacted {
				t.Fatal("unknown credential template survived independent label projection")
			}
			if script.Name != literal || script.Actions[0].Body != body {
				t.Fatal("credential-template projection changed executable source")
			}
		}
	}
}

func TestSupportedRuntimeReferencesRemainPortableInSensitiveFields(t *testing.T) {
	for _, reference := range []string{
		"{{var.value}}", "{{param.value}}", "{{data.source.column}}", "{{env.VALUE}}", "{{secret.VALUE}}", "{{file.upload}}", "{{run.id}}",
		"{{ fn.uuid() }}", "{{fn.uuid.v4}}", "{{fn.timestamp.unix()}}", "{{fn.timestamp.unix_ms}}", "{{fn.timestamp.iso8601}}", "{{fn.timestamp.rfc3339()}}",
		"{{fake.uuid()}}", "{{fake.email()}}", "{{fn.random(-2, 2)}}", "{{fn.random_string(12)}}",
	} {
		for _, value := range []string{reference, "Bearer " + reference} {
			script := &dsl.Script{Name: "safe", Protocol: "http", Actions: []dsl.Action{{Type: "GET", Target: "/", Auth: &dsl.AuthConfig{Type: "bearer", Token: value}, HeaderList: dsl.HeaderList{{Name: "Authorization", Value: value}}}}}
			safe, err := Project(script)
			if err != nil || safe == nil || safe.Actions[0].Auth.Token != value || safe.Actions[0].HeaderList[0].Value != value {
				t.Fatal("supported runtime binding was resolved or masked")
			}
		}
	}
}

func TestJSONLikeDoubleBracesCannotHideCredentialReplicas(t *testing.T) {
	const secret = "synthetic-double-brace-credential"
	for _, malformed := range []string{`{{"apiToken":"` + secret + `"}}`, `{{"api\u0054oken":"` + secret + `"}}`, `{{apiToken:` + secret + `}}`, `{{fn.unknown("apiToken":"` + secret + `")}}`} {
		quoted, _ := json.Marshal(malformed)
		for _, body := range []string{malformed, "message=" + url.QueryEscape(malformed), `{"payload":` + string(quoted) + `}`} {
			for _, encoding := range []string{"", "base64"} {
				stored := body
				if encoding != "" {
					stored = base64.StdEncoding.EncodeToString([]byte(body))
				}
				script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Name: secret, Type: "POST", Target: "/", Body: stored, BodyEncoding: encoding}}}
				if safe, err := Project(script); err == nil || safe != nil {
					t.Fatal("JSON-like double braces produced partial credential disclosure")
				}
				if ProjectLabel(secret, script) != credential.Redacted {
					t.Fatal("JSON-like double braces exposed copied credential label")
				}
				if script.Name != secret || script.Actions[0].Body != stored {
					t.Fatal("JSON-like double-brace projection changed source")
				}
			}
		}
	}
}

func TestMalformedQueryMemberSyntaxCannotPublishCredentialReplicas(t *testing.T) {
	const secret = "synthetic-query-credential"
	for _, inner := range []string{
		`{{"apiToken":"` + secret + `"}}`,
		`{ordinary:invalid,"apiToken":"` + secret + `"}`,
		`{ ordinary: safe "apiToken" : "` + secret + `" }`,
		`{!"api\u0054oken":"` + secret + `"}`,
		"{#ignored\n\"apiToken\":\"" + secret + "\"}",
		`{'apiToken':'` + secret + `'}`,
		`{ apiToken: '` + secret + `' }`,
		`query { "apiToken":"` + secret + `" }`,
		`mutation Invalid {ordinary:invalid,"apiToken":"` + secret + `"}`,
		"#comment\n{\"apiToken\":\"" + secret + "\"}",
		`{"apiToken" "` + secret + `"}`,
		`{"apiToken"`,
		`{ ordinary: safe "apiToken" "` + secret + `" }`,
		`query { lookup(input:{"apiToken" "` + secret + `"}) { id } }`,
		`prefix ' {\"apiToken\":\"` + secret + `\"}`,
	} {
		quoted, _ := json.Marshal(inner)
		queryBody := `{"query":` + string(quoted) + `}`
		outer, _ := json.Marshal(queryBody)
		for _, body := range []string{queryBody, `{"payload":` + string(outer) + `}`, "message=" + url.QueryEscape(queryBody), "query=" + url.QueryEscape(inner)} {
			for _, encoding := range []string{"", "base64"} {
				stored := body
				if encoding != "" {
					stored = base64.StdEncoding.EncodeToString([]byte(body))
				}
				script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Name: secret, Type: "POST", Target: "/", Body: stored, BodyEncoding: encoding}}}
				if safe, err := Project(script); err == nil || safe != nil {
					t.Fatal("malformed query published partial credential disclosure")
				}
				if ProjectLabel(secret, script) != credential.Redacted {
					t.Fatal("malformed query exposed copied credential label")
				}
				if script.Name != secret || script.Actions[0].Body != stored {
					t.Fatal("query projection changed executable source")
				}
			}
		}
	}
}

func TestQueryStructureBudgetCannotFallBackToGraphQLCollection(t *testing.T) {
	inner := `{ lookup(recordId:"safe") { id } }`
	quoted, _ := json.Marshal(inner)
	body := []byte(`{"query":` + string(quoted) + `}`)
	collector := newSecretCollector(nil, nil)
	collector.structuredBudget.bytes = 2*len(body) + 2*len(inner) - 1
	collector.collectStructuredBody(body, "")
	if !collector.structuredBudget.incomplete {
		t.Fatal("exhausted query dispatch continued with partial GraphQL collection")
	}
}

func TestDeclaredGraphQLMalformedMembersCannotPublishReplicas(t *testing.T) {
	const secret = "synthetic-declared-query-credential"
	for _, query := range []string{`query { "apiToken":"` + secret + `" }`, `{"apiToken" "` + secret + `"}`, `{'apiToken':'` + secret + `'}`} {
		for _, encoding := range []string{"", "base64"} {
			body := query
			if encoding != "" {
				body = base64.StdEncoding.EncodeToString([]byte(body))
			}
			script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Name: secret, Type: "POST", Target: "/", Headers: map[string]string{"Content-Type": "application/graphql"}, Body: body, BodyEncoding: encoding}}}
			if safe, err := Project(script); err == nil || safe != nil {
				t.Fatal("declared malformed GraphQL yielded partial disclosure")
			}
			if ProjectLabel(secret, script) != credential.Redacted {
				t.Fatal("declared malformed GraphQL yielded credential label")
			}
		}
	}
}

func TestGraphQLAcceptedCollectionHonorsSharedValueBudget(t *testing.T) {
	collector := newSecretCollector(nil, nil)
	collector.structuredBudget.values = 2
	collector.collectStructuredBody([]byte(`{ lookup(recordId:"safe") { id } }`), "application/graphql")
	if !collector.structuredBudget.incomplete {
		t.Fatal("accepted GraphQL collection ignored shared value allowance")
	}
}

func TestRawAndFormInferredGraphQLCannotBypassQueryAdmission(t *testing.T) {
	const secret = "synthetic-inferred-query-credential"
	for _, query := range []string{`query Synthetic {"apiToken":"` + secret + `"}`, `mutation Synthetic {ordinary:invalid,"apiToken":"` + secret + `"}`, `query {"apiToken" "` + secret + `"}`, `query { lookup(input:[{ordinary:"safe", "apiToken" "` + secret + `"}]) { id } }`, `prefix {'apiToken':'` + secret + `'}`, `prefix { apiToken:'` + secret + `' }`, `prefix '{"apiToken":"` + secret + `"}`} {
		sourceBody := query
		// Unknown prefixes are query-field evidence, not an inferred raw protocol.
		if strings.HasPrefix(query, "prefix ") {
			quoted, _ := json.Marshal(query)
			sourceBody = `{"query":` + string(quoted) + `}`
		}
		for _, body := range []string{sourceBody, "query=" + url.QueryEscape(query)} {
			for _, contentType := range []string{"", "text/plain", "application/octet-stream"} {
				for _, encoding := range []string{"", "base64"} {
					stored := body
					if encoding != "" {
						stored = base64.StdEncoding.EncodeToString([]byte(body))
					}
					script := &dsl.Script{Name: secret, Protocol: "http", Actions: []dsl.Action{{Name: secret, Type: "POST", Target: "/", Body: stored, BodyEncoding: encoding}}}
					if contentType != "" {
						script.Actions[0].Headers = map[string]string{"Content-Type": contentType}
					}
					if safe, err := Project(script); err == nil || safe != nil {
						t.Fatal("inferred/form GraphQL produced partial credential disclosure")
					}
					if ProjectLabel(secret, script) != credential.Redacted {
						t.Fatal("inferred/form GraphQL produced credential label")
					}
					if script.Name != secret || script.Actions[0].Body != stored {
						t.Fatal("inferred query admission changed source")
					}
				}
			}
		}
	}
}

func TestNeutralQueryAndSentinelProjectionIsStable(t *testing.T) {
	for _, query := range []string{`SELECT "recordId" FROM "Account"`, `ordinary search "nearby phrase"`, "select accountId from records where name = 'safe'", "ordinary search + nearby?", "Customer O'Brian", credential.Redacted} {
		quoted, _ := json.Marshal(query)
		for _, body := range []string{`{"query":` + string(quoted) + `,"recordId":"safe"}`, "ordinary=one%20two&query=" + url.QueryEscape(query) + "&flag"} {
			script := &dsl.Script{Name: "safe", Protocol: "http", Actions: []dsl.Action{{Type: "POST", Target: "/", Body: body}}}
			safe, err := Project(script)
			if err != nil || safe == nil || safe.Actions[0].Body != body {
				t.Fatal("neutral query/sentinel lost readable structure")
			}
			repeated, err := Project(safe)
			if err != nil || repeated == nil || repeated.Actions[0].Body != body {
				t.Fatal("repeated neutral query/sentinel projection changed source structure")
			}
		}
	}
	for _, query := range []string{`query Lookup { lookup(recordId:"safe") { id } }`, `mutation Save { save(recordId:"safe") { id } }`, `fragment Fields on Record { id }`, `query Lookup { lookup(scale:1.00e+2, offset:-2.5E-1, ordinary:"safe") { id } }`} {
		script := &dsl.Script{Name: "safe", Protocol: "http", Actions: []dsl.Action{{Type: "POST", Target: "/", Body: query}}}
		if safe, err := Project(script); err != nil || safe == nil || safe.Actions[0].Body != query {
			t.Fatal("valid inferred named GraphQL body changed")
		}
	}
}

// A project-secret reference bound inside a URL nested in a query value is
// shown, not masked again, while the nested URL's other credentials are.
func TestProjectKeepsReferencesBoundInsideNestedURLs(t *testing.T) {
	target := "/login?redirect=https%3A%2F%2Finner.example.test%2Fsso%3Fpassword%3D{{secret.NESTED_PASSWORD}}%26token%3Dsynthetic-nested-token-9c1e%26safe%3Dok&x=1"
	script := &dsl.Script{Name: "Nested", Protocol: "http", Actions: []dsl.Action{{Name: "Login", Type: "request", Method: "GET", Target: target}}}
	safe, err := Project(script)
	if err != nil || safe == nil {
		t.Fatalf("projection failed: %v", err)
	}
	want := "/login?redirect=https%3A%2F%2Finner.example.test%2Fsso%3Fpassword%3D{{secret.NESTED_PASSWORD}}%26token%3D" + credential.Redacted + "%26safe%3Dok&x=1"
	if got := safe.Actions[0].Target; got != want {
		t.Fatalf("target = %s, want %s", got, want)
	}
}
