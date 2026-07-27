package requestimport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestStructuredFormSerializationPreservesOrderDuplicatesAndRawEncoding(t *testing.T) {
	t.Parallel()
	noEquals := false
	body, err := materializeBody(Body{
		Kind: BodyFormURLEncoded,
		FormFields: []FormField{
			{Name: "tag", Value: "first value"},
			{Name: "tag", Value: "second"},
			{Name: "encoded", RawName: "already%20encoded", RawValue: "a%2Fb"},
			{Name: "flag", HadEquals: &noEquals},
		},
	}, "form-candidate")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body.Payload), "tag=first+value&tag=second&already%20encoded=a%2Fb&flag"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
	if body.MediaType != "application/x-www-form-urlencoded" || body.FormFields != nil {
		t.Fatalf("materialized form = %#v", body)
	}
}

func TestStructuredFormRejectsUnencodedRawDelimiters(t *testing.T) {
	t.Parallel()
	_, err := materializeBody(Body{
		Kind:       BodyFormURLEncoded,
		FormFields: []FormField{{RawName: "bad&name", RawValue: "value"}},
	}, "form-candidate")
	if err == nil || !strings.Contains(err.Error(), "unencoded form delimiter") {
		t.Fatalf("error = %v", err)
	}
}

func TestStructuredMultipartSerializationIsDeterministicAndPreservesParts(t *testing.T) {
	t.Parallel()
	request := Request{
		Key:         "upload",
		CandidateID: "candidate-17",
		Target:      "/upload",
		Body: Body{
			Kind: BodyMultipart,
			Multipart: &MultipartBody{Parts: []MultipartPart{
				{Name: "tag", Data: []byte("one")},
				{Name: "tag", Data: []byte("two")},
				{Name: "document", FileName: "payload.bin", ContentType: "application/octet-stream", FileAssetRef: "upload_file"},
			}},
		},
	}
	var firstBody string
	var firstType string
	for iteration := 0; iteration < 25; iteration++ {
		action, report, err := FinalizeAction(request, FinalizeOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if report.Disposition != ExecutableExact {
			t.Fatalf("report = %#v", report)
		}
		if iteration == 0 {
			firstBody, firstType = action.Body, action.Headers["Content-Type"]
		} else if action.Body != firstBody || action.Headers["Content-Type"] != firstType {
			t.Fatalf("multipart output changed on iteration %d", iteration)
		}
	}
	if strings.Count(firstBody, `name="tag"`) != 2 ||
		!strings.Contains(firstBody, "\r\n\r\none\r\n") ||
		!strings.Contains(firstBody, "\r\n\r\ntwo\r\n") ||
		!strings.Contains(firstBody, "{{file.upload_file}}") {
		t.Fatalf("multipart body = %q", firstBody)
	}
	if !strings.HasPrefix(firstType, "multipart/form-data; boundary=----apexload-") {
		t.Fatalf("content type = %q", firstType)
	}
}

func TestStructuredMultipartPreservesBoundaryFromMediaType(t *testing.T) {
	t.Parallel()
	projected, _, err := ProjectCredentials(Request{
		Key: "source-boundary", Target: "/upload",
		Body: Body{
			Kind:      BodyMultipart,
			MediaType: `multipart/form-data; boundary="source-boundary-17"`,
			Multipart: &MultipartBody{Parts: []MultipartPart{{Name: "token", Data: []byte("canary")}}},
		},
	}, CredentialProjectionOptions{Mode: credentialpolicy.SecretReference})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(projected.Body.Payload), "--source-boundary-17\r\n") ||
		projected.Body.MediaType != `multipart/form-data; boundary="source-boundary-17"` {
		t.Fatalf("projected body = %q, media type = %q", projected.Body.Payload, projected.Body.MediaType)
	}
}

func TestStructuredGraphQLSerializationPreservesJSONTypes(t *testing.T) {
	t.Parallel()
	body, err := materializeBody(Body{
		Kind: BodyGraphQL,
		GraphQL: &GraphQLBody{
			Query:         "query Item($id: Int!) { item(id: $id) { id } }",
			OperationName: "Item",
			Variables:     json.RawMessage(` { "id": 7, "enabled": true, "optional": null } `),
			Extensions:    json.RawMessage(`{"persistedQuery":{"version":1}}`),
		},
	}, "graphql-candidate")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"query":"query Item($id: Int!) { item(id: $id) { id } }","operationName":"Item","variables":{"id":7,"enabled":true,"optional":null},"extensions":{"persistedQuery":{"version":1}}}`
	if got := string(body.Payload); got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body.Payload, &decoded); err != nil {
		t.Fatal(err)
	}
	variables := decoded["variables"].(map[string]any)
	if _, ok := variables["id"].(float64); !ok || variables["enabled"] != true || variables["optional"] != nil {
		t.Fatalf("GraphQL variable types changed: %#v", variables)
	}
}

func TestStructuredGraphQLRejectsNonObjectVariables(t *testing.T) {
	t.Parallel()
	_, err := materializeBody(Body{
		Kind:    BodyGraphQL,
		GraphQL: &GraphQLBody{Query: "query { viewer { id } }", Variables: json.RawMessage(`["not","an","object"]`)},
	}, "graphql-candidate")
	if err == nil || !strings.Contains(err.Error(), "variables must be a JSON object") {
		t.Fatalf("error = %v", err)
	}
}

func TestFileBodyCreatesAValidBlockedBindingDraft(t *testing.T) {
	t.Parallel()
	action, report, err := FinalizeAction(Request{
		Key:    "file-body",
		Target: "/upload",
		Method: "POST",
		Body:   Body{Kind: BodyFile, FileAssetRef: "payload"},
	}, FinalizeOptions{ActionPath: "actions[0]"})
	if err != nil {
		t.Fatal(err)
	}
	serializedReport, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serializedReport), "bound bytes") || strings.Contains(string(serializedReport), `"assets"`) {
		t.Fatalf("protected report asset leaked through JSON: %s", serializedReport)
	}
	script := validBaseScript()
	script.Actions = []dsl.Action{action}
	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != BlockedDraft || result.Readiness.ReplayReady ||
		result.Script.Actions[0].Body != "{{file.payload}}" ||
		result.Script.Actions[0].Metadata["execution_blocker"] != "import_missing_file_bindings" {
		t.Fatalf("result = %#v", result)
	}
}

func TestFileBodyCarriesBoundAssetThroughFinalizeReport(t *testing.T) {
	t.Parallel()
	action, report, err := FinalizeAction(Request{
		Key:    "bound-file-body",
		Target: "/upload",
		Method: "POST",
		Body: Body{
			Kind:         BodyFile,
			FileAssetRef: "payload",
			Assets:       []Asset{{Name: "payload", FileName: "payload.txt", Data: []byte("bound bytes")}},
		},
	}, FinalizeOptions{ActionPath: "actions[0]"})
	if err != nil {
		t.Fatal(err)
	}
	script := validBaseScript()
	script.Actions = []dsl.Action{action}
	result, err := FinalizeScript(script, []Report{report}, FinalizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != ExecutableExact || !result.Readiness.ReplayReady ||
		len(result.Script.DataSources) != 1 ||
		result.Script.DataSources[0].InlineData != "bound bytes" {
		t.Fatalf("result = %#v", result)
	}
}

func TestStructuredCredentialProjectionCoversMultipartAndGraphQL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		request Request
	}{
		{
			name: "multipart",
			request: Request{
				Key: "multipart-secret", Target: "/upload",
				Body: Body{Kind: BodyMultipart, Multipart: &MultipartBody{Parts: []MultipartPart{
					{Name: "password", Data: []byte("canary-multipart")},
				}}},
			},
		},
		{
			name: "graphql variables",
			request: Request{
				Key: "graphql-secret", Target: "/graphql",
				Body: Body{Kind: BodyGraphQL, GraphQL: &GraphQLBody{
					Query:     "mutation Login($input: Login!) { login(input: $input) { ok } }",
					Variables: json.RawMessage(`{"input":{"password":"canary-graphql"}}`),
				}},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			projected, report, err := ProjectCredentials(test.request, CredentialProjectionOptions{
				Mode: credentialpolicy.SecretReference,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(projected.Body.Payload), "{{secret.") ||
				strings.Contains(string(projected.Body.Payload), "canary-") {
				t.Fatalf("projected body = %q", projected.Body.Payload)
			}
			if report.Disposition != ExecutableExact || len(report.Requirements) != 1 {
				t.Fatalf("report = %#v", report)
			}
		})
	}
}

func TestWireBodyCredentialProjectionCoversRawAndMultipart(t *testing.T) {
	t.Parallel()
	const boundary = "wire-boundary"
	tests := []struct {
		name       string
		body       Body
		wantStable string
	}{
		{
			name: "raw body governed as one value",
			body: Body{
				Kind:      BodyRaw,
				MediaType: "text/plain",
				Payload:   []byte("password = canary-raw\nnote = retained-inside-governed-value"),
			},
		},
		{
			name: "multipart nested structured credential",
			body: Body{
				Kind:      BodyMultipart,
				MediaType: "multipart/form-data; boundary=" + boundary,
				Payload: []byte(
					"--" + boundary + "\r\n" +
						"Content-Disposition: form-data; name=\"payload\"\r\n" +
						"Content-Type: application/json\r\n\r\n" +
						"{\"password\":\"canary-multipart-wire\",\"count\":1}\r\n" +
						"--" + boundary + "--\r\n",
				),
			},
			wantStable: `"count":1`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			projected, report, err := ProjectCredentials(Request{
				Key: "wire-secret", Target: "/wire", Method: "POST", Body: test.body,
			}, CredentialProjectionOptions{Mode: credentialpolicy.SecretReference})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(projected.Body.Payload), "canary-") ||
				!strings.Contains(string(projected.Body.Payload), "{{secret.") {
				t.Fatalf("projected body = %q", projected.Body.Payload)
			}
			if test.wantStable != "" && !strings.Contains(string(projected.Body.Payload), test.wantStable) {
				t.Fatalf("projected body lost stable bytes %q: %q", test.wantStable, projected.Body.Payload)
			}
			if report.Disposition != ExecutableExact || len(report.Requirements) != 1 {
				t.Fatalf("report = %#v", report)
			}
		})
	}
}

func TestStructuredBodyMaterializationDoesNotMutateCaller(t *testing.T) {
	t.Parallel()
	equals := true
	request := Request{
		Key: "clone", Target: "/form",
		Body: Body{Kind: BodyFormURLEncoded, FormFields: []FormField{{
			Name: "token", Value: "source", HadEquals: &equals,
		}}},
	}
	projected, _, err := ProjectCredentials(request, CredentialProjectionOptions{Mode: credentialpolicy.SecretReference})
	if err != nil {
		t.Fatal(err)
	}
	if request.Body.FormFields[0].Value != "source" || request.Body.FormFields[0].HadEquals != &equals {
		t.Fatalf("caller mutated: %#v", request)
	}
	if reflect.DeepEqual(projected.Body, request.Body) {
		t.Fatal("projection did not detach/materialize the body")
	}
}
