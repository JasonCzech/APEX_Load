package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBrowserIntelligencePromotionRequiresCurrentAcceptedEvidence(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	body := acceptanceBody(storage.AcceptancePassed, true)
	body["requirement_id"] = "BI-1"
	body["feature"] = "browser-findings"
	response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", body, "")
	if response.Code != http.StatusConflict {
		t.Fatalf("unqualified promotion: %d %s", response.Code, response.Body.String())
	}
	body["requirement_id"] = "BE-4"
	body["feature"] = "browser-metrics"
	body["commit_sha"] = strings.Repeat("a", 40)
	body["binary_sha256"] = strings.Repeat("b", 64)
	body["provider"] = "chromium"
	body["profile"] = "desktop"
	q := &storage.BrowserAcceptanceQualification{Version: 1, RuntimeSHA256: strings.Repeat("c", 64), CapabilitySHA256: strings.Repeat("d", 64), Metric: "lcp", MeasurementAlgorithm: "document-metrics.v1"}
	body["qualification"] = q
	response = authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", body, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("BE acceptance: %d %s", response.Code, response.Body.String())
	}
	var be storage.FeatureAcceptanceRecord
	if err := json.Unmarshal(response.Body.Bytes(), &be); err != nil {
		t.Fatal(err)
	}
	hash, err := storage.BrowserAcceptanceRecordSHA256(&be)
	if err != nil {
		t.Fatal(err)
	}
	body["requirement_id"] = "BI-1"
	body["feature"] = "browser-findings"
	q.AnalysisAlgorithm = "threshold.v1"
	q.Dependencies = []storage.BrowserAcceptanceDependency{{RecordID: be.ID, RecordSHA256: hash}}
	response = authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", body, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("qualified promotion: %d %s", response.Code, response.Body.String())
	}
	var promoted storage.FeatureAcceptanceRecord
	if err := json.Unmarshal(response.Body.Bytes(), &promoted); err != nil {
		t.Fatal(err)
	}
	statuses := acceptanceStatus(t, server, "requirement=BI-1").Statuses
	if len(statuses) != 1 || !statuses[0].Supported || statuses[0].Latest.Qualification.Dependencies[0].RecordSHA256 != hash {
		t.Fatal("promotion pins lost", statuses)
	}

	alternate := *q
	alternate.AnalysisAlgorithm = "threshold.v2"
	body["qualification"] = &alternate
	response = authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", body, "")
	if response.Code != http.StatusCreated {
		t.Fatal("independent estimator promotion", response.Code, response.Body.String())
	}
	statuses = acceptanceStatus(t, server, "requirement=BI-1").Statuses
	if len(statuses) != 2 {
		t.Fatal("algorithm cohorts collapsed", statuses)
	}
	counting := &browserAcceptanceCountingBackend{Store: store, FeatureAcceptanceStore: store, BrowserAcceptanceDependencyStore: store}
	server.store = counting
	statuses = acceptanceStatus(t, server, "requirement=BI-1&cohort_sha256="+promoted.CohortSHA256).Statuses
	if counting.checks != 1 {
		t.Fatalf("exact cohort query checked %d subjects", counting.checks)
	}
	server.store = store
	if len(statuses) != 1 || statuses[0].Latest.ID != promoted.ID {
		t.Fatal("exact cohort filter lost its pin", statuses)
	}
	body["qualification"] = q
	revoke := acceptanceBody(storage.AcceptanceRevoked, false)
	revoke["feature"] = "browser-metrics"
	delete(revoke, "platform")
	recordAcceptance(t, server, revoke)
	statuses = acceptanceStatus(t, server, "requirement=BI-1&cohort_sha256="+promoted.CohortSHA256).Statuses
	if len(statuses) != 1 || statuses[0].Supported || statuses[0].Reason != "browser_evidence_dependency_unavailable" || statuses[0].Latest.ID != promoted.ID || statuses[0].Latest.Result != storage.AcceptancePassed {
		t.Fatal("withdrawal did not preserve and disqualify history", statuses)
	}
	response = authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", body, "")
	if response.Code != http.StatusConflict {
		t.Fatalf("withdrawn promotion: %d %s", response.Code, response.Body.String())
	}
}

// An older backend can store generic acceptance records without advertising
// the atomic dependency contract. It must not receive a BI promotion write.
type browserAcceptanceBackendWithoutGate struct {
	storage.Store
	storage.FeatureAcceptanceStore
}

func TestBrowserIntelligencePromotionRefusesBackendWithoutAtomicGate(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	server.store = browserAcceptanceBackendWithoutGate{Store: server.store, FeatureAcceptanceStore: store}
	body := acceptanceBody(storage.AcceptancePassed, true)
	body["requirement_id"], body["feature"] = "BI-1", "browser-findings"
	response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/system/acceptance-records", body, "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("unchecked backend accepted promotion", response.Code, response.Body.String())
	}
	records, err := store.ListFeatureAcceptanceRecords("BI-1", "", 10, 0)
	if err != nil || len(records) != 0 {
		t.Fatal("unsupported promotion persisted", err, records)
	}
}

// Exact cohort queries do not validate unrelated promoted cohorts.
type browserAcceptanceCountingBackend struct {
	storage.Store
	storage.FeatureAcceptanceStore
	storage.BrowserAcceptanceDependencyStore
	checks int
}

func (s *browserAcceptanceCountingBackend) CheckBrowserAcceptanceDependencies(record *storage.FeatureAcceptanceRecord) error {
	s.checks++
	return s.BrowserAcceptanceDependencyStore.CheckBrowserAcceptanceDependencies(record)
}
