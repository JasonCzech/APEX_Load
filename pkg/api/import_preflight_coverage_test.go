package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/jmeter"
)

// TestImportRoutesAttachReplayPreflight is the coverage guard for replay
// preflight.
//
// Preflight is the only place an import tells an operator whether the draft can
// actually be replayed — missing variables, unsatisfied secret references,
// unseeded data sources, review gates. It is attached per handler rather than
// by the shared response envelope, so a new importer silently ships without it
// unless something enforces the invariant. Every row here asserts both halves:
// the response field the UI reads, and the copy preserved on the script so a
// later save keeps it.
//
// Two import routes are deliberately absent and cannot be table-driven here:
// eventhubs-capture requires a valid Avro OCF payload, and github requires a
// reachable GitHub API. Both attach preflight in their handlers; their coverage
// is the handler code path itself.
func TestImportRoutesAttachReplayPreflight(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		body        func(t *testing.T) (payload []byte, contentType string)
		multiScript bool
	}{
		{
			name: "raml",
			path: "/api/v1/scripts/import/raml",
			body: jsonImportBody(map[string]any{
				"name":       "Orders",
				"project_id": "proj-preflight",
				"content": `#%RAML 1.0
title: Orders
baseUri: https://api.example.test
/orders:
  get:
`,
			}),
		},
		{
			name: "wsdl",
			path: "/api/v1/scripts/import/wsdl",
			body: jsonImportBody(map[string]any{
				"name":       "Accounts",
				"project_id": "proj-preflight",
				"content": `<definitions name="AccountService" targetNamespace="urn:accounts" xmlns="http://schemas.xmlsoap.org/wsdl/" xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/">
  <portType name="AccountPortType"><operation name="GetAccount"/></portType>
  <binding name="AccountBinding" type="tns:AccountPortType"><operation name="GetAccount"><soap:operation soapAction="urn:GetAccount"/></operation></binding>
  <service name="AccountService"><port name="AccountPort"><soap:address location="https://soap.example.test/account"/></port></service>
</definitions>`,
			}),
		},
		{
			name: "devweb",
			path: "/api/v1/scripts/import/devweb",
			body: jsonImportBody(map[string]any{
				"name":       "DevWeb orders",
				"project_id": "proj-preflight",
				"content": `load.action(async function () {
  await new load.WebRequest({url: "https://api.example.test/orders", method: "GET"}).send();
});`,
			}),
		},
		{
			name: "k6",
			path: "/api/v1/scripts/import/k6",
			body: jsonImportBody(map[string]any{
				"name":       "k6 orders",
				"project_id": "proj-preflight",
				"content": `import http from 'k6/http';
export default function () {
  http.get('https://api.example.test/orders');
}`,
			}),
		},
		{
			name: "broker-config",
			path: "/api/v1/scripts/import/broker-config",
			body: multipartImportBody("broker.mqsc",
				[]byte("DEFINE QLOCAL('ORDERS.IN') REPLACE\n"),
				map[string]string{"project_id": "proj-preflight"},
			),
		},
		{
			name:        "jmeter",
			path:        "/api/v1/scripts/import/jmeter",
			multiScript: true,
			body: multipartImportBody("plan.jmx", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Orders"><boolProp name="TestPlan.functional_mode">false</boolProp></TestPlan>
    <hashTree>
      <ThreadGroup testname="Group"><stringProp name="ThreadGroup.num_threads">1</stringProp><stringProp name="ThreadGroup.ramp_time">1</stringProp></ThreadGroup>
      <hashTree>
        <HTTPSamplerProxy testname="orders">
          <stringProp name="HTTPSampler.domain">api.example.test</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/orders</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`), map[string]string{"project_id": "proj-preflight"}),
		},
		{
			name:        "enterprise-script-archive",
			path:        "/api/v1/scripts/import/enterprise-script-archive",
			multiScript: true,
			body: func(t *testing.T) ([]byte, string) {
				t.Helper()
				archive := buildPreflightCoverageArchive(t, map[string]string{
					"script.usr":  "[General]\nType=Web\n[Actions]\nAction=Action.c\n",
					"default.cfg": "[General]\nDefaultRunLogic=default.usp\n",
					"default.usp": "[RunLogicRunRoot]\nRunLogicNumOfIterations=\"1\"\nRunLogicPaceType=\"ASAP\"\n",
					"Action.c":    "Action()\n{\n\tweb_url(\"orders\",\n\t\t\"URL=https://api.example.test/orders\",\n\t\tLAST);\n\treturn 0;\n}\n",
				})
				return multipartImportBody("archive.zip", archive,
					map[string]string{"project_id": "proj-preflight"})(t)
			},
		},
		{
			name:        "cloudtest",
			path:        "/api/v1/scripts/import/cloudtest",
			multiScript: true,
			body: multipartImportBody("repository.xml", []byte(`<Repository>
  <Clip name="Orders"><HTTP/></Clip>
  <Composition name="Checkout"><Track name="East" clip="Orders"/></Composition>
</Repository>`), map[string]string{"project_id": "proj-preflight"}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			payload, contentType := test.body(t)
			req := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(payload))
			req.Header.Set("Content-Type", contentType)
			resp := httptest.NewRecorder()

			server.mux.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
			}

			var decoded struct {
				Preflight *ReplayPreflight `json:"preflight"`
				Script    *struct {
					Metadata struct {
						Import struct {
							Preserved map[string]any `json:"preserved"`
						} `json:"import"`
					} `json:"metadata"`
				} `json:"script"`
				Scripts []struct {
					Metadata struct {
						Import struct {
							Preserved map[string]any `json:"preserved"`
						} `json:"import"`
					} `json:"metadata"`
				} `json:"scripts"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}

			if decoded.Preflight == nil {
				t.Fatalf("response has no preflight; body=%s", resp.Body.String())
			}
			if decoded.Script == nil {
				t.Fatal("response has no script")
			}
			if decoded.Script.Metadata.Import.Preserved["replay_preflight"] == nil {
				t.Fatalf("script metadata is missing preserved replay_preflight: %#v",
					decoded.Script.Metadata.Import.Preserved)
			}
			if test.multiScript {
				if len(decoded.Scripts) == 0 {
					t.Fatal("multi-script import returned no scripts")
				}
				for index, script := range decoded.Scripts {
					if script.Metadata.Import.Preserved["replay_preflight"] == nil {
						t.Fatalf("scripts[%d] is missing preserved replay_preflight: %#v",
							index, script.Metadata.Import.Preserved)
					}
				}
			}
		})
	}
}

func jsonImportBody(payload map[string]any) func(t *testing.T) ([]byte, string) {
	return func(t *testing.T) ([]byte, string) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		return body, "application/json"
	}
}

func multipartImportBody(filename string, content []byte, fields map[string]string) func(t *testing.T) ([]byte, string) {
	return func(t *testing.T) ([]byte, string) {
		t.Helper()
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write multipart file: %v", err)
		}
		for name, value := range fields {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatalf("write multipart field %q: %v", name, err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close multipart writer: %v", err)
		}
		return buffer.Bytes(), writer.FormDataContentType()
	}
}

func buildPreflightCoverageArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return archive.Bytes()
}

// TestAPIArchiveReadersRejectTraversalEntries pins the shared archive-path
// policy at the API boundary.
//
// These three readers previously used archive/zip directly, so a "../" entry
// name reached downstream lookup keys, sourceview mappings, and source_path
// provenance. Nothing wrote it to disk, but the name propagated. The readers now
// go through pkg/internal/archiveingest (or, where original names must be
// preserved, validate against it), and the import must fail rather than silently
// dropping the entry.
func TestAPIArchiveReadersRejectTraversalEntries(t *testing.T) {
	hostile := map[string]string{
		"../escape.jmx":  "<jmeterTestPlan/>",
		"../escape.raml": "#%RAML 1.0\ntitle: Escape\n",
		"../escape.usr":  "[General]\nType=Web\n",
	}

	t.Run("jmeter", func(t *testing.T) {
		archive := buildPreflightCoverageArchive(t, map[string]string{
			"../escape.jmx": hostile["../escape.jmx"],
		})
		if _, _, _, err := readJMeterZipImport(archive); err == nil {
			t.Fatal("JMeter archive reader accepted a traversal entry")
		}
	})

	t.Run("raml", func(t *testing.T) {
		archive := buildPreflightCoverageArchive(t, map[string]string{
			"../escape.raml": hostile["../escape.raml"],
		})
		if _, _, _, err := extractRAMLZip(archive); err == nil {
			t.Fatal("RAML archive reader accepted a traversal entry")
		}
	})

	t.Run("enterprise supplemental merge", func(t *testing.T) {
		archive := buildPreflightCoverageArchive(t, map[string]string{
			"../escape.usr": hostile["../escape.usr"],
		})
		_, err := mergeEnterpriseArchiveZipWithSupplementalFiles(
			archive, []string{"Action.java"}, [][]byte{[]byte("fresh")})
		if err == nil {
			t.Fatal("enterprise supplemental merge accepted a traversal entry")
		}
	})

	t.Run("enterprise merge keeps benign leading dot-slash", func(t *testing.T) {
		archive := buildPreflightCoverageArchive(t, map[string]string{
			"./MyScript/Demo.usr": "[General]\nType=Web\n",
		})
		if _, err := mergeEnterpriseArchiveZipWithSupplementalFiles(archive, nil, nil); err != nil {
			t.Fatalf("benign \"./\" entry was rejected: %v", err)
		}
	})
}

func TestJMeterZIPConvertsEveryJMXInDeterministicPathOrder(t *testing.T) {
	const jmx = `<jmeterTestPlan version="1.2"><hashTree><TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree><HeaderManager testname="Auth" enabled="true"><collectionProp name="HeaderManager.headers"><elementProp name="" elementType="Header"><stringProp name="Header.name">X-API-Key</stringProp><stringProp name="Header.value">[REDACTED]</stringProp></elementProp></collectionProp></HeaderManager><hashTree/><HTTPSamplerProxy testname="Home" enabled="true"><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`
	archive := buildPreflightCoverageArchive(t, map[string]string{
		"z/second.jmx":   jmx,
		"a/first.jmx":    jmx,
		"data/users.csv": "id\n1\n",
	})
	documents, sidecars, err := readJMeterZipImportAll(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 2 || documents[0].FileName != "a/first.jmx" || documents[1].FileName != "z/second.jmx" {
		t.Fatalf("documents = %#v", documents)
	}
	result, err := convertJMeterImportRequest(&jmeterImportRequest{
		Documents: documents,
		Opts:      jmeter.ConvertOptions{SidecarFiles: sidecars, ScenarioDraft: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %d, want 2", len(result.Scripts))
	}
	if len(result.SecretRequirements) != 2 ||
		result.SecretRequirements[0].SuggestedName == result.SecretRequirements[1].SuggestedName {
		t.Fatalf("multi-JMX credentials were not allocated across the batch: %#v", result.SecretRequirements)
	}
	for index, wantPath := range []string{"a/first.jmx", "z/second.jmx"} {
		if result.SecretRequirements[index].Source.Path != wantPath {
			t.Fatalf("requirement %d source = %q, want %q", index, result.SecretRequirements[index].Source.Path, wantPath)
		}
	}
	if result.Scripts[0].Name == result.Scripts[1].Name ||
		strings.EqualFold(result.Scripts[0].Name, result.Scripts[1].Name) {
		t.Fatalf("multi-JMX script names collide: %q and %q", result.Scripts[0].Name, result.Scripts[1].Name)
	}
	if result.ScenarioDraft == nil || len(result.ScenarioDraft.Groups) != 2 {
		t.Fatalf("scenario draft = %#v, want two groups", result.ScenarioDraft)
	}
	for index, group := range result.ScenarioDraft.Groups {
		if group.ScriptName != result.Scripts[index].Name {
			t.Fatalf("scenario group %d script_name = %q, want %q", index, group.ScriptName, result.Scripts[index].Name)
		}
	}
	for index, wantEntry := range []string{"a/first.jmx", "z/second.jmx"} {
		metadata := result.Scripts[index].Metadata
		if metadata == nil || metadata.Import == nil {
			t.Fatalf("script %d has no import metadata", index)
		}
		if got := metadata.Import.Preserved["source_zip_entry"]; got != wantEntry {
			t.Fatalf("script %d source entry = %v, want %q", index, got, wantEntry)
		}
	}
}

func TestJMeterZIPScopesSameNamedCSVSidecarsToEachJMX(t *testing.T) {
	const jmx = `<jmeterTestPlan version="1.2"><hashTree><TestPlan testname="Plan" enabled="true"/><hashTree><ThreadGroup testname="Users" enabled="true"/><hashTree><CSVDataSet testname="Rows" enabled="true"><stringProp name="filename">data.csv</stringProp><stringProp name="delimiter">,</stringProp></CSVDataSet><hashTree/><HTTPSamplerProxy testname="Lookup" enabled="true"><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.protocol">https</stringProp><stringProp name="HTTPSampler.path">/</stringProp><stringProp name="HTTPSampler.method">GET</stringProp></HTTPSamplerProxy><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`
	archive := buildPreflightCoverageArchive(t, map[string]string{
		"a/plan.jmx": jmx,
		"a/data.csv": "alpha\nfirst\n",
		"b/plan.jmx": jmx,
		"b/data.csv": "beta\nsecond\n",
	})
	documents, sidecars, err := readJMeterZipImportAll(archive)
	if err != nil {
		t.Fatal(err)
	}
	result, err := convertJMeterImportRequest(&jmeterImportRequest{
		Documents: documents,
		Opts:      jmeter.ConvertOptions{SidecarFiles: sidecars},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %d, want 2", len(result.Scripts))
	}
	for index, want := range []string{"alpha\nfirst\n", "beta\nsecond\n"} {
		if len(result.Scripts[index].DataSources) != 1 {
			t.Fatalf("script %d data sources = %#v", index, result.Scripts[index].DataSources)
		}
		if got := result.Scripts[index].DataSources[0].InlineData; got != want {
			t.Fatalf("script %d inline data = %q, want %q", index, got, want)
		}
	}
}
