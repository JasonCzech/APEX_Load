package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/storage"
)

func buildSyntheticArchiveZip(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("create zip header %s: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestEnterpriseSOAPAndHTTPImportSaveAndAuthoringEditsPreserveUnredactedDSL(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "authoring-e2e.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: storage.DefaultProjectID, Name: "Default", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})

	const (
		soapUser     = "synth.soap.operator"
		soapPass     = "SynthSoapPass!2026#Literal"
		soapSecret2  = "SynthSoapApiSecret!2026#Second"
		soapLongOp   = "urn:synthetic:enterprise:provisioning:v2:ExecuteProvisioningOrderOperationWithExtendedContractName"
		httpBearer   = "Bearer SynthHttpBearerToken!998877"
		csvPassValue = "SynthCsvRowPass!4455"
	)

	soapActionC := fmt.Sprintf(`Action()
{
	web_add_header("SOAPAction", "%s");
	web_rest("SubmitProvisioningOrder",
		"URL=https://soap.synthetic.example.test/services/ProvisioningService",
		"Method=POST",
		"EncType=text/xml; charset=utf-8",
		"Body=<?xml version=\"1.0\" encoding=\"UTF-8\"?>"
		"<soapenv:Envelope xmlns:soapenv=\"http://schemas.xmlsoap.org/soap/envelope/\" xmlns:n1=\"urn:synthetic:auth\">"
		"<soapenv:Header>"
		"<!--Zero or more repetitions:-->"
		"<n1:authHeader><n1:userName>%s</n1:userName><n1:userPassword>%s</n1:userPassword><n1:apiSecret>%s</n1:apiSecret></n1:authHeader>"
		"</soapenv:Header>"
		"<soapenv:Body><n1:SubmitOrder><n1:orderId>ORD-1001</n1:orderId></n1:SubmitOrder></soapenv:Body>"
		"</soapenv:Envelope>",
		LAST);
	return 0;
}
`, soapLongOp, soapUser, soapPass, soapSecret2)

	soapArchive := buildSyntheticArchiveZip(t, map[string][]byte{
		"SynthSoapOrder/SynthSoapOrder.usr": []byte("[General]\nType=Multi\nDefaultCfg=default.cfg\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nvuser_end=vuser_end.c\n"),
		"SynthSoapOrder/default.cfg":        []byte("[WEB]\nProxyUseBrowser=1\nProxyUseProxy=1\nProxyHTTPHost=proxy.synthetic.example.test\nProxyHTTPPort=8080\n"),
		"SynthSoapOrder/vuser_init.c":       []byte("vuser_init() { return 0; }\n"),
		"SynthSoapOrder/Action.c":           []byte(soapActionC),
		"SynthSoapOrder/vuser_end.c":        []byte("vuser_end() { return 0; }\n"),
	})

	httpActionC := fmt.Sprintf(`Action()
{
	web_add_header("Authorization", "%s");
	web_custom_request("CreateSession",
		"URL=https://api.synthetic.example.test/v1/sessions",
		"Method=POST",
		"EncType=application/json",
		"Body={\"username\":\"{p_user}\",\"password\":\"{p_pass}\"}",
		LAST);
	return 0;
}
`, httpBearer)

	httpPrm := "[parameter:p_user]\nColumnName=\"user\"\nDelimiter=\",\"\nGenerateNewVal=\"EachIteration\"\nOriginalValue=\"\"\nOutOfRangePolicy=\"ContinueWithLast\"\nParamName=\"p_user\"\nSelectNextRow=\"Sequential\"\nStartRow=\"1\"\nTable=\"users.dat\"\nTableLocation=\"Local\"\nType=\"Table\"\n[parameter:p_pass]\nColumnName=\"password\"\nDelimiter=\",\"\nGenerateNewVal=\"SameLine\"\nOriginalValue=\"\"\nOutOfRangePolicy=\"ContinueWithLast\"\nParamName=\"p_pass\"\nSelectNextRow=\"Same line as p_user\"\nStartRow=\"1\"\nTable=\"users.dat\"\nTableLocation=\"Local\"\nType=\"Table\"\n"
	httpDat := fmt.Sprintf("user,password\nsynth_alice,%s\n", csvPassValue)

	httpArchive := buildSyntheticArchiveZip(t, map[string][]byte{
		"SynthHttpSession/SynthHttpSession.usr": []byte("[General]\nType=Multi\nDefaultCfg=default.cfg\nParameterFile=SynthHttpSession.prm\n[Actions]\nvuser_init=vuser_init.c\nAction=Action.c\nvuser_end=vuser_end.c\n"),
		"SynthHttpSession/default.cfg":          []byte("[WEB]\nProxyUseBrowser=1\nProxyUseProxy=1\n"),
		"SynthHttpSession/SynthHttpSession.prm": []byte(httpPrm),
		"SynthHttpSession/users.dat":            []byte(httpDat),
		"SynthHttpSession/vuser_init.c":         []byte("vuser_init() { return 0; }\n"),
		"SynthHttpSession/Action.c":             []byte(httpActionC),
		"SynthHttpSession/vuser_end.c":          []byte("vuser_end() { return 0; }\n"),
	})

	for _, tc := range []struct {
		name           string
		archive        []byte
		filename       string
		mustContain    []string
		checkParamPrev bool
	}{
		{
			name:        "soap_web_rest_usz",
			archive:     soapArchive,
			filename:    "SynthSoapOrder.usz",
			mustContain: []string{soapUser, soapPass, soapSecret2, "<!--Zero or more repetitions:-->"},
		},
		{
			name:           "http_custom_request_usz",
			archive:        httpArchive,
			filename:       "SynthHttpSession.usz",
			mustContain:    []string{httpBearer, csvPassValue},
			checkParamPrev: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bodyBuf bytes.Buffer
			mw := multipart.NewWriter(&bodyBuf)
			part, err := mw.CreateFormFile("file", tc.filename)
			if err != nil {
				t.Fatalf("CreateFormFile: %v", err)
			}
			if _, err := part.Write(tc.archive); err != nil {
				t.Fatalf("write archive: %v", err)
			}
			if err := mw.Close(); err != nil {
				t.Fatalf("close multipart: %v", err)
			}

			importReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive?project_id="+storage.DefaultProjectID, &bodyBuf)
			importReq.Header.Set("Content-Type", mw.FormDataContentType())
			importResp := httptest.NewRecorder()
			server.mux.ServeHTTP(importResp, importReq)
			if importResp.Code != http.StatusOK {
				t.Fatalf("import status = %d body=%s", importResp.Code, importResp.Body.String())
			}
			// encoding/json escapes '<' and '>' as \u003c and \u003e in raw HTTP JSON;
			// unescape HTML entities before substring matching so XML comments and tags
			// can be asserted cleanly across every JSON envelope.
			unescapeJSONHTML := strings.NewReplacer(`\u003c`, `<`, `\u003e`, `>`, `\u0026`, `&`)
			importRaw := unescapeJSONHTML.Replace(importResp.Body.String())
			if strings.Contains(importRaw, "[REDACTED]") {
				t.Fatalf("import response unexpectedly contained [REDACTED]: %s", importRaw)
			}
			for _, literal := range tc.mustContain {
				if !strings.Contains(importRaw, literal) {
					t.Fatalf("import response missing literal %q", literal)
				}
			}

			var imported struct {
				Script dsl.Script `json:"script"`
			}
			if err := json.Unmarshal(importResp.Body.Bytes(), &imported); err != nil {
				t.Fatalf("decode import response: %v", err)
			}

			savePayload, err := json.Marshal(map[string]any{
				"operation_id": "op-save-" + tc.name,
				"project_id":   storage.DefaultProjectID,
				"scripts":      []dsl.Script{imported.Script},
			})
			if err != nil {
				t.Fatalf("marshal save payload: %v", err)
			}
			saveReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive/save", bytes.NewReader(savePayload))
			saveReq.Header.Set("Content-Type", "application/json")
			saveResp := httptest.NewRecorder()
			server.mux.ServeHTTP(saveResp, saveReq)
			if saveResp.Code != http.StatusCreated {
				t.Fatalf("save status = %d body=%s", saveResp.Code, saveResp.Body.String())
			}
			if strings.Contains(saveResp.Body.String(), "[REDACTED]") {
				t.Fatalf("save response unexpectedly contained [REDACTED]: %s", saveResp.Body.String())
			}
			var savedBatch struct {
				Scripts []StoredScript `json:"scripts"`
			}
			if err := json.Unmarshal(saveResp.Body.Bytes(), &savedBatch); err != nil {
				t.Fatalf("decode save response: %v", err)
			}
			if len(savedBatch.Scripts) != 1 {
				t.Fatalf("saved scripts count = %d, want 1", len(savedBatch.Scripts))
			}
			scriptID := savedBatch.Scripts[0].ID

			for _, path := range []string{
				"/api/v1/scripts/" + scriptID + "?view=authoring",
				"/api/v1/scripts/" + scriptID,
				"/api/v1/scripts",
			} {
				readReq := httptest.NewRequest(http.MethodGet, path, nil)
				readResp := httptest.NewRecorder()
				server.mux.ServeHTTP(readResp, readReq)
				if readResp.Code != http.StatusOK {
					t.Fatalf("GET %s status = %d body=%s", path, readResp.Code, readResp.Body.String())
				}
				decodedBody := unescapeJSONHTML.Replace(readResp.Body.String())
				if strings.Contains(decodedBody, "[REDACTED]") {
					t.Fatalf("GET %s unexpectedly contained [REDACTED]: %s", path, decodedBody)
				}
				for _, literal := range tc.mustContain {
					if !strings.Contains(decodedBody, literal) {
						t.Fatalf("GET %s missing literal %q", path, literal)
					}
				}
			}

			// Load authoring snapshot with ETag for Save #1 and Save #2.
			loadAuthoring := func() (StoredScript, string) {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/"+scriptID+"?view=authoring", nil)
				resp := httptest.NewRecorder()
				server.mux.ServeHTTP(resp, req)
				if resp.Code != http.StatusOK {
					t.Fatalf("GET authoring status = %d body=%s", resp.Code, resp.Body.String())
				}
				var stored StoredScript
				if err := json.Unmarshal(resp.Body.Bytes(), &stored); err != nil {
					t.Fatalf("decode authoring StoredScript: %v", err)
				}
				return stored, resp.Header().Get("ETag")
			}

			firstRead, firstETag := loadAuthoring()
			if firstRead.Script == nil {
				t.Fatal("firstRead.Script is nil")
			}
			edit1 := *firstRead.Script
			edit1.Config.HTTP.ProxyMode = "none"
			edit1.Config.HTTP.Proxy = dsl.ProxyConfig{}
			if edit1.Metadata == nil {
				edit1.Metadata = &dsl.ScriptMetadata{}
			}
			if edit1.Metadata.Import == nil {
				edit1.Metadata.Import = &dsl.ImportMetadata{Source: "enterprise_script_archive"}
			}
			if edit1.Metadata.Import.Preserved == nil {
				edit1.Metadata.Import.Preserved = map[string]any{}
			}
			edit1.Metadata.Import.Preserved["source_mapping_review"] = map[string]any{
				"importRevision": "rev-1",
				"capturedAt":     "2026-11-10T18:00:00Z",
				"snapshot":       map[string]any{"actionCount": len(edit1.Actions)},
				"bindings":       []any{map[string]any{"sourceFile": "Action.c", "targetPath": "actions[0]"}},
				"decisions":      map[string]any{"actions[0]": "reviewed"},
			}

			putScript := func(payload dsl.Script, ifMatch string) StoredScript {
				t.Helper()
				raw, err := json.Marshal(map[string]any{"script": payload})
				if err != nil {
					t.Fatalf("marshal PUT script: %v", err)
				}
				req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+scriptID, bytes.NewReader(raw))
				req.Header.Set("Content-Type", "application/json")
				if ifMatch != "" {
					req.Header.Set("If-Match", ifMatch)
				}
				resp := httptest.NewRecorder()
				server.mux.ServeHTTP(resp, req)
				if resp.Code != http.StatusOK {
					t.Fatalf("PUT status = %d body=%s", resp.Code, resp.Body.String())
				}
				if strings.Contains(resp.Body.String(), "[REDACTED]") {
					t.Fatalf("PUT response unexpectedly contained [REDACTED]: %s", resp.Body.String())
				}
				var updated StoredScript
				if err := json.Unmarshal(resp.Body.Bytes(), &updated); err != nil {
					t.Fatalf("decode PUT response: %v", err)
				}
				return updated
			}

			afterSave1 := putScript(edit1, firstETag)
			if afterSave1.Script.Config.HTTP.ProxyMode != "none" {
				t.Fatalf("after Save #1 proxy_mode = %q, want none", afterSave1.Script.Config.HTTP.ProxyMode)
			}

			// Save #2: reload via ?view=authoring, update source_mapping_review decisions, and PUT again.
			secondRead, secondETag := loadAuthoring()
			edit2 := *secondRead.Script
			reviewMap, ok := edit2.Metadata.Import.Preserved["source_mapping_review"].(map[string]any)
			if !ok {
				t.Fatalf("Save #1 did not preserve source_mapping_review: %#v", edit2.Metadata.Import.Preserved["source_mapping_review"])
			}
			reviewMap["decisions"] = map[string]any{"actions[0]": "verified-second-save"}
			afterSave2 := putScript(edit2, secondETag)
			if afterSave2.Script.Config.HTTP.ProxyMode != "none" {
				t.Fatalf("after Save #2 proxy_mode = %q, want none", afterSave2.Script.Config.HTTP.ProxyMode)
			}
			afterSave2JSON, _ := json.Marshal(afterSave2)
			decodedAfterSave2 := unescapeJSONHTML.Replace(string(afterSave2JSON))
			for _, literal := range tc.mustContain {
				if !strings.Contains(decodedAfterSave2, literal) {
					t.Fatalf("after Save #2 missing literal %q", literal)
				}
			}

			if tc.checkParamPrev {
				prevBody, _ := json.Marshal(parameterPreviewRequest{
					Parameters:  afterSave2.Script.Parameters,
					DataSources: afterSave2.Script.DataSources,
					Name:        "p_pass",
					VUser:       1,
					Iteration:   1,
					Occurrence:  1,
				})
				prevReq := httptest.NewRequest(http.MethodPost, "/api/v1/dsl/parameter-preview", bytes.NewReader(prevBody))
				prevReq.Header.Set("Content-Type", "application/json")
				prevResp := httptest.NewRecorder()
				server.mux.ServeHTTP(prevResp, prevReq)
				if prevResp.Code != http.StatusOK {
					t.Fatalf("parameter preview status = %d body=%s", prevResp.Code, prevResp.Body.String())
				}
				var preview parameterPreviewResponse
				if err := json.Unmarshal(prevResp.Body.Bytes(), &preview); err != nil {
					t.Fatalf("decode parameter preview: %v", err)
				}
				if preview.Masked || preview.Value != csvPassValue {
					t.Fatalf("parameter preview = %+v, want unmasked %q", preview, csvPassValue)
				}
			}
		})
	}
}

func TestVuGenFolderAndZipUploadSkipsOversizedResultDirsAndReplayHAR(t *testing.T) {
	// Construct a multipart folder upload with a simulated >64 MiB FileHeader.Size
	// on result1/Iteration1/trans.json and replay.har so we verify they are skipped
	// BEFORE MaxArchiveEntryBytes rejection without allocating 130 MiB in test RAM.
	makeHeader := func(field, filename string, content []byte, declaredSize int64) (*multipart.FileHeader, map[string][]*multipart.FileHeader) {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
		h.Set("Content-Type", "application/octet-stream")
		part, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		mr := multipart.NewReader(&b, mw.Boundary())
		form, err := mr.ReadForm(1 << 20)
		if err != nil {
			t.Fatal(err)
		}
		fh := form.File[field][0]
		if declaredSize > 0 {
			fh.Size = declaredSize
		}
		return fh, form.File
	}

	fields := map[string][]*multipart.FileHeader{}
	addField := func(relPath string, content []byte, size int64) {
		key := "file:" + url.PathEscape(relPath)
		fh, _ := makeHeader(key, filepath.Base(relPath), content, size)
		fields[key] = []*multipart.FileHeader{fh}
	}

	oversized := int64(enterprisearchive.MaxArchiveEntryBytes) + (10 << 20) // 74 MiB
	addField("SynthVuGen/SynthVuGen.usr", []byte("[General]\nType=Multi\n[Actions]\nAction=Action.c\n"), 0)
	addField("SynthVuGen/Action.c", []byte(`Action() { web_url("Home", "URL=https://example.test/", LAST); return 0; }`), 0)
	addField("SynthVuGen/replay.har", []byte(`{"log":{"entries":[]}}`), oversized)
	addField("SynthVuGen/result1/Iteration1/trans.json", []byte(`{}`), oversized)
	addField("SynthVuGen/results/summary.html", []byte(`<html></html>`), oversized)

	zipped, err := zipEnterpriseFolderUpload(fields)
	if err != nil {
		t.Fatalf("zipEnterpriseFolderUpload rejected VuGen replay artifacts: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		t.Fatalf("open zipped folder output: %v", err)
	}
	for _, f := range zr.File {
		if strings.Contains(f.Name, "replay.har") || strings.Contains(f.Name, "result1/") || strings.Contains(f.Name, "results/") {
			t.Fatalf("unexpected non-source VuGen artifact retained in archive: %s", f.Name)
		}
	}

	// Verify path traversal inside result1/ is still rejected loudly.
	badFields := map[string][]*multipart.FileHeader{
		"file:SynthVuGen/SynthVuGen.usr":            fields["file:"+url.PathEscape("SynthVuGen/SynthVuGen.usr")],
		"file:SynthVuGen/result1/../../traversal.c": fields["file:"+url.PathEscape("SynthVuGen/Action.c")],
	}
	if _, err := zipEnterpriseFolderUpload(badFields); err == nil {
		t.Fatal("expected traversal path inside result1/ to be rejected")
	}
}

func TestKafkaOAuthClientSecretLiteralCRUDAndRuntimeProjection(t *testing.T) {
	server := NewServer(ServerConfig{})
	const literalSecret = "SynthKafkaOAuthSecret!2026"
	createPayload := dsl.Script{
		Name:     "Synthetic Kafka OAuth Producer",
		Version:  "1.0.0",
		Protocol: "kafka",
		Config: dsl.ScriptConfig{
			Kafka: dsl.KafkaConfig{
				Mock:             true,
				Brokers:          []string{"kafka.synthetic.example.test:9093"},
				Topic:            "orders.v1",
				SecurityProtocol: "sasl_ssl",
				SASLMechanism:    "oauthbearer",
				OAuth: &dsl.KafkaOAuthConfig{
					Mode:         "client_credentials",
					TokenURL:     "https://idp.synthetic.example.test/oauth2/token",
					ClientID:     "synth-kafka-client",
					ClientSecret: literalSecret,
				},
			},
		},
		Actions: []dsl.Action{
			{Name: "ProduceOrder", Type: "kafka_produce", Body: `{"id":"1"}`},
		},
	}
	raw, err := json.Marshal(map[string]any{"script": createPayload})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/scripts status = %d body=%s", resp.Code, resp.Body.String())
	}
	var created StoredScript
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Script == nil || created.Script.Config.Kafka.OAuth == nil || created.Script.Config.Kafka.OAuth.ClientSecret != literalSecret {
		t.Fatalf("created Kafka OAuth config = %#v, want literal client_secret", created.Script)
	}

	// Runtime/run-snapshot projection (maskScriptSecrets) must still mask client_secret!
	runtimeProjected := maskScriptSecrets(created.Script)
	if runtimeProjected == nil || runtimeProjected.Config.Kafka.OAuth == nil || runtimeProjected.Config.Kafka.OAuth.ClientSecret != "[REDACTED]" {
		t.Fatalf("maskScriptSecrets(created.Script) OAuth = %#v, want [REDACTED]", runtimeProjected.Config.Kafka.OAuth)
	}
}

func TestLibraryScriptHashCanonicalUnmaskedWithLegacyMaskedFallback(t *testing.T) {
	scriptA := &dsl.Script{
		Name:     "Auth Flow",
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.synthetic.example.test"},
		Actions: []dsl.Action{{
			Name:    "Login",
			Type:    "request",
			Method:  http.MethodPost,
			Target:  "/login",
			Headers: map[string]string{"Authorization": "Bearer SynthSecretTokenA"},
		}},
	}
	scriptB := cloneScriptDefinition(scriptA)
	scriptB.Actions[0].Headers["Authorization"] = "Bearer SynthSecretTokenB"

	hashA := hashLibraryScript(scriptA)
	hashB := hashLibraryScript(scriptB)
	if hashA == hashB {
		t.Fatalf("canonical unmasked hashLibraryScript did not distinguish distinct literal credentials: %s", hashA)
	}
	legacyHash := legacyMaskedHashLibraryScript(scriptA)
	if !libraryScriptHashMatches(scriptA, hashA) {
		t.Fatal("libraryScriptHashMatches rejected canonical unmasked hash")
	}
	if !libraryScriptHashMatches(scriptA, legacyHash) {
		t.Fatal("libraryScriptHashMatches rejected backward-compatible legacy masked hash")
	}
}

func TestCloneDraftPreservesLiteralCredentialsDefaultAndMigratesCleanly(t *testing.T) {
	server, records, _, projectID := newProjectSecretAPIServer(t)
	const (
		httpPass   = "SynthCloneHttpPass!2026"
		soapPassA  = "SynthCloneSoapPassA!2026"
		soapPassB  = "SynthCloneSoapPassB!2026"
		csvPass    = "SynthCloneCsvPass!2026"
		jvmPropVal = "-Dapi.secret=SynthCloneJvmPass!2026"
	)
	soapBody := `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Header><userPassword>` + soapPassA + `</userPassword></soapenv:Header><soapenv:Body><secPass><password>` + soapPassB + `</password></secPass></soapenv:Body></soapenv:Envelope>`
	source := &dsl.Script{
		ID:        "clone-source-script",
		Name:      "Clone Source",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: projectID,
		Config: dsl.ScriptConfig{
			BaseURL: "https://api.synthetic.example.test",
			HTTP: dsl.HTTPConfig{
				Auth: dsl.AuthConfig{Type: "basic", Username: "svc_user", Password: httpPass},
			},
			JavaVuser: dsl.JavaVuserConfig{
				AdditionalVMParameters: []string{jvmPropVal},
			},
		},
		DataSources: []dsl.DataSource{{
			Name:       "creds",
			Type:       "csv",
			Columns:    []string{"username", "password"},
			InlineData: "username,password\nop1," + csvPass + "\n",
		}},
		Actions: []dsl.Action{{
			Name:    "InvokeSOAP",
			Type:    "request",
			Method:  http.MethodPost,
			Target:  "/ws",
			Headers: map[string]string{"Content-Type": "text/xml; charset=utf-8"},
			Body:    soapBody,
		}},
	}
	if err := records.SaveScript(source); err != nil {
		t.Fatal(err)
	}
	if err := server.runner.reloadStoredScripts(); err != nil {
		t.Fatal(err)
	}

	// Default clone (ImportDetectedCredentialValues == false) keeps every literal intact.
	defaultClone := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+source.ID+"/clone-draft", map[string]any{})
	if defaultClone.Code != http.StatusOK {
		t.Fatalf("default clone status = %d body=%s", defaultClone.Code, defaultClone.Body.String())
	}
	defaultBody := defaultClone.Body.String()
	if strings.Contains(defaultBody, "[REDACTED]") {
		t.Fatalf("default clone introduced [REDACTED]: %s", defaultBody)
	}
	for _, want := range []string{httpPass, soapPassA, soapPassB, csvPass, jvmPropVal} {
		if !strings.Contains(defaultBody, want) {
			t.Fatalf("default clone lost literal %q: %s", want, defaultBody)
		}
	}
	var defaultDecoded struct {
		Script       dsl.Script                 `json:"script"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(defaultClone.Body.Bytes(), &defaultDecoded); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(defaultDecoded.Script.Config.HTTP.Auth.Password, "{{secret.") {
		t.Fatalf("default clone unexpectedly rewrote HTTP auth password to secret ref: %q", defaultDecoded.Script.Config.HTTP.Auth.Password)
	}
	for _, req := range defaultDecoded.Requirements {
		if strings.Contains(req.Replacement, "[REDACTED]") {
			t.Fatalf("clone requirement %s Replacement contains [REDACTED]: %q", req.ID, req.Replacement)
		}
		for _, secret := range []string{httpPass, soapPassA, soapPassB} {
			if strings.Contains(req.Replacement, secret) {
				t.Fatalf("default clone requirement %s Replacement leaked plaintext %q: %q", req.ID, secret, req.Replacement)
			}
		}
	}

	// Opt-in credential migration (ImportDetectedCredentialValues == true) converts
	// bindable credentials (including structurally projected formatted JSON) to {{secret.*}}
	// without masking CSV inline_data or JVM args to [REDACTED], preserves annotation metadata,
	// and leaves non-JSON non-linear fields intact on editable without leaking plaintext in Replacement.
	const (
		jsonPass      = "SynthCloneJsonPass!2026"
		nonLinearVal  = "SynthXmlUnalignedPass!2026"
		nonLinearTok  = "SynthXmlUnalignedToken!2026"
		unalignedBody = "<login><password><![CDATA[" + nonLinearVal + "</password><token>tail]]></password><token>" + nonLinearTok + "</token></login>"
	)
	source.Actions = append(source.Actions,
		dsl.Action{
			Name:    "FormattedJSONLogin",
			Type:    "request",
			Method:  http.MethodPost,
			Target:  "/json-login",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    "{\n  \"z_note\": \"keep-json\",\n  \"password\": \"" + jsonPass + "\"\n}",
		},
		dsl.Action{
			Name:    "UnalignedXMLLogin",
			Type:    "request",
			Method:  http.MethodPost,
			Target:  "/xml-login",
			Headers: map[string]string{"Content-Type": "text/xml; charset=utf-8"},
			Body:    unalignedBody,
		},
	)
	if err := records.SaveScript(source); err != nil {
		t.Fatal(err)
	}
	if err := server.runner.reloadStoredScripts(); err != nil {
		t.Fatal(err)
	}

	migratedClone := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+source.ID+"/clone-draft", map[string]any{
		"import_detected_credential_values": true,
	})
	if migratedClone.Code != http.StatusOK {
		t.Fatalf("migrated clone status = %d body=%s", migratedClone.Code, migratedClone.Body.String())
	}
	migratedBody := migratedClone.Body.String()
	if strings.Contains(migratedBody, "[REDACTED]") {
		t.Fatalf("migrated clone left [REDACTED] in response: %s", migratedBody)
	}
	for _, bound := range []string{httpPass, soapPassA, soapPassB, jsonPass} {
		if strings.Contains(migratedBody, bound) {
			t.Fatalf("migrated clone leaked bindable credential %q: %s", bound, migratedBody)
		}
	}
	for _, preserved := range []string{csvPass, jvmPropVal, "keep-json"} {
		if !strings.Contains(migratedBody, preserved) {
			t.Fatalf("migrated clone corrupted non-bindable field %q: %s", preserved, migratedBody)
		}
	}
	var migratedDecoded struct {
		Script       dsl.Script                 `json:"script"`
		Requirements []SecretBindingRequirement `json:"secret_binding_requirements"`
	}
	if err := json.Unmarshal(migratedClone.Body.Bytes(), &migratedDecoded); err != nil {
		t.Fatal(err)
	}
	if len(migratedDecoded.Script.Actions) < 3 || migratedDecoded.Script.Actions[2].Body != unalignedBody {
		t.Fatalf("migrated clone corrupted unaligned XML body: got=%q want=%q", migratedDecoded.Script.Actions[2].Body, unalignedBody)
	}
	foundAnnotatedGroup := false
	foundRebindUnavailable := false
	for _, req := range migratedDecoded.Requirements {
		if strings.Contains(req.Replacement, "[REDACTED]") {
			t.Fatalf("migrated requirement %s Replacement contains [REDACTED]: %q", req.ID, req.Replacement)
		}
		for _, secret := range []string{httpPass, soapPassA, soapPassB, jsonPass, nonLinearVal, nonLinearTok} {
			if strings.Contains(req.Replacement, secret) {
				t.Fatalf("migrated requirement %s Replacement leaked plaintext %q: %q", req.ID, secret, req.Replacement)
			}
		}
		if req.ValueGroupID != "" && req.DestinationValuePreserved {
			foundAnnotatedGroup = true
		}
		if req.RebindUnavailableReason != "" {
			foundRebindUnavailable = true
		}
	}
	if !foundAnnotatedGroup || !foundRebindUnavailable {
		t.Fatalf("migrated clone lost requirement annotations: group=%v rebindUnavailable=%v reqs=%#v", foundAnnotatedGroup, foundRebindUnavailable, migratedDecoded.Requirements)
	}
}

func TestVuGenNonSourceSkippingWindowsPathsDirCountsTraversalAndSupplemental(t *testing.T) {
	server := NewServer(ServerConfig{})
	const (
		usrContent = "[General]\nType=Multi\nDefaultCfg=default.cfg\n[Actions]\nAction=Action.c\n"
		cContent   = "Action()\n{\n\tweb_url(\"home\", \"URL=https://app.synthetic.example.test/home\", LAST);\n\treturn 0;\n}\n"
		cfgContent = "[ThinkTime]\nOptions=RECORDED\n"
	)

	t.Run("windows_backslashes_and_result_dir_counts", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		addEntry := func(name string, content []byte) {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(content); err != nil {
				t.Fatal(err)
			}
		}
		addEntry(`MyScript\test.usr`, []byte(usrContent))
		addEntry(`MyScript\Action.c`, []byte(cContent))
		addEntry(`MyScript\default.cfg`, []byte(cfgContent))
		addEntry(`MyScript\result1\replay.har`, []byte(`{"log":{"entries":[]}}`))
		for i := 0; i < enterprisearchive.MaxArchiveEntries+50; i++ {
			addEntry(fmt.Sprintf(`MyScript\result1\iter_%d\`, i), nil)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}

		var mp bytes.Buffer
		mw := multipart.NewWriter(&mp)
		part, err := mw.CreateFormFile("file", "windows_vugen.usz")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(buf.Bytes())
		_ = mw.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", &mp)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("Windows VuGen archive import status = %d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("traversal_in_result_dir_rejected", func(t *testing.T) {
		archive := buildSyntheticArchiveZip(t, map[string][]byte{
			"MyScript/test.usr":               []byte(usrContent),
			"MyScript/Action.c":               []byte(cContent),
			"MyScript/result1/../../evil.txt": []byte("evil"),
		})
		var mp bytes.Buffer
		mw := multipart.NewWriter(&mp)
		part, _ := mw.CreateFormFile("file", "traversal.usz")
		_, _ = part.Write(archive)
		_ = mw.Close()

		if _, err := mergeEnterpriseArchiveZipWithPreparedFiles(archive, nil); err == nil {
			t.Fatal("mergeEnterpriseArchiveZipWithPreparedFiles accepted traversal entry inside result1")
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", &mp)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusUnprocessableEntity && resp.Code != http.StatusBadRequest {
			t.Fatalf("traversal archive status = %d, want 400/422 body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("supplemental_default_cfg_merges_despite_result1_default_cfg_and_large_replay_har", func(t *testing.T) {
		largeHar := bytes.Repeat([]byte("a"), enterprisearchive.MaxArchiveEntryBytes+1024)
		archive := buildSyntheticArchiveZip(t, map[string][]byte{
			"MyScript/test.usr":            []byte(usrContent),
			"MyScript/Action.c":            []byte(cContent),
			"MyScript/default.cfg":         []byte(cfgContent),
			"MyScript/result1/default.cfg": []byte("[ThinkTime]\nOptions=RECORDED\n"),
			"MyScript/replay.har":          largeHar,
		})
		var mp bytes.Buffer
		mw := multipart.NewWriter(&mp)
		part, _ := mw.CreateFormFile("file", "supp_vugen.usz")
		_, _ = part.Write(archive)
		supp, _ := mw.CreateFormFile("supplemental_file", "default.cfg")
		_, _ = supp.Write([]byte("[ThinkTime]\nOptions=NOTHINK\n"))
		_ = mw.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", &mp)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("supplemental VuGen archive import status = %d body=%s", resp.Code, resp.Body.String())
		}
		if !strings.Contains(resp.Body.String(), "NOTHINK") {
			t.Fatalf("supplemental default.cfg did not replace MyScript/default.cfg: %s", resp.Body.String())
		}

		// Direct call to mergeEnterpriseArchiveZipWithSupplementalFiles with unstripped >64 MiB replay.har must also succeed.
		mergedDirect, mergeErr := mergeEnterpriseArchiveZipWithSupplementalFiles(archive, []string{"default.cfg"}, [][]byte{[]byte("[ThinkTime]\nOptions=NOTHINK\n")})
		if mergeErr != nil || len(mergedDirect) == 0 {
			t.Fatalf("direct mergeEnterpriseArchiveZipWithSupplementalFiles failed on >64 MiB replay.har: %v", mergeErr)
		}
	})

	t.Run("trailing_slash_symlink_in_result_dir_rejected", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, content := range map[string][]byte{
			"MyScript/test.usr": []byte(usrContent),
			"MyScript/Action.c": []byte(cContent),
		} {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(content)
		}
		symlinkHdr := &zip.FileHeader{Name: "MyScript/result1/evil_link/", Method: zip.Store}
		symlinkHdr.SetMode(fs.ModeSymlink | 0o777)
		symlinkW, err := zw.CreateHeader(symlinkHdr)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = symlinkW.Write([]byte("/etc/passwd"))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		archive := buf.Bytes()

		if _, err := mergeEnterpriseArchiveZipWithPreparedFiles(archive, nil); err == nil {
			t.Fatal("mergeEnterpriseArchiveZipWithPreparedFiles accepted trailing-slash symlink entry")
		}
		var mp bytes.Buffer
		mw := multipart.NewWriter(&mp)
		part, _ := mw.CreateFormFile("file", "symlink.usz")
		_, _ = part.Write(archive)
		_ = mw.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", &mp)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusUnprocessableEntity && resp.Code != http.StatusBadRequest {
			t.Fatalf("trailing-slash symlink archive status = %d, want 400/422 body=%s", resp.Code, resp.Body.String())
		}
	})
}

func TestBindKafkaCredentialsClearsLiteralClientSecret(t *testing.T) {
	script := &dsl.Script{
		Name:     "Kafka Repair",
		Version:  "1.0.0",
		Protocol: "kafka",
		Config: dsl.ScriptConfig{
			Kafka: dsl.KafkaConfig{
				Mock:             true,
				Brokers:          []string{"kafka.synthetic.example.test:9093"},
				Topic:            "events",
				SecurityProtocol: "sasl_ssl",
				SASLMechanism:    "oauthbearer",
				OAuth: &dsl.KafkaOAuthConfig{
					Mode:         "client_credentials",
					TokenURL:     "https://idp.synthetic.example.test/oauth2/token",
					ClientID:     "raw-client-id",
					ClientSecret: "SynthKafkaRawSecret!2026",
				},
			},
		},
		Actions: []dsl.Action{{Name: "send", Type: "kafka_produce", Body: "{}"}},
	}
	err := bindJMeterMessagingCredentials(script, ImportRepairRequest{
		Finding: "jmeter_kafka_oauth_credentials_required",
		Operation: ImportRepairOperation{
			Type:           "bind_kafka_credentials",
			ClientIDSecret: "KAFKA_CLIENT_ID",
			SecretName:     "KAFKA_CLIENT_SECRET",
		},
	})
	if err != nil {
		t.Fatalf("bindJMeterMessagingCredentials error: %v", err)
	}
	if script.Config.Kafka.OAuth.ClientSecret != "" || script.Config.Kafka.OAuth.ClientSecretRef != "{{secret.KAFKA_CLIENT_SECRET}}" {
		t.Fatalf("OAuth after bind_kafka_credentials = %#v", script.Config.Kafka.OAuth)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("ValidateScriptIssues after bind_kafka_credentials = %v", issues)
	}
}

func TestDegradedTokenlessImportDraftMutationsSucceed(t *testing.T) {
	server := NewServer(ServerConfig{})
	const bearer = "SynthTokenlessDraftSecret!2026"
	draft := &dsl.Script{
		Name:     "Tokenless Import Draft",
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://app.synthetic.example.test"},
		Actions: []dsl.Action{{
			Name:    "GetAccount",
			Type:    "request",
			Method:  http.MethodGet,
			Target:  "https://app.synthetic.example.test/accounts/1",
			Headers: map[string]string{"Authorization": "Bearer " + bearer},
		}},
	}
	ensureCredentialPreservingImportMetadata(draft, "har")
	stripImportDraftAttestation(draft)

	// 1. Transport apply succeeds on tokenless draft and preserves literal credential.
	transportBody, _ := json.Marshal(importTransportApplyRequest{
		Script: cloneScriptDefinition(draft),
		Edits:  []requestimport.TransportEdit{{Path: "actions[0]", Origin: "https://app.synthetic.example.test", From: "", To: "2"}},
	})
	transportReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/transport", bytes.NewReader(transportBody))
	transportReq.Header.Set("Content-Type", "application/json")
	transportResp := httptest.NewRecorder()
	server.handleApplyImportTransport(transportResp, transportReq)
	if transportResp.Code != http.StatusOK || !strings.Contains(transportResp.Body.String(), bearer) {
		t.Fatalf("tokenless transport apply = %d body=%s", transportResp.Code, transportResp.Body.String())
	}

	// 2. Import settings apply succeeds on tokenless draft and preserves literal credential.
	settingsBody, _ := json.Marshal(ImportSettingsRequest{
		Script:   cloneScriptDefinition(draft),
		Category: "HTTP",
		Changes:  []ImportSettingsChange{settingChange("config.http.request_timeout_ms", "4500")},
	})
	settingsReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/settings", bytes.NewReader(settingsBody))
	settingsReq.Header.Set("Content-Type", "application/json")
	settingsResp := httptest.NewRecorder()
	server.handleImportSettings(settingsResp, settingsReq)
	if settingsResp.Code != http.StatusOK || !strings.Contains(settingsResp.Body.String(), bearer) {
		t.Fatalf("tokenless settings apply = %d body=%s", settingsResp.Code, settingsResp.Body.String())
	}

	// 3. Tokenless credential-preserving POST /api/v1/scripts honors Idempotency-Key without creating duplicates.
	durableServer, _, _, projectID := newProjectSecretAPIServer(t)
	tokenlessCreate := cloneScriptDefinition(draft)
	tokenlessCreate.ProjectID = projectID
	createPayload, _ := json.Marshal(map[string]any{"script": tokenlessCreate})
	doCreate := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(createPayload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "idem-tokenless-raw-cred-1")
		resp := httptest.NewRecorder()
		durableServer.mux.ServeHTTP(resp, req)
		return resp
	}
	firstCreate := doCreate()
	secondCreate := doCreate()
	if firstCreate.Code != http.StatusCreated || secondCreate.Code != http.StatusCreated {
		t.Fatalf("idempotent tokenless create codes = %d, %d; first=%s second=%s", firstCreate.Code, secondCreate.Code, firstCreate.Body.String(), secondCreate.Body.String())
	}
	var created1, created2 StoredScript
	if err := json.Unmarshal(firstCreate.Body.Bytes(), &created1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(secondCreate.Body.Bytes(), &created2); err != nil {
		t.Fatal(err)
	}
	if created1.ID == "" || created1.ID != created2.ID {
		t.Fatalf("Idempotency-Key replay created distinct scripts: %q vs %q", created1.ID, created2.ID)
	}

	// 4. Saving an attested import draft after replacing ALL raw credentials with {{secret.*}} succeeds (201 Created).
	attestedCandidate := &dsl.Script{
		Name:      "Attested All-Bound Draft",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: projectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://app.synthetic.example.test"},
		Actions: []dsl.Action{{
			Name:    "GetAccount",
			Type:    "request",
			Method:  http.MethodGet,
			Target:  "https://app.synthetic.example.test/accounts/1",
			Headers: map[string]string{"Authorization": "Bearer SynthAttestedSecret!2026"},
		}},
	}
	ensureCredentialPreservingImportMetadata(attestedCandidate, "har")
	importReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", nil)
	envelope := durableServer.newScriptImportResponseWithSourceSecrets(importReq, scriptImportResponseOptions{Script: attestedCandidate})
	encodedDraft, err := json.Marshal(envelope["script"])
	if err != nil {
		t.Fatal(err)
	}
	var attestedDraft dsl.Script
	if err := json.Unmarshal(encodedDraft, &attestedDraft); err != nil {
		t.Fatal(err)
	}
	if importDraftAttestationToken(&attestedDraft) == "" {
		t.Fatal("expected attested import draft token")
	}
	attestedDraft.Actions[0].Headers["Authorization"] = "Bearer {{secret.BOUND_ACCOUNT_TOKEN}}"
	boundSavePayload, _ := json.Marshal(map[string]any{"script": &attestedDraft})
	boundSaveReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(boundSavePayload))
	boundSaveReq.Header.Set("Content-Type", "application/json")
	boundSaveResp := httptest.NewRecorder()
	durableServer.mux.ServeHTTP(boundSaveResp, boundSaveReq)
	if boundSaveResp.Code != http.StatusCreated {
		t.Fatalf("attested draft save with all credentials bound to {{secret.*}} status = %d body=%s", boundSaveResp.Code, boundSaveResp.Body.String())
	}
}

func TestFullDigestAttestedDraftAllBoundSavesAndRejectsTampering(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x5a}, 32),
		KeyID: "full-digest-attested-e2e",
	})
	if err != nil {
		t.Fatal(err)
	}
	modes := []struct {
		name      string
		newServer func(t *testing.T) *Server
	}{
		{
			name: "memory_attestation",
			newServer: func(t *testing.T) *Server {
				store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "mem-att.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				now := time.Now().UTC()
				if err := store.SaveProject(&storage.Project{ID: storage.DefaultProjectID, Name: "Default", CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
				return NewServer(ServerConfig{Store: store})
			},
		},
		{
			name: "protected_store_attestation",
			newServer: func(t *testing.T) *Server {
				store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "prot-att.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				now := time.Now().UTC()
				if err := store.SaveProject(&storage.Project{ID: storage.DefaultProjectID, Name: "Default", CreatedAt: now, UpdatedAt: now}); err != nil {
					t.Fatal(err)
				}
				return NewServer(ServerConfig{
					Store:                store,
					BrunoArtifactDir:     t.TempDir(),
					BrunoArtifactWrapper: wrapper,
				})
			},
		},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			srv := mode.newServer(t)

			mintDraft := func(candidate *dsl.Script) dsl.Script {
				t.Helper()
				ensureCredentialPreservingImportMetadata(candidate, "har")
				req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", nil)
				env := srv.newScriptImportResponseWithSourceSecrets(req, scriptImportResponseOptions{Script: candidate})
				raw, err := json.Marshal(env["script"])
				if err != nil {
					t.Fatal(err)
				}
				var out dsl.Script
				if err := json.Unmarshal(raw, &out); err != nil {
					t.Fatal(err)
				}
				if importDraftAttestationToken(&out) == "" {
					t.Fatal("expected attestation token on FullDigest draft")
				}
				return out
			}

			postSave := func(draft *dsl.Script) *httptest.ResponseRecorder {
				t.Helper()
				payload, _ := json.Marshal(map[string]any{"script": draft})
				req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(payload))
				req.Header.Set("Content-Type", "application/json")
				resp := httptest.NewRecorder()
				srv.mux.ServeHTTP(resp, req)
				return resp
			}

			// 1. Unaligned XML CDATA FullDigest draft: replacing all raw credentials with {{secret.*}} saves 201.
			xmlDraft := mintDraft(unalignedXMLCredentialScript("SynthFullDigestXmlPass!2026"))
			xmlDraft.Actions[1].Body = "<login><password>{{secret.XML_PASS}}</password><token>{{secret.XML_TOK}}</token></login>"
			if resp := postSave(&xmlDraft); resp.Code != http.StatusCreated {
				t.Fatalf("all-bound unaligned XML FullDigest save = %d body=%s", resp.Code, resp.Body.String())
			}

			// 2. CSV InlineData password column FullDigest draft: replacing raw CSV data with external file / non-credential data saves 201.
			csvCandidate := &dsl.Script{
				Name:      "CSV FullDigest Draft",
				Version:   "1.0.0",
				Protocol:  "http",
				ProjectID: storage.DefaultProjectID,
				Config:    dsl.ScriptConfig{BaseURL: "https://app.synthetic.example.test"},
				DataSources: []dsl.DataSource{{
					Name:       "users",
					Type:       "csv",
					Columns:    []string{"username", "password"},
					InlineData: "username,password\nop1,SynthCsvFullDigestPass!2026\n",
				}},
				Actions: []dsl.Action{{
					Name:   "Login",
					Type:   "request",
					Method: http.MethodPost,
					Target: "/login",
					Body:   "user={{data.users.username}}&pass={{secret.CSV_REPLACED_SECRET}}",
				}},
			}
			csvDraft := mintDraft(csvCandidate)
			csvDraft.DataSources = nil
			if resp := postSave(&csvDraft); resp.Code != http.StatusCreated {
				t.Fatalf("all-bound CSV FullDigest save = %d body=%s", resp.Code, resp.Body.String())
			}

			// 3. Modifying a remaining raw credential on an attested FullDigest draft still returns 409 Conflict.
			tamperedDraft := mintDraft(unalignedXMLCredentialScript("SynthOriginalXmlPass!2026"))
			tamperedDraft.Actions[1].Body = unalignedXMLBody("SynthTamperedXmlPass!2026", "synthetic-xml-token-5b8e")
			if resp := postSave(&tamperedDraft); resp.Code != http.StatusConflict {
				t.Fatalf("tampered FullDigest draft status = %d, want 409 body=%s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestNonStringScalarParamsSecretAnnotationCloneAndApply(t *testing.T) {
	server, records, _, projectID := newProjectSecretAPIServer(t)
	script := &dsl.Script{
		ID:        "numeric-secret-param-script",
		Name:      "Numeric Param Script",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: projectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.synthetic.example.test"},
		Actions: []dsl.Action{{
			Name:   "CallAPI",
			Type:   "request",
			Method: http.MethodGet,
			Target: "/items",
			Params: map[string]any{
				"api_key":         float64(12345),
				"null_secret":     nil,
				"secret_bindings": map[string]any{"API_KEY": true, "OBJ_KEY": map[string]any{"id": "u1"}},
				"proxy":           map[string]any{"username": map[string]any{"id": "u1"}},
				"field":           map[string]any{"type": "input", "name": "password", "value": true, "values": map[string]any{"v": 1}},
			},
		}},
	}
	if err := records.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	if err := server.runner.reloadStoredScripts(); err != nil {
		t.Fatal(err)
	}

	// 1. Import preview annotates numeric, null, bool, and object-projected redactions as DestinationValuePreserved + RebindUnavailableReason.
	previewCandidate := cloneScriptDefinition(script)
	previewCandidate.ID = ""
	ensureCredentialPreservingImportMetadata(previewCandidate, "har")
	importReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", nil)
	preview := server.newScriptImportResponseWithSourceSecrets(importReq, scriptImportResponseOptions{Script: previewCandidate})
	reqs, _ := preview["secret_binding_requirements"].([]SecretBindingRequirement)
	byPath := make(map[string]SecretBindingRequirement, len(reqs))
	for _, r := range reqs {
		byPath[r.Path] = r
	}
	for _, wantPath := range []string{
		"/actions/0/params/api_key",
		"/actions/0/params/null_secret",
		"/actions/0/params/secret_bindings/API_KEY",
		"/actions/0/params/secret_bindings/OBJ_KEY",
		"/actions/0/params/proxy/username",
		"/actions/0/params/field/value",
		"/actions/0/params/field/values",
	} {
		got, ok := byPath[wantPath]
		if !ok {
			t.Fatalf("missing requirement at %s in %#v", wantPath, byPath)
		}
		if !got.DestinationValuePreserved || got.PreservationUnavailableReason != "" || got.RebindUnavailableReason != secretBindingRebindUnavailableReason {
			t.Fatalf("requirement %s = %#v, want DestinationValuePreserved=true, PreservationUnavailableReason=\"\", RebindUnavailableReason=%q", wantPath, got, secretBindingRebindUnavailableReason)
		}
	}

	// 2. Unattested POST /api/v1/scripts save with null_secret, bool, and map[string]any succeeds (201 Created, no 409 Conflict).
	unattestedCandidate := cloneScriptDefinition(script)
	unattestedCandidate.ID = ""
	unattestedCandidate.Name = "Saved Unattested NonString Script"
	createResp := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts", map[string]any{
		"script": unattestedCandidate,
	})
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/scripts with null_secret/bool/object params = %d body=%s, want 201", createResp.Code, createResp.Body.String())
	}

	// 3. POST /api/v1/scripts/{id}/clone-draft with import_detected_credential_values: true returns 200 OK (no 500) and preserves 12345 without [REDACTED].
	cloneResp := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+script.ID+"/clone-draft", map[string]any{
		"import_detected_credential_values": true,
	})
	if cloneResp.Code != http.StatusOK {
		t.Fatalf("clone-draft with non-string secret params = %d body=%s", cloneResp.Code, cloneResp.Body.String())
	}
	if strings.Contains(cloneResp.Body.String(), "[REDACTED]") || !strings.Contains(cloneResp.Body.String(), "12345") {
		t.Fatalf("clone-draft body lost numeric param or introduced [REDACTED]: %s", cloneResp.Body.String())
	}

	// 4. POST /api/v1/scripts/secret-bindings/apply returns 400 secret_binding_rebind_unavailable (not 409 stale) for numeric, bool, and object requirements.
	for _, wantPath := range []string{
		"/actions/0/params/api_key",
		"/actions/0/params/secret_bindings/API_KEY",
		"/actions/0/params/proxy/username",
	} {
		reqItem := byPath[wantPath]
		applyResp := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/secret-bindings/apply", map[string]any{
			"script": preview["script"],
			"bindings": []map[string]any{{
				"requirement_id": reqItem.ID,
				"secret_name":    "BOUND_NON_STRING_SECRET",
			}},
		})
		if applyResp.Code != http.StatusBadRequest || !strings.Contains(applyResp.Body.String(), secretBindingRebindUnavailableCode) {
			t.Fatalf("secret-bindings/apply on %s = %d body=%s, want 400 %s", wantPath, applyResp.Code, applyResp.Body.String(), secretBindingRebindUnavailableCode)
		}
	}
}
