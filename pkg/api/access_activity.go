package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// AccessUsageRun deliberately excludes scripts, credentials and execution payloads.
type AccessUsageRun struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ProjectID string    `json:"project_id"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status"`
}

func validateAccessActivityDates(w http.ResponseWriter, r *http.Request, filter storage.AuditEventFilter) bool {
	for _, name := range []string{"created_after", "created_before"} {
		if raw := strings.TrimSpace(r.URL.Query().Get(name)); raw != "" && parseOptionalQueryTime(raw) == nil {
			writeError(w, http.StatusBadRequest, name+" must be an RFC3339 timestamp")
			return false
		}
	}
	if filter.CreatedAfter != nil && filter.CreatedBefore != nil && filter.CreatedAfter.After(*filter.CreatedBefore) {
		writeError(w, http.StatusBadRequest, "created_after must not be after created_before")
		return false
	}
	return true
}

// Apply all predicates to the same authorized snapshot. In particular, filtering
// by actor must never reintroduce tests outside a delegated admin's projects.
func (s *Server) accessUsageTests(filter storage.AuditEventFilter, scope map[string]struct{}, all bool) ([]*ManagedTest, error) {
	var domainProjects map[string]bool
	if filter.DomainID != "" {
		projects, err := s.store.ListProjects()
		if err != nil {
			return nil, err
		}
		domainProjects = map[string]bool{}
		for _, project := range projects {
			if normalizeDomainID(project.DomainID) == normalizeDomainID(filter.DomainID) {
				domainProjects[normalizeProjectID(project.ID)] = true
			}
		}
	}
	tests := []*ManagedTest{}
	for _, test := range s.runner.listOperationalTestSnapshots() {
		if test == nil {
			continue
		}
		project := normalizeProjectID(test.ProjectID)
		if !all {
			if _, ok := scope[project]; !ok {
				continue
			}
		}
		if filter.ProjectID != "" && project != normalizeProjectID(filter.ProjectID) {
			continue
		}
		if domainProjects != nil && !domainProjects[project] {
			continue
		}
		if filter.ActorSubject != "" && usageActorKey(test) != filter.ActorSubject {
			continue
		}
		if filter.CreatedAfter != nil && test.CreatedAt.Before(*filter.CreatedAfter) {
			continue
		}
		if filter.CreatedBefore != nil && test.CreatedAt.After(*filter.CreatedBefore) {
			continue
		}
		tests = append(tests, test)
	}
	sort.Slice(tests, func(i, j int) bool {
		if tests[i].CreatedAt.Equal(tests[j].CreatedAt) {
			return tests[i].ID < tests[j].ID
		}
		return tests[i].CreatedAt.After(tests[j].CreatedAt)
	})
	return tests, nil
}

// Build the entire export in a private temporary file before sending any bytes.
// A storage failure or cancellation cannot look like a successful partial CSV.
func (s *Server) exportAllAccessEvents(w http.ResponseWriter, r *http.Request, filter storage.AuditEventFilter) {
	file, err := os.CreateTemp("", "apexload-access-export-*.csv")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to prepare export")
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	now := time.Now().UTC()
	if filter.CreatedBefore == nil || filter.CreatedBefore.After(now) {
		filter.CreatedBefore = &now
	}
	filter.Limit, filter.Offset = 1000, 0
	writer := csv.NewWriter(file)
	err = writer.Write([]string{"id", "created_at", "action", "status", "actor_type", "actor_subject", "actor_display_name", "domain_id", "project_id", "resource_type", "resource_id"})
	for err == nil {
		if r.Context().Err() != nil {
			return
		}
		var events []*storage.AuditEvent
		events, err = s.store.ListAuditEvents(filter)
		if err != nil {
			break
		}
		for _, event := range events {
			err = writer.Write(spreadsheetSafeCSVRow([]string{event.ID, event.CreatedAt.UTC().Format(time.RFC3339), event.Action, event.Status, event.ActorType, event.ActorSubject, event.ActorDisplayName, event.DomainID, event.ProjectID, event.ResourceType, event.ResourceID}))
			if err != nil {
				break
			}
		}
		if len(events) < filter.Limit {
			break
		}
		filter.Offset += len(events)
	}
	writer.Flush()
	if err == nil {
		err = writer.Error()
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to complete export")
		return
	}
	if _, err = file.Seek(0, 0); err != nil {
		writeError(w, http.StatusInternalServerError, "unable to read export")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"apexload-user-activity-%s.csv\"", now.Format("2006-01-02")))
	http.ServeContent(w, r, "activity.csv", now, file)
}
