package appdynamics

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAppDynamicsSnapshotRunAssociationAndSafeLink(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	guid := "01234567-89ab-cdef-0123-456789abcdef"
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if q.Get("data-collector-name") != "X-Apex-Test-Run" || q.Get("data-collector-value") != "run123" || q.Get("need-props") != "false" || q.Get("need-exit-calls") != "false" {
			t.Fatal(q)
		}
		return jsonResponse(200, `[{"requestGUID":"`+guid+`","serverStartTime":`+jsonNumber(from.UnixMilli())+`,"timeTakenInMilliSecs":50,"errorOccured":false,"userExperience":"NORMAL","applicationComponentId":9,"httpHeaders":{"Authorization":"private-password"},"sql":"SELECT confidential"}]`), nil
	})
	s := Selection{ProjectID: "p", Applications: []string{"1"}, Snapshots: true, RunHeader: true, HeaderOrigins: []string{"https://service.example"}}
	result, err := c.Collect(context.Background(), s, from, from.Add(time.Minute), "run123")
	if err != nil || len(result.Evidence) != 1 {
		t.Fatal(result, err)
	}
	d := result.Evidence[0]
	if d.Properties["correlation"] != "run_header" || d.Properties["requestguid"] != guid || d.Properties["run_id"] != "run123" || d.Properties["sql"] != "" || !strings.HasSuffix(d.DeepLink, "#location=APP_SNAPSHOT_VIEWER&application=1&requestGUID="+guid) {
		t.Fatal(d)
	}
}

func TestAppDynamicsViolationRetainsResolutionAndOriginalTime(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	start := from.Add(-time.Minute)
	end := from.Add(30 * time.Second)
	for _, resolved := range []bool{false, true} {
		c := fixtureClient(t, AuthBasic, func(*http.Request) (*http.Response, error) {
			state, ended := "OPEN", int64(0)
			if resolved {
				state, ended = "RESOLVED", end.UnixMilli()
			}
			return jsonResponse(200, `[{"id":42,"startTimeInMillis":`+jsonNumber(start.UnixMilli())+`,"endTimeInMillis":`+jsonNumber(ended)+`,"incidentStatus":"`+state+`","affectedEntityDefinition":{"entityType":"APPLICATION_COMPONENT_NODE","entityId":7},"description":"private captured SQL"}]`), nil
		})
		result, err := c.Collect(context.Background(), Selection{ProjectID: "p", Applications: []string{"1"}, Violations: true}, from, from.Add(time.Minute), "r")
		if err != nil || len(result.Evidence) != 1 || result.Evidence[0].Properties["incident_start_utc"] != start.Format(time.RFC3339Nano) {
			t.Fatal(result, err)
		}
		d := result.Evidence[0]
		if resolved && (d.Status != "RESOLVED" || !d.TimestampUTC.Equal(end) || d.Properties["incident_end_utc"] != end.Format(time.RFC3339Nano)) || !resolved && !d.TimestampUTC.Equal(from) {
			t.Fatal(d)
		}
	}
}
