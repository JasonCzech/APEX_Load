package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type ConnectionTestRequest struct {
	Provider         string `json:"provider"`
	ExpectedRevision string `json:"expected_revision"`
}

func (s *Server) handleConnectionTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input ConnectionTestRequest
	if !decodeJSONBody(w, r, &input) {
		return
	}
	settings := s.runner.GetSystemSettings()
	if input.ExpectedRevision == "" || input.ExpectedRevision != systemSettingsRevision(settings) {
		writeError(w, 409, "Reload and review the saved connection before testing")
		return
	}
	var base, path, token, username, password string
	var hosts []string
	var enabled, configured bool
	switch input.Provider {
	case "prometheus":
		p := settings.Prometheus
		base = p.BaseURL
		path = "/api/v1/query?query=vector%281%29"
		hosts = p.allowlistHosts()
		enabled = p.Enabled
		token = strings.TrimSpace(envValue(p.BearerTokenEnvVar))
		configured = base != "" && (p.BearerTokenEnvVar == "" || token != "")
	case "grafana":
		g := settings.Grafana
		base = g.BaseURL
		path = "/api/datasources"
		hosts = g.allowlistHosts()
		enabled = g.Enabled
		token = g.token()
		configured = base != "" && token != ""
	case "opensearch":
		o := settings.OpenSearch
		base = o.BaseURL
		path = "/"
		hosts = o.allowlistHosts()
		enabled, configured = newOpenSearchTrafficProfileSource(o).available()
		token = o.bearerToken()
		username = o.Username
		password = o.password()
	default:
		writeError(w, 400, "Choose Prometheus, Grafana, or OpenSearch")
		return
	}
	if !enabled || !configured {
		writeError(w, 409, "Enable and save the connection and supply its referenced credentials in the controller environment before testing")
		return
	}
	endpoint := strings.TrimRight(base, "/") + path
	allow := map[string]struct{}{}
	for _, host := range hosts {
		allow[strings.ToLower(host)] = struct{}{}
	}
	if err := validateOutboundURLAllowingHosts(endpoint, allow); err != nil {
		writeError(w, 400, "Saved connection URL is not permitted")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		writeError(w, 400, "Invalid saved connection URL")
		return
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if username != "" {
		req.SetBasicAuth(username, password)
	}
	response, err := newHostAllowlistedHTTPClient(hosts, 5*time.Second).Do(req)
	if err != nil {
		writeError(w, 502, "Connection test failed. Check the saved URL, network access, TLS and controller credentials.")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeError(w, 502, "Connection rejected the test. Check credentials and read permissions.")
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1048577))
	if err != nil || len(body) > 1048576 {
		writeError(w, 502, "Connection returned an unreadable or oversized response")
		return
	}
	var payload any
	if json.Unmarshal(body, &payload) != nil {
		writeError(w, 502, "Connection did not return the expected JSON response")
		return
	}
	valid := false
	switch input.Provider {
	case "prometheus":
		if object, ok := payload.(map[string]any); ok {
			valid = object["status"] == "success" && object["data"] != nil
		}
	case "grafana":
		_, valid = payload.([]any)
	case "opensearch":
		if object, ok := payload.(map[string]any); ok {
			valid = object["version"] != nil && object["cluster_name"] != nil
		}
	}
	if !valid {
		writeError(w, 502, "Response did not match the selected provider")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "connected", "checked_at": time.Now().UTC().Format(time.RFC3339), "message": "Saved connection responded successfully. This verifies API access; dashboard queries and metric collection must be checked separately."})
}
