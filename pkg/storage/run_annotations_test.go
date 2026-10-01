package storage

import (
	"errors"
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
