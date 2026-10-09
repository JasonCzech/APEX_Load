package sample

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestNormalizeRecordedExchangesHandlesHandshakesAndSplitCookies(t *testing.T) {
	exchanges := []exchange{
		{SourceEntryID: 1, Method: "GET", URL: "https://intranet.test/app", Headers: map[string]string{"Authorization": "NTLM t1"},
			Response: &response{Status: 401, HeaderList: dsl.HeaderList{{Name: "WWW-Authenticate", Value: "NTLM"}}}},
		{SourceEntryID: 2, Method: "GET", URL: "https://intranet.test/app",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "NTLM t3"}, {Name: "Accept", Value: "*/*"}},
			Headers:    map[string]string{"Authorization": "NTLM t3", "Accept": "*/*"},
			Response:   &response{Status: 200, HeaderList: dsl.HeaderList{{Name: "Set-Cookie", Value: "sid=1; Path=/"}, {Name: "Set-Cookie", Value: "pref=dark; Path=/"}}}},
		{SourceEntryID: 3, Method: "GET", URL: "https://intranet.test/next",
			HeaderList: dsl.HeaderList{{Name: "cookie", Value: "sid=1"}, {Name: "cookie", Value: "pref=dark; js=on"}},
			Headers:    map[string]string{"cookie": "sid=1; pref=dark; js=on"}},
	}
	got, warnings := normalizeRecordedExchanges(exchanges, "har")
	if len(got) != 2 || got[0].SourceEntryID != 2 {
		t.Fatalf("the handshake leg must be dropped: %+v", got)
	}
	if exchangeIntegratedAuthScheme(got[0]) != "" || got[0].Headers["Accept"] != "*/*" {
		t.Fatalf("Authorization must go and other headers stay: %+v", got[0].Headers)
	}
	if len(got[0].ReviewFindings) != 1 || got[0].ReviewFindings[0].Code != "integrated_auth_requires_configuration" {
		t.Fatalf("findings = %+v", got[0].ReviewFindings)
	}
	if len(got[1].HeaderList) != 1 || got[1].HeaderList[0].Value != "js=on" || got[1].Headers["Cookie"] != "js=on" {
		t.Fatalf("split cookie headers = %+v / %+v, want only the script-set cookie", got[1].HeaderList, got[1].Headers)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "1 NTLM/Negotiate handshake") || !strings.Contains(joined, "2 recorded Cookie value") {
		t.Fatalf("warnings = %v", warnings)
	}
	if exchanges[1].Headers["Authorization"] != "NTLM t3" {
		t.Fatal("the caller's exchanges must not be modified")
	}
}
