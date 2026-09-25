package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

const confluenceMaxResponseBytes = 1 << 20

type confluencePage struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	SpaceID  string `json:"spaceId"`
	ParentID string `json:"parentId"`
	Version  struct {
		Number int `json:"number"`
	} `json:"version"`
}

func confluencePageTitle(report *RunArchiveReport, testID string) string {
	name := "Run"
	if report != nil && report.Manifest != nil && strings.TrimSpace(report.Manifest.TestName) != "" {
		name = strings.TrimSpace(report.Manifest.TestName)
	}
	runes := []rune(name)
	if len(runes) > 160 {
		name = string(runes[:160])
	}
	return fmt.Sprintf("%s — APEX Load run %s", name, testID)
}

func confluencePageBody(report *RunArchiveReport, reportURL string) string {
	if report == nil || report.Manifest == nil {
		return "<p>Run evidence is unavailable.</p>"
	}
	e := html.EscapeString
	m := report.Manifest
	o := report.Overview
	var b strings.Builder
	fmt.Fprintf(&b, "<p><strong>Run:</strong> %s<br/><strong>Execution:</strong> %s<br/><strong>Completed:</strong> %s</p>", e(m.TestID), e(m.Status), e(m.CompletedAt.UTC().Format(time.RFC3339)))
	if report.Verdict != nil {
		fmt.Fprintf(&b, "<p><strong>Verdict:</strong> %s — %s</p>", e(report.Verdict.Status), e(report.Verdict.Explanation))
	}
	fmt.Fprintf(&b, "<h2>Summary</h2><table><tbody><tr><th>Transactions</th><td>%d</td></tr><tr><th>Errors</th><td>%d</td></tr><tr><th>Error rate</th><td>%.2f%%</td></tr><tr><th>Average response</th><td>%.2f ms</td></tr><tr><th>p95 response</th><td>%.2f ms</td></tr><tr><th>Peak VUsers</th><td>%d</td></tr></tbody></table>", o.Transactions, o.Errors, o.ErrorPct, o.AvgResponseMs, o.P95Ms, o.PeakActiveVUsers)
	if len(report.ByTransaction) > 0 {
		b.WriteString("<h2>Transactions</h2><table><tbody><tr><th>Name</th><th>Count</th><th>Errors</th><th>p95 ms</th></tr>")
		names := make([]string, 0, len(report.ByTransaction))
		for name := range report.ByTransaction {
			names = append(names, name)
		}
		sort.Strings(names)
		for i, name := range names {
			if i >= 100 {
				break
			}
			stats := report.ByTransaction[name]
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td><td>%d</td><td>%.2f</td></tr>", e(name), stats.Transactions, stats.Errors, stats.P95Ms)
		}
		b.WriteString("</tbody></table>")
	}
	if reportURL != "" {
		fmt.Fprintf(&b, "<p><a href=\"%s\">Open full report in APEX Load</a></p>", e(reportURL))
	}
	return b.String()
}

func (s *Server) confluenceHTTPClient(c *storage.ConfluenceConnection) (*http.Client, error) {
	u, err := url.Parse(c.SiteURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, errors.New("invalid Confluence site URL")
	}
	if err := validateOutboundURL(c.SiteURL + "/wiki/api/v2/pages"); err != nil {
		return nil, errors.New("Confluence site URL is not permitted")
	}
	return newSSRFSafeHTTPClient(10 * time.Second), nil
}

func (s *Server) confluenceToken(ctx context.Context, c *storage.ConfluenceConnection) (string, error) {
	if s.runner == nil {
		return "", errors.New("project secret resolution is unavailable")
	}
	token, ok := engine.ResolveSecret(ctx, s.runner.secretResolverForProject(c.ProjectID), c.TokenSecretName)
	if !ok || token == "" {
		return "", errors.New("Confluence API token secret is unavailable")
	}
	return token, nil
}

func confluenceRequest(ctx context.Context, client *http.Client, c *storage.ConfluenceConnection, token, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.SiteURL+"/wiki/api/v2"+path, reader)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Email, token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("Confluence request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, confluenceMaxResponseBytes+1))
	if err != nil {
		return errors.New("Confluence response could not be read")
	}
	if len(data) > confluenceMaxResponseBytes {
		return errors.New("Confluence response is too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Confluence returned HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("Confluence returned invalid JSON")
		}
	}
	return nil
}

func (s *Server) verifyConfluenceDestination(ctx context.Context, c *storage.ConfluenceConnection) error {
	client, err := s.confluenceHTTPClient(c)
	if err != nil {
		return err
	}
	token, err := s.confluenceToken(ctx, c)
	if err != nil {
		return err
	}
	return checkConfluenceDestinationPage(ctx, client, c, token)
}

func checkConfluenceDestinationPage(ctx context.Context, client *http.Client, c *storage.ConfluenceConnection, token string) error {
	var page confluencePage
	if err := confluenceRequest(ctx, client, c, token, http.MethodGet, "/pages/"+url.PathEscape(c.ParentPageID), nil, &page); err != nil {
		return err
	}
	if page.ID != c.ParentPageID || page.SpaceID != c.SpaceID {
		return errors.New("parent page does not belong to the configured space")
	}
	return nil
}

func findConfluenceRunPage(ctx context.Context, client *http.Client, c *storage.ConfluenceConnection, token, title string) (*confluencePage, error) {
	query := url.Values{"title": {title}, "space-id": {c.SpaceID}, "limit": {"25"}}
	var result struct {
		Results []confluencePage `json:"results"`
	}
	if err := confluenceRequest(ctx, client, c, token, http.MethodGet, "/pages?"+query.Encode(), nil, &result); err != nil {
		return nil, err
	}
	for _, page := range result.Results {
		if page.Title == title && page.SpaceID == c.SpaceID && page.ParentID == c.ParentPageID {
			return &page, nil
		}
	}
	return nil, nil
}

func publishConfluencePage(ctx context.Context, client *http.Client, c *storage.ConfluenceConnection, token string, previous *storage.ConfluencePublication, report *RunArchiveReport, reportURL string) (string, string, error) {
	title := confluencePageTitle(report, report.Manifest.TestID)
	body := map[string]any{"representation": "storage", "value": confluencePageBody(report, reportURL)}
	pageID := ""
	if previous != nil {
		pageID = previous.PageID
	}
	var existing *confluencePage
	if pageID != "" {
		existing = &confluencePage{ID: pageID}
		if err := confluenceRequest(ctx, client, c, token, http.MethodGet, "/pages/"+url.PathEscape(pageID), nil, existing); err != nil {
			return "", "", err
		}
	}
	if existing == nil {
		found, err := findConfluenceRunPage(ctx, client, c, token, title)
		if err != nil {
			return "", "", err
		}
		existing = found
	}
	var page confluencePage
	if existing == nil {
		payload := map[string]any{"spaceId": c.SpaceID, "parentId": c.ParentPageID, "status": "current", "title": title, "body": body}
		if err := confluenceRequest(ctx, client, c, token, http.MethodPost, "/pages", payload, &page); err != nil {
			return "", "", err
		}
	} else {
		if existing.Title != title || existing.SpaceID != c.SpaceID || existing.ParentID != c.ParentPageID {
			return "", "", errors.New("existing Confluence page has a different destination")
		}
		payload := map[string]any{"id": existing.ID, "status": "current", "title": title, "body": body, "version": map[string]any{"number": existing.Version.Number + 1}}
		if err := confluenceRequest(ctx, client, c, token, http.MethodPut, "/pages/"+url.PathEscape(existing.ID), payload, &page); err != nil {
			return "", "", err
		}
	}
	if page.ID == "" {
		return "", "", errors.New("Confluence returned no page ID")
	}
	return page.ID, c.SiteURL + "/wiki/spaces/" + url.PathEscape(c.SpaceID) + "/pages/" + url.PathEscape(page.ID), nil
}
