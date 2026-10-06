package collectoragent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestElasticReadinessAdvertisesAssignedSealingKeyAndContract(t *testing.T) {
	f := newElasticSpoolCleanupFixture(t)
	expected := f.job.Destination + "#" + base64.StdEncoding.EncodeToString(f.decrypt.PublicKey().Bytes())
	if destinations := f.agent.approvedDestinations(); !slices.Contains(destinations, expected) {
		t.Fatalf("enrolled encryption key absent: %v", destinations)
	}
	requests := 0
	f.agent.client = &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		var ad RunJobReadiness
		if err := json.Unmarshal([]byte(req.Header.Get("X-APEX-Monitor-Readiness")), &ad); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(ad.Packs, "elastic") || !slices.Contains(ad.Contracts, ElasticBatchContract) || !slices.Contains(ad.Destinations, expected) {
			t.Fatalf("incomplete enrollment contract: %+v", ad)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("[]")), Header: make(http.Header), Request: req}, nil
	})}
	var jobs []SignedRunJob
	if err := f.agent.jobRequest(context.Background(), http.MethodGet, "jobs?page_size=32", nil, &jobs); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatal("readiness not advertised")
	}
}
