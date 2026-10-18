package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func confluenceReportURL(r *http.Request, testID string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("APEXLOAD_PUBLIC_URL")), "/")
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", errors.New("APEXLOAD_PUBLIC_URL is invalid")
		}
		return base + "/#/report/" + url.PathEscape(testID), nil
	}
	if r == nil {
		return "", errors.New("APEXLOAD_PUBLIC_URL is required for automatic publishing")
	}
	if !validPowerBILinkHost(strings.TrimSpace(r.Host)) {
		return "", errors.New("cannot derive a safe APEX Load report link")
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/#/report/%s", scheme, strings.TrimSpace(r.Host), url.PathEscape(testID)), nil
}

func (s *Server) publishRunConfluence(ctx context.Context, testID, connectionID, actor string, automatic bool, r *http.Request) (*storage.ConfluencePublication, error) {
	store, ok := s.store.(storage.ConfluenceStore)
	if !ok {
		return nil, errors.New("Confluence storage is unavailable")
	}
	test, err := s.store.GetTest(testID)
	if err != nil {
		return nil, errors.New("test not found")
	}
	testID = test.ID
	c, err := store.GetConfluenceConnection(connectionID)
	if err != nil || c == nil || c.ProjectID != test.ProjectID {
		return nil, errors.New("Confluence connection not found in this project")
	}
	if !c.Enabled {
		return nil, errors.New("Confluence connection is disabled")
	}
	if automatic && !c.AutoPublish {
		return nil, errors.New("automatic publishing is disabled for this connection")
	}
	previous, claimed, err := store.ClaimConfluencePublication(testID, test.ProjectID, connectionID, actor, automatic)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return previous, errors.New("Confluence publication is already in progress or complete")
	}
	fail := func(cause error) (*storage.ConfluencePublication, error) {
		if finishErr := store.FinishConfluencePublication(testID, connectionID, "failed", "", "", cause.Error()); finishErr != nil {
			return nil, errors.Join(cause, fmt.Errorf("save Confluence publication failure: %w", finishErr))
		}
		return nil, cause
	}
	link, err := confluenceReportURL(r, testID)
	if err != nil {
		return fail(err)
	}
	record, ok := s.archiveDomainService().GetRunArchiveContext(ctx, testID)
	if !ok {
		return fail(errors.New("archived run is unavailable"))
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return fail(fmt.Errorf("materialize run archive: %w", err))
	}
	report, err := s.archiveReadCaches().cachedRunArchiveReport(record)
	lease.Release()
	if err != nil {
		return fail(fmt.Errorf("build archived report: %w", err))
	}
	if report.Manifest == nil {
		return fail(errors.New("archived report manifest is missing"))
	}
	attachReportVerdictAndSLA(report)
	applyPowerBIVerdictOverride(s.store, report, testID)
	client, err := s.confluenceHTTPClient(c)
	if err != nil {
		return fail(err)
	}
	token, err := s.confluenceToken(ctx, c)
	if err != nil {
		return fail(err)
	}
	pageID, pageURL, err := publishConfluencePage(ctx, client, c, token, previous, report, link)
	if err != nil {
		return fail(err)
	}
	if err := store.FinishConfluencePublication(testID, connectionID, "complete", pageID, pageURL, ""); err != nil {
		return nil, err
	}
	return store.GetConfluencePublication(testID, connectionID)
}

func (s *Server) handlePublishRunToConfluence(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConnectionID string `json:"connection_id"`
	}
	if !decodeJSONBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.ConnectionID) == "" {
		writeError(w, 400, "connection_id is required")
		return
	}
	_, lease, ok := s.requireRunArchive(w, r)
	if !ok {
		return
	}
	lease.Release()
	_, actor, _ := s.resolveAuditActor(r)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	result, err := s.publishRunConfluence(ctx, r.PathValue("id"), body.ConnectionID, actor, false, r)
	if err != nil {
		status := http.StatusBadGateway
		if result != nil {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	s.recordAuditEvent(r, "confluence.publish", "success", "test", r.PathValue("id"), result.ProjectID, map[string]any{"connection_id": result.ConnectionID, "page_id": result.PageID})
	writeJSON(w, 200, map[string]any{"publication": result})
}

func (s *Server) handleGetConfluencePublicationStatus(w http.ResponseWriter, r *http.Request) {
	store, ok := s.confluenceStore(w)
	if !ok {
		return
	}
	test, err := s.store.GetTest(r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "test not found")
		return
	}
	if err != nil {
		writeError(w, 500, "failed to load test")
		return
	}
	connections, err := store.ListConfluenceConnections(test.ProjectID)
	if err != nil {
		writeError(w, 500, "failed to list Confluence connections")
		return
	}
	available := make([]confluenceConnectionResponse, 0)
	for _, c := range connections {
		if c.Enabled {
			available = append(available, confluenceSelectableView(c))
		}
	}
	publications, err := store.ListConfluencePublications(test.ID)
	if err != nil {
		writeError(w, 500, "failed to load Confluence publications")
		return
	}
	writeJSON(w, 200, map[string]any{"connections": available, "publications": publications})
}

func (s *Server) retryConfluencePublications(ctx context.Context) error {
	store, ok := s.store.(storage.ConfluenceStore)
	if !ok {
		return nil
	}
	items, err := store.ListPendingConfluencePublications(20)
	if err != nil {
		return err
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		connection, connectionErr := store.GetConfluenceConnection(item.ConnectionID)
		if connectionErr != nil || connection == nil || !connection.Enabled || !connection.AutoPublish {
			if err := store.AbandonConfluencePublication(item.TestID, item.ConnectionID, "automatic Confluence destination is unavailable"); err != nil {
				return fmt.Errorf("abandon Confluence publication for run %s: %w", item.TestID, err)
			}
			continue
		}
		// A deleted or disabled destination fails visibly and stops retrying after
		// the bounded attempt count; it never changes the recorded run outcome.
		attemptCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		_, err := s.publishRunConfluence(attemptCtx, item.TestID, item.ConnectionID, "automatic", true, nil)
		cancel()
		if err != nil {
			slog.Warn("Confluence automatic publish failed", "test_id", item.TestID, "connection_id", item.ConnectionID, "error", err)
		}
	}
	return nil
}
