package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestArchivePopulationFilter(t *testing.T) {
	for _, population := range []string{"action", "transaction", "unknown", ""} {
		t.Run(population, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/?population="+population+"&action=shared", nil)
			filter, ok := parseTransactionEventFilter(httptest.NewRecorder(), request)
			if !ok {
				t.Fatal("valid population rejected")
			}
			for _, eventPopulation := range []string{"action", "transaction", "unknown"} {
				event := TransactionEvent{ActionName: "shared", Population: eventPopulation}
				if got, want := transactionMatchesArchiveFilter(&event, filter), population == "" || population == eventPopulation; got != want {
					t.Fatalf("%s matches %s: %v, want %v", eventPopulation, population, got, want)
				}
			}
			legacy := TransactionEvent{ActionName: "shared", BusinessTransaction: true}
			if got, want := transactionMatchesArchiveFilter(&legacy, filter), population == "" || population == "transaction"; got != want {
				t.Fatalf("legacy transaction: %v, want %v", got, want)
			}
		})
	}
	response := httptest.NewRecorder()
	if _, ok := parseTransactionEventFilter(response, httptest.NewRequest("GET", "/?population=bogus", nil)); ok || response.Code != 400 {
		t.Fatal("invalid population accepted")
	}
}

func TestCalendarPreservesOperatorStoppedOutcome(t *testing.T) {
	for _, tc := range []struct{ name, status, subject, termination, want string }{
		{"finished", "COMPLETE", "", "workload_completed", ""},
		{"operator", "COMPLETE", "operator", "", "operator_stopped"},
		{"archive", "COMPLETE", "", "operator_stopped", "operator_stopped"},
		{"failed", "FAILED", "operator", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := &ManagedTest{ID: "run", Status: tc.status, StoppedBySubject: tc.subject}
			event := runCalendarEventFromTest(run, nil, SystemInventoryResponse{}, &RunArchiveManifest{TerminationReason: tc.termination}, time.Now())
			if event.Status != tc.status || event.ReasonCode != tc.want {
				t.Fatalf("unexpected outcome: %+v", event)
			}
		})
	}
}
