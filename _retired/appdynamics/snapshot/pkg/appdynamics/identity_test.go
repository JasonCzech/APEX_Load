package appdynamics

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAppDynamicsWildcardMetricScopeUsesControllerIDs(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	var nodeCalls atomic.Int32
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/nodes") {
			nodeCalls.Add(1)
			return jsonResponse(200, `[{"id":7,"name":"Node A","tierId":9,"tierName":"Services","machineId":3},{"id":8,"name":"Node B","tierId":9,"tierName":"Services","machineId":4}]`), nil
		}
		return jsonResponse(200, `[{"metricId":2231,"metricPath":"Application Infrastructure Performance|Services|Individual Nodes|Node A|Hardware Resources|CPU|%Busy","metricName":"Hardware Resources|CPU|%Busy","frequency":"ONE_MIN","metricValues":[{"startTimeInMillis":`+jsonNumber(from.UnixMilli())+`,"value":10}]},{"metricId":2231,"metricPath":"Application Infrastructure Performance|Services|Individual Nodes|Node B|Hardware Resources|CPU|%Busy","metricName":"Hardware Resources|CPU|%Busy","frequency":"ONE_MIN","metricValues":[{"startTimeInMillis":`+jsonNumber(from.UnixMilli())+`,"value":20}]}]`), nil
	})
	s := Selection{ProjectID: "p", Applications: []string{"1"}, ScopeIDs: map[string][]string{"node": {"7"}}, Metrics: []MetricSelection{{ApplicationID: "1", Path: "Application Infrastructure Performance|*|Individual Nodes|*|Hardware Resources|CPU|%Busy", Name: "cpu", EntityIDs: map[string]string{"node": "8"}}}}
	result, err := c.Collect(context.Background(), s, from, from.Add(time.Minute), "r")
	if err != nil || len(result.Points) != 1 || nodeCalls.Load() != 1 {
		t.Fatal(result, err, nodeCalls.Load())
	}
	p := result.Points[0]
	if p.Provider.EntityIDs["node"] != "7" || p.Provider.EntityIDs["tier"] != "9" || p.Provider.EntityIDs["machine"] != "3" || *p.Value != 10 {
		t.Fatal(p)
	}
	s.ScopeIDs = nil
	result, err = c.Collect(context.Background(), s, from, from.Add(time.Minute), "r")
	if err != nil || len(result.Points) != 2 || result.Points[0].Dimensions["catalog_path_id"] == result.Points[1].Dimensions["catalog_path_id"] {
		t.Fatal(result, err)
	}
}
func TestAppDynamicsBusinessTransactionMetricIDs(t *testing.T) {
	l := entityLookup{}
	selected, err := l.selection(MetricSelection{ApplicationID: "1", Path: "Business Transaction Performance|Business Transactions|Services|*|Calls per Minute", EntityIDs: map[string]string{"business_transaction": "999"}}, metricResponse{Name: "BTM|BTs|BT:125|Component:9|Calls per Minute"})
	if err != nil || selected.EntityIDs["business_transaction"] != "125" || selected.EntityIDs["tier"] != "9" {
		t.Fatal(selected, err)
	}
	if matchesScope(selected.EntityIDs, map[string][]string{"node": {"7"}}) {
		t.Fatal("unverified scope matched")
	}
	_, err = l.selection(MetricSelection{ApplicationID: "1", Path: "exact", EntityIDs: map[string]string{"business_transaction": "999"}}, metricResponse{Name: "BTM|BTs|BT:125|Component:9|Calls per Minute"})
	if err == nil {
		t.Fatal("mismatching explicit entity silently relabeled")
	}
}

func TestAppDynamicsBackendMetricScopeAndTopology(t *testing.T) {
	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Minute)
	c := fixtureClient(t, AuthBasic, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/backends") {
			return jsonResponse(200, `[{"id":10,"name":"Inventory DB","tierId":9},{"id":11,"name":"Other DB","tierId":9}]`), nil
		}
		return jsonResponse(200, `[{"metricId":2231,"metricPath":"Backend Performance|Inventory DB|Calls per Minute","frequency":"ONE_MIN","metricValues":[{"startTimeInMillis":`+jsonNumber(from.UnixMilli())+`,"value":0}]},{"metricId":2232,"metricPath":"Backend Performance|Other DB|Calls per Minute","frequency":"ONE_MIN","metricValues":[{"startTimeInMillis":`+jsonNumber(from.UnixMilli())+`,"value":10}]}]`), nil
	})
	s := Selection{ProjectID: "p", Applications: []string{"1"}, ScopeIDs: map[string][]string{"backend": {"10"}}, Metrics: []MetricSelection{{ApplicationID: "1", Name: "calls", Path: "Backend Performance|*|Calls per Minute"}}}
	result, err := c.Collect(context.Background(), s, from, from.Add(time.Minute), "r")
	if err != nil || len(result.Points) != 1 || result.Points[0].Provider.EntityIDs["backend"] != "10" || result.Points[0].Provider.EntityIDs["tier"] != "9" {
		t.Fatal(result, err)
	}
	backend := false
	for _, entity := range result.Topology {
		if entity.Type == "backend" {
			backend = entity.ID == "10" && entity.ParentID == "1" && entity.EntityIDs["backend"] == "10"
		}
	}
	if !backend {
		t.Fatal(result.Topology)
	}
	s.ScopeIDs = map[string][]string{"machine": {"3"}}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}
