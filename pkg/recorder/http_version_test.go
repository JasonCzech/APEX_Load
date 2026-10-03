package recorder

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

func TestRecorderHTTPVersionObservationAndRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, client, upstream, version, reason string }{
		{"H1", "HTTP/1.1", "HTTP/1.1", "1.1", ""},
		{"H2", "HTTP/2.0", "HTTP/2.0", "2", ""},
		{"different proxy legs", "HTTP/1.1", "HTTP/2.0", "", "proxy_http_version_legs_differ"},
		{"legacy unknown", "", "", "", "http_version_unavailable"},
		{"unknown upstream", "HTTP/1.1", "unknown", "", "http_version_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := &Session{ID: "version-recording", Name: "Version recording", BaseURL: "https://fixture.invalid", StartedAt: time.Now(), Requests: []RecordedRequest{{Seq: 1, Timestamp: time.Now(), Method: "GET", URL: "https://fixture.invalid/read", Path: "/read", StatusCode: 200, RequestHTTPVersion: tc.client, ResponseHTTPVersion: tc.upstream}}}
			direct := ConvertSession(session, session.Name)
			data, err := ExportHAR(session, false)
			if err != nil {
				t.Fatal(err)
			}
			var archive harArchive
			if err = json.Unmarshal(data, &archive); err != nil {
				t.Fatal(err)
			}
			entry := archive.Log.Entries[0]
			if entry.Request.HTTPVersion != exportedObservedHTTPVersion(tc.client) || entry.Response.HTTPVersion != exportedObservedHTTPVersion(tc.upstream) {
				t.Fatal("export invented or collapsed a leg")
			}
			if entry.HTTPVersionProvenance["request_leg"] != "client_to_proxy" || entry.HTTPVersionProvenance["response_leg"] != "proxy_to_origin" {
				t.Fatal("export lost leg provenance")
			}
			imported, err := har.Convert(data, har.ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, script := range []struct {
				version  string
				metadata map[string]any
			}{{direct.Script.Actions[0].HTTPVersion.Value(), direct.Script.Actions[0].Metadata}, {imported.Script.Actions[0].HTTPVersion.Value(), imported.Script.Actions[0].Metadata}} {
				if script.version != tc.version {
					t.Fatalf("leaf version=%q", script.version)
				}
				marker, _ := script.metadata[requestimport.ImportHTTPVersionFidelityMetadataKey].(map[string]any)
				if tc.reason != "" && marker["reason_code"] != tc.reason {
					t.Fatalf("marker=%v", marker)
				}
				if tc.reason == "" && script.metadata["execution_blocker"] != nil {
					t.Fatal("known equal legs were gated")
				}
			}
		})
	}
}

func TestProxyRecordsDifferentActualHTTPVersionLegs(t *testing.T) {
	allowPrivateProxyTargets(t)
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.ProtoMajor != 2 {
			t.Errorf("upstream wire protocol=%s", request.Proto)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	upstream.EnableHTTP2 = true
	upstream.StartTLS()
	defer upstream.Close()
	recorder := New(":0")
	recorder.proxyClient = upstream.Client()
	recorder.StartSession("version-proxy", "Version proxy", SessionFilters{}, nil)
	request := httptest.NewRequest("GET", upstream.URL+"/read", nil)
	response := httptest.NewRecorder()
	recorder.handleProxy(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("proxy status=%d", response.Code)
	}
	session, _ := recorder.GetSession("version-proxy")
	if len(session.Requests) != 1 || session.Requests[0].RequestHTTPVersion != "HTTP/1.1" || session.Requests[0].ResponseHTTPVersion != "HTTP/2.0" {
		t.Fatalf("observed exchanges=%+v", session.Requests)
	}
	draft := ConvertSession(session, "Version proxy").Script.Actions[0]
	marker := draft.Metadata[requestimport.ImportHTTPVersionFidelityMetadataKey].(map[string]any)
	if draft.HTTPVersion.IsPresent() || marker["reason_code"] != "proxy_http_version_legs_differ" {
		t.Fatal("proxy invented one exchange policy from differing observed legs")
	}
}

func TestRecordRequestRetainsSeparateObservedProtocols(t *testing.T) {
	recorder := New(":0")
	session := &Session{ID: "versions", Status: "recording", StartedAt: time.Now(), Filters: SessionFilters{BodyCaptureLimitBytes: 1024}}
	request, _ := http.NewRequest("GET", "https://fixture.invalid/read", nil)
	response := &http.Response{StatusCode: 200, Header: make(http.Header), Proto: "HTTP/2.0", ProtoMajor: 2}
	if seq := recorder.recordRequest(session, request, nil, response, nil, time.Millisecond); seq != 1 {
		t.Fatalf("recorded seq=%d", seq)
	}
	if session.Requests[0].RequestHTTPVersion != "HTTP/1.1" || session.Requests[0].ResponseHTTPVersion != "HTTP/2.0" {
		t.Fatal("request/response legs collapsed")
	}
}
