package enterprisearchive

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertZipExplainsXMLBodyDeclaredAsJSON(t *testing.T) {
	t.Parallel()
	const body = `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><password>soap-credential-canary</password></s:Body></s:Envelope>`
	for _, contentType := range []string{"application/json", "text/xml; charset=utf-8"} {
		t.Run(contentType, func(t *testing.T) {
			source := "Action()\n{\n\tlr_start_transaction(\"SOAP request\");\n\tweb_set_sockets_option(\"SSL_VERSION\", \"TLS1.2\");\n\n\n" +
				"\tweb_rest(\"SOAP request\",\n\t\t\"URL=https://example.test/soap\",\n\t\t\"Method=POST\",\n\t\t" + strconv.Quote("Body="+body) + ",\n\t\tHEADERS,\n\t\t\"Name=Content-Type\", " + strconv.Quote("Value="+contentType) + ", ENDHEADER,\n\t\tLAST);\n\tlr_end_transaction(\"SOAP request\", LR_AUTO);\n}\n"
			archive := buildSourceZip(t, map[string]string{
				"SOAP.usr": "[General]\nType=Web\n[Actions]\nAction=Action.c\n", "Action.c": source,
			})
			result, err := ConvertZip(archive, ConvertOptions{PreserveTransactions: true})
			if err != nil {
				t.Fatal(err)
			}
			var request *dsl.Action
			for index := range result.Script.Actions {
				if result.Script.Actions[index].Type == "POST" {
					request = &result.Script.Actions[index]
				}
			}
			if request == nil || request.Body != body || request.Headers["Content-Type"] != contentType {
				t.Fatal("import changed the authored HTTP body or Content-Type")
			}
			if issues := dsl.ValidateScriptIssues(result.Script); len(issues) != 0 {
				t.Fatalf("invalid draft: %v", issues)
			}
			// Exercise the persisted-draft path as well as the first conversion.
			encoded, err := json.Marshal(result.Script)
			if err != nil {
				t.Fatal(err)
			}
			var persisted dsl.Script
			if err := json.Unmarshal(encoded, &persisted); err != nil {
				t.Fatal(err)
			}
			for _, script := range []*dsl.Script{result.Script, &persisted} {
				findings, err := importreview.FindingsFromScript(script)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, finding := range findings {
					if finding.Code == "credential_json_malformed" {
						t.Fatal("mismatch still has the generic JSON malformed diagnosis")
					}
					if finding.Code != "request_body_xml_json_content_type_mismatch" {
						continue
					}
					found = true
					if finding.SourceRef != "Action.c:7" || finding.Gate != importreview.GateExecution || finding.Detail != importreview.XMLBodyJSONContentTypeDetail {
						t.Fatalf("mismatch finding lost source, gate, or guidance: %#v", finding)
					}
					if strings.Contains(finding.Detail, "soap-credential-canary") {
						t.Fatal("mismatch finding exposed body credentials")
					}
				}
				if found != (contentType == "application/json") {
					t.Fatalf("mismatch present = %v for %s", found, contentType)
				}
			}
		})
	}
}
