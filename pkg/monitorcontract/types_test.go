package monitorcontract

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestHistoricalSelectionRetainsProviderMetadata(t *testing.T) {
	original := AppDynamicsSelection{ProjectID: "p", QueryOffset: 2, Applications: []string{"1"}, Metrics: []AppDynamicsMetricSelection{{ApplicationID: "1", Path: "Business Transactions|Checkout", Name: "response", Unit: "ms", Statistic: "average", EntityIDs: map[string]string{"tier": "2"}}}, Analytics: []AppDynamicsAnalyticsQuery{{Name: "count", Query: "SELECT count(*) FROM transactions", ApplicationID: "1", Kind: "metric", BucketSeconds: 60, RunIDField: "run_id"}}, ScopeIDs: map[string][]string{"node": {"3"}}, Events: true, Violations: true, Snapshots: true, SettleSeconds: 120, RunHeader: true, HeaderOrigins: []string{"https://orders.example"}, CollectorHeader: "header", CollectorValue: "value"}
	body, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var recovered AppDynamicsSelection
	if err = json.Unmarshal(body, &recovered); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, recovered) {
		t.Fatalf("historical metadata changed: %#v", recovered)
	}
}
