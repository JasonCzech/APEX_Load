package api

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type runAnnotationRequest struct {
	ExpectedRevision int64     `json:"expected_revision"`
	Timestamp        time.Time `json:"timestamp"`
	Label            string    `json:"label"`
	Text             string    `json:"text,omitempty"`
}

func (s *Server) runAnnotationContext(w http.ResponseWriter, r *http.Request) (*ManagedTest, storage.RunAnnotationStore, bool) {
	run, ok := s.archiveDomainService().GetTest(r.PathValue("id"))
	if !ok {
		writeError(w, 404, "run not found")
		return nil, nil, false
	}
	store, ok := s.store.(storage.RunAnnotationStore)
	if !ok {
		writeError(w, 503, "annotation storage unavailable")
		return nil, nil, false
	}
	return run, store, true
}
func (s *Server) handleGetRunAnnotations(w http.ResponseWriter, r *http.Request) {
	run, store, ok := s.runAnnotationContext(w, r)
	if !ok {
		return
	}
	set, err := store.GetRunAnnotations(run.ProjectID, run.ID)
	if err != nil {
		writeError(w, 500, "failed to load annotations")
		return
	}
	writeJSON(w, 200, set)
}
func (s *Server) handleCreateRunAnnotation(w http.ResponseWriter, r *http.Request) {
	run, store, ok := s.runAnnotationContext(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req runAnnotationRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	req.Text = strings.TrimSpace(req.Text)
	invalid := func(s string) bool {
		return strings.ContainsFunc(s, func(c rune) bool { return unicode.IsControl(c) && c != '\n' && c != '\t' })
	}
	if req.ExpectedRevision < 0 || req.Label == "" || len(req.Label) > 256 || len(req.Text) > 2048 || invalid(req.Label) || invalid(req.Text) || run.StartedAt == nil || req.Timestamp.Before(*run.StartedAt) || req.Timestamp.After(time.Now().Add(time.Second)) || req.Timestamp.Sub(*run.StartedAt) > 24*time.Hour || run.EndedAt != nil && req.Timestamp.After(*run.EndedAt) {
		writeError(w, 400, "choose a bounded annotation and a timestamp within the executed run")
		return
	}
	set, err := store.GetRunAnnotations(run.ProjectID, run.ID)
	if err != nil {
		writeError(w, 500, "failed to load annotations")
		return
	}
	if set.Revision != req.ExpectedRevision {
		writeError(w, 409, "annotations changed; reload before saving")
		return
	}
	if len(set.Annotations) >= 200 {
		writeError(w, 422, "annotation audit history limit reached")
		return
	}
	_, subject, display := s.resolveAuditActor(r)
	set.Annotations = append(set.Annotations, storage.RunAnnotation{ID: uuid.NewString(), Timestamp: req.Timestamp.UTC(), Label: req.Label, Text: req.Text, CreatedAt: time.Now().UTC(), CreatedBy: firstNonEmpty(display, subject)})
	set.Revision++
	saved, err := store.SaveRunAnnotations(run.ProjectID, run.ID, set, req.ExpectedRevision)
	if err != nil {
		if errors.Is(err, storage.ErrRunAnnotationLimit) {
			writeError(w, 422, err.Error())
			return
		}
		writeError(w, 500, "failed to save annotation")
		return
	}
	if !saved {
		writeError(w, 409, "annotations changed; reload before saving")
		return
	}
	s.recordAuditEvent(r, "run.annotation.create", "success", "test", run.ID, run.ProjectID, map[string]any{"annotation_id": set.Annotations[len(set.Annotations)-1].ID})
	writeJSON(w, 201, set)
}
func (s *Server) handleDeleteRunAnnotation(w http.ResponseWriter, r *http.Request) {
	run, store, ok := s.runAnnotationContext(w, r)
	if !ok {
		return
	}
	expected, err := strconv.ParseInt(r.URL.Query().Get("expected_revision"), 10, 64)
	if err != nil || expected < 1 {
		writeError(w, 400, "expected_revision is required")
		return
	}
	set, err := store.GetRunAnnotations(run.ProjectID, run.ID)
	if err != nil {
		writeError(w, 500, "failed to load annotations")
		return
	}
	if set.Revision != expected {
		writeError(w, 409, "annotations changed; reload before deleting")
		return
	}
	found := false
	for i := range set.Annotations {
		a := &set.Annotations[i]
		if a.ID == r.PathValue("annotation_id") && a.DeletedAt == nil {
			now := time.Now().UTC()
			a.DeletedAt = &now
			_, subject, display := s.resolveAuditActor(r)
			a.DeletedBy = firstNonEmpty(display, subject)
			found = true
			break
		}
	}
	if !found {
		writeError(w, 404, "annotation not found")
		return
	}
	set.Revision++
	saved, err := store.SaveRunAnnotations(run.ProjectID, run.ID, set, expected)
	if err != nil {
		writeError(w, 500, "failed to delete annotation")
		return
	}
	if !saved {
		writeError(w, 409, "annotations changed; reload before deleting")
		return
	}
	s.recordAuditEvent(r, "run.annotation.delete", "success", "test", run.ID, run.ProjectID, map[string]any{"annotation_id": r.PathValue("annotation_id")})
	writeJSON(w, 200, set)
}
