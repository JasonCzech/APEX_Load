package sample

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestHARDocumentEvidenceIsMemoizedAndCallerOwned(t *testing.T) {
	doc, err := ParseHARDocument(documentFixture())
	if err != nil {
		t.Fatal(err)
	}
	firstTime, err := doc.ClientTimestamps()
	if err != nil || len(firstTime.Server) == 0 || firstTime.From.IsZero() {
		t.Fatalf("timestamp evidence = %+v, %v", firstTime, err)
	}
	firstUUIDs, judged, err := doc.ServerUUIDs()
	if err != nil || !judged || len(firstUUIDs) == 0 {
		t.Fatalf("UUID evidence = %v/%v/%v", firstUUIDs, judged, err)
	}
	wantTime := firstTime
	wantTime.Server = map[string]bool{}
	for key, value := range firstTime.Server {
		wantTime.Server[key] = value
	}
	wantUUIDs := map[string]bool{}
	for key, value := range firstUUIDs {
		wantUUIDs[key] = value
	}
	// Callers own what they get: changing it must not reach later callers.
	firstTime.Server["1791547299"] = true
	firstUUIDs["ffffffff-ffff-4fff-8fff-ffffffffffff"] = true
	for key := range wantUUIDs {
		delete(firstUUIDs, key)
	}

	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			again, err := doc.ClientTimestamps()
			if err != nil || !reflect.DeepEqual(again, wantTime) {
				t.Errorf("repeated timestamp evidence = %+v, %v; want %+v", again, err, wantTime)
			}
			uuids, againJudged, err := doc.ServerUUIDs()
			if err != nil || againJudged != judged || !reflect.DeepEqual(uuids, wantUUIDs) {
				t.Errorf("repeated UUID evidence = %v/%v/%v; want %v", uuids, againJudged, err, wantUUIDs)
			}
		}()
	}
	group.Wait()
}

func TestHARDocumentMemoizedEvidenceKeepsErrors(t *testing.T) {
	for _, content := range []string{
		"\uFEFF" + documentFixture(),
		strings.Replace(documentFixture(), `"startedDateTime":"2026-10-09T12:00:00Z"`, `"startedDateTime":"2026-10-09T12:00:00Z","StartedDateTime":"2026-10-10T12:00:00Z"`, 1),
		strings.ReplaceAll(documentFixture(), `"value":"b0b0b0b0-b1b1-4c2c-8d3d-e4e4e4e4e4e4"`, `"value":5`),
	} {
		doc, err := ParseHARDocument(content)
		if err != nil {
			t.Fatal(err)
		}
		firstTime, firstTimeErr := doc.ClientTimestamps()
		firstUUIDs, firstJudged, firstUUIDErr := doc.ServerUUIDs()
		for i := 0; i < 2; i++ {
			again, err := doc.ClientTimestamps()
			if !reflect.DeepEqual(again, firstTime) || !reflect.DeepEqual(err, firstTimeErr) {
				t.Fatalf("repeated timestamp evidence = %+v/%v, want %+v/%v", again, err, firstTime, firstTimeErr)
			}
			uuids, judged, err := doc.ServerUUIDs()
			if !reflect.DeepEqual(uuids, firstUUIDs) || judged != firstJudged || !reflect.DeepEqual(err, firstUUIDErr) {
				t.Fatalf("repeated UUID evidence = %v/%v/%v, want %v/%v/%v", uuids, judged, err, firstUUIDs, firstJudged, firstUUIDErr)
			}
		}
		if firstTimeErr == nil && firstUUIDErr == nil {
			t.Fatalf("fixture produced no evidence error: %.60q", content)
		}
	}
}
