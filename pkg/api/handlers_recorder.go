package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/recorder"
)

func (s *Server) handleStartRecording(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string   `json:"name"`
		ExcludeStatic bool     `json:"exclude_static"`
		ExcludeHosts  []string `json:"exclude_hosts,omitempty"`
		IncludeHosts  []string `json:"include_hosts,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Name == "" {
		req.Name = "Recording Session"
	}

	id := uuid.New().String()

	notify := func(evt recorder.SessionEvent) {
		s.hub.BroadcastJSON(map[string]any{
			"type":       evt.Type,
			"session_id": evt.SessionID,
			"request":    evt.Request,
		})
	}

	session, err := s.recorder.StartSession(id, req.Name, recorder.SessionFilters{
		ExcludeStatic: req.ExcludeStatic,
		ExcludeHosts:  req.ExcludeHosts,
		IncludeHosts:  req.IncludeHosts,
	}, notify)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"session":    session,
		"proxy_addr": s.recorder.ProxyAddr(),
	})
}

func (s *Server) handleStopRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, err := s.recorder.StopSession(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleGetRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.recorder.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	sessions := s.recorder.ListSessions()
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": sessions,
		"count":    len(sessions),
		"active":   s.recorder.ActiveSession(),
	})
}

func (s *Server) handleDeleteRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.recorder.DeleteSession(id) {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

func (s *Server) handleConvertRecording(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := s.recorder.GetSession(id)
	if !ok {
		writeError(w, http.StatusNotFound, "recording session not found")
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	result := recorder.ConvertSession(session, req.Name)

	stored := s.runner.StoreScript(result.Script)

	writeJSON(w, http.StatusCreated, map[string]any{
		"script":       stored,
		"correlations": result.Correlations,
		"warnings":     result.Warnings,
	})
}

func (s *Server) handleRecorderStatus(w http.ResponseWriter, r *http.Request) {
	active := s.recorder.ActiveSession()
	var activeInfo any
	if active != nil {
		activeInfo = map[string]any{
			"id":            active.ID,
			"name":          active.Name,
			"status":        active.Status,
			"request_count": len(active.Requests),
			"started_at":    active.StartedAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"proxy_addr": s.recorder.ProxyAddr(),
		"active":     activeInfo,
	})
}
