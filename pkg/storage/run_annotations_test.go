package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRunAnnotationsCASIsolationAuditAndRetention(t *testing.T) {
	path := t.TempDir() + "/annotations.db"
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.SaveTest(&TestRecord{ID: "run", ProjectID: DefaultProjectID, Name: "run", Status: "COMPLETE", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	set := RunAnnotationSet{Revision: 1, Annotations: []RunAnnotation{{ID: "note", Timestamp: now, Label: "Deployment", CreatedAt: now, CreatedBy: "editor"}}}
	if ok, err := s.SaveRunAnnotations(DefaultProjectID, "run", set, 0); err != nil || !ok {
		t.Fatalf("save %v %v", ok, err)
	}
	if ok, err := s.SaveRunAnnotations(DefaultProjectID, "run", set, 0); err != nil || ok {
		t.Fatalf("stale insert %v %v", ok, err)
	}
	if got, err := s.GetRunAnnotations("other", "run"); err != nil || len(got.Annotations) != 0 {
		t.Fatalf("project leak %+v %v", got, err)
	}
	set.Revision = 2
	set.Annotations[0].DeletedAt = &now
	set.Annotations[0].DeletedBy = "editor"
	if ok, err := s.SaveRunAnnotations(DefaultProjectID, "run", set, 1); err != nil || !ok {
		t.Fatalf("delete %v %v", ok, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetRunAnnotations(DefaultProjectID, "run")
	if err != nil || got.Revision != 2 || got.Annotations[0].DeletedAt == nil || got.Annotations[0].Label != "Deployment" {
		t.Fatalf("audit %+v %v", got, err)
	}
	if err := s.DeleteTest("run"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetRunAnnotations(DefaultProjectID, "run"); err != nil || len(got.Annotations) != 0 {
		t.Fatalf("retention %+v %v", got, err)
	}
	if ok, err := s.SaveRunAnnotations(DefaultProjectID, "run", RunAnnotationSet{Revision: 1, Annotations: set.Annotations}, 0); err != nil || ok {
		t.Fatalf("orphan recreated %v %v", ok, err)
	}
}
func TestRunAnnotationBounds(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir() + "/bounds.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, set := range []RunAnnotationSet{{Revision: 1, Annotations: []RunAnnotation{{ID: "x"}}}, {Revision: 1, Annotations: make([]RunAnnotation, 201)}, {Revision: 3}} {
		if _, err := s.SaveRunAnnotations(DefaultProjectID, "run", set, 0); err == nil {
			t.Fatalf("accepted invalid %+v", set)
		}
	}
}

func TestRunAnnotationEncodedQuotaIsExplicit(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir() + "/quota.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	set := RunAnnotationSet{Revision: 1}
	for i := 0; i < 200; i++ {
		set.Annotations = append(set.Annotations, RunAnnotation{ID: "x", Label: "Note", Timestamp: now, CreatedAt: now, Text: strings.Repeat("\n", 2048)})
	}
	if _, err := s.SaveRunAnnotations(DefaultProjectID, "run", set, 0); !errors.Is(err, ErrRunAnnotationLimit) {
		t.Fatalf("quota error %v", err)
	}
}

func TestRunAnnotationNearQuotaLegacyHistoryRemainsDeletable(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir() + "/deletion-quota.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	if err := s.SaveTest(&TestRecord{ID: "run", ProjectID: DefaultProjectID, Name: "run", Status: "COMPLETE", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	set := RunAnnotationSet{Revision: 1, Annotations: []RunAnnotation{}}
	var raw []byte
	for i := 0; i < 200; i++ {
		set.Annotations = append(set.Annotations, RunAnnotation{ID: fmt.Sprintf("%036d", i), Label: "Note", Timestamp: now, CreatedAt: now, CreatedBy: "editor", Text: strings.Repeat("\n", 2048)})
		raw, err = json.Marshal(set)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > maxRunAnnotationContentBytes {
			last := &set.Annotations[len(set.Annotations)-1]
			// Newlines encode to two bytes, leaving at most one byte of slack.
			excess := len(raw) - maxRunAnnotationContentBytes
			last.Text = last.Text[:len(last.Text)-(excess+1)/2]
			raw, err = json.Marshal(set)
			if err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if gap := maxRunAnnotationContentBytes - len(raw); gap < 0 || gap > 1 {
		t.Fatalf("not a near-quota fixture: %d", gap)
	}
	// Seed the payload exactly as the old 512KiB admission rule would accept it.
	if _, err := s.db.Exec(`INSERT INTO run_annotations(test_id, project_id, revision, payload) VALUES (?, ?, ?, ?)`, "run", DefaultProjectID, set.Revision, string(raw)); err != nil {
		t.Fatal(err)
	}
	originalLastText := set.Annotations[len(set.Annotations)-1].Text
	for i := range set.Annotations {
		expected := set.Revision
		set.Revision++
		set.Annotations[i].DeletedAt = &now
		set.Annotations[i].DeletedBy = strings.Repeat("<", 256) // Worst-case JSON escaping.
		if saved, err := s.SaveRunAnnotations(DefaultProjectID, "run", set, expected); err != nil || !saved {
			t.Fatalf("delete %d: %v %v", i, saved, err)
		}
	}
	got, err := s.GetRunAnnotations(DefaultProjectID, "run")
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != set.Revision || len(got.Annotations) != len(set.Annotations) || got.Annotations[len(got.Annotations)-1].Text != originalLastText {
		t.Fatalf("lost history: revision=%d count=%d", got.Revision, len(got.Annotations))
	}
	for i, a := range got.Annotations {
		if a.DeletedAt == nil || a.DeletedBy != strings.Repeat("<", 256) {
			t.Fatalf("missing tombstone %d", i)
		}
	}
	retained, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained) <= maxRunAnnotationContentBytes || len(retained) > maxRunAnnotationHistoryBytes {
		t.Fatalf("invalid retained history budget: %d", len(retained))
	}
	// Deletion cannot free immutable-content quota for additional notes.
	got.Revision++
	got.Annotations = append(got.Annotations, RunAnnotation{ID: "extra", Label: "Extra", Timestamp: now, CreatedAt: now, Text: strings.Repeat("x", 2048)})
	if _, err := s.SaveRunAnnotations(DefaultProjectID, "run", got, set.Revision); !errors.Is(err, ErrRunAnnotationLimit) {
		t.Fatalf("deleted history bypassed content quota: %v", err)
	}
}
