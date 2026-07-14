package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/storage"
)

const testNoteBodyMaxCharacters = 64000

type testNotesListResponse struct {
	TestID string              `json:"test_id"`
	Notes  []*storage.TestNote `json:"notes"`
}

type testNoteRequest struct {
	Body string `json:"body"`
}

type atomicTestNoteArchiveStore interface {
	CreateTestNoteAndArchiveRefresh(note *storage.TestNote) error
	UpdateTestNoteAndArchiveRefresh(note *storage.TestNote) error
	DeleteTestNoteAndArchiveRefresh(id string, testID string) error
}

func (s *Server) handleListTestNotes(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")
	test, ok := s.runner.GetTest(testID)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	testID = test.ID
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage backend unavailable")
		return
	}
	notes, err := s.store.ListTestNotes(testID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load test notes: "+err.Error())
		return
	}
	if notes == nil {
		notes = []*storage.TestNote{}
	}
	writeJSON(w, http.StatusOK, testNotesListResponse{TestID: testID, Notes: notes})
}

func (s *Server) handleCreateTestNote(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")
	test, ok := s.runner.GetTest(testID)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	testID = test.ID
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage backend unavailable")
		return
	}
	body, ok := decodeTestNoteBody(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	_, actorSubject, actorDisplay := s.resolveAuditActor(r)
	actor := firstNonEmpty(actorDisplay, actorSubject)
	note := &storage.TestNote{
		ID:        newTestNoteID(),
		TestID:    testID,
		Body:      body,
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: actor,
		UpdatedBy: actor,
	}
	var err error
	if atomicStore, ok := s.store.(atomicTestNoteArchiveStore); ok {
		err = atomicStore.CreateTestNoteAndArchiveRefresh(note)
	} else {
		err = s.store.CreateTestNote(note)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create test note: "+err.Error())
		return
	}
	projectID, _, _ := resolveTestProjectID(r, s.store)
	s.recordAuditEvent(r, "test.notes.create", "success", "test", testID, projectID, map[string]any{
		"note_id": note.ID,
	})
	s.scheduleTestNotesArchiveRefresh(testID)
	writeJSON(w, http.StatusCreated, note)
}

func (s *Server) handleUpdateTestNote(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")
	noteID := r.PathValue("note_id")
	test, ok := s.runner.GetTest(testID)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	testID = test.ID
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage backend unavailable")
		return
	}
	body, ok := decodeTestNoteBody(w, r)
	if !ok {
		return
	}
	note, err := s.store.GetTestNote(noteID)
	if err != nil || note == nil || note.TestID != testID {
		if err == nil || errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "test note not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load test note: "+err.Error())
		return
	}
	_, actorSubject, actorDisplay := s.resolveAuditActor(r)
	note.Body = body
	note.UpdatedAt = time.Now().UTC()
	note.UpdatedBy = firstNonEmpty(actorDisplay, actorSubject)
	if atomicStore, ok := s.store.(atomicTestNoteArchiveStore); ok {
		err = atomicStore.UpdateTestNoteAndArchiveRefresh(note)
	} else {
		err = s.store.UpdateTestNote(note)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "test note not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update test note: "+err.Error())
		return
	}
	projectID, _, _ := resolveTestProjectID(r, s.store)
	s.recordAuditEvent(r, "test.notes.update", "success", "test", testID, projectID, map[string]any{
		"note_id": note.ID,
	})
	s.scheduleTestNotesArchiveRefresh(testID)
	writeJSON(w, http.StatusOK, note)
}

func (s *Server) handleDeleteTestNote(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")
	noteID := r.PathValue("note_id")
	test, ok := s.runner.GetTest(testID)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	testID = test.ID
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage backend unavailable")
		return
	}
	note, err := s.store.GetTestNote(noteID)
	if err != nil || note == nil || note.TestID != testID {
		if err == nil || errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "test note not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load test note: "+err.Error())
		return
	}
	if atomicStore, ok := s.store.(atomicTestNoteArchiveStore); ok {
		err = atomicStore.DeleteTestNoteAndArchiveRefresh(noteID, testID)
	} else {
		err = s.store.DeleteTestNote(noteID)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "test note not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete test note: "+err.Error())
		return
	}
	projectID, _, _ := resolveTestProjectID(r, s.store)
	s.recordAuditEvent(r, "test.notes.delete", "success", "test", testID, projectID, map[string]any{
		"note_id": noteID,
	})
	s.scheduleTestNotesArchiveRefresh(testID)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": noteID})
}

func decodeTestNoteBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req testNoteRequest
	if !decodeJSONBody(w, r, &req) {
		return "", false
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		writeError(w, http.StatusBadRequest, "note body cannot be empty")
		return "", false
	}
	if utf8.RuneCountInString(body) > testNoteBodyMaxCharacters {
		writeError(w, http.StatusBadRequest, "note too long (max 64000 characters)")
		return "", false
	}
	return body, true
}
