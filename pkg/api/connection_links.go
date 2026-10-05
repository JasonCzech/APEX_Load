package api

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/deeplink"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Connection kinds that carry trace link templates: the trace-lookup
// providers.
const (
	linkConnectionDynatrace = "dynatrace"
	linkConnectionELK       = "elk"

	connectionLinksDefinitionKind = storage.AnalysisDefinitionKindConnectionLinks
	// exemplarLinkWindow pads an exemplar's time on both sides of its link.
	exemplarLinkWindow = 5 * time.Minute
)

// traceLinkConnectionKinds maps a trace-lookup provider to the connection
// kind that holds its link templates.
var traceLinkConnectionKinds = map[string]string{
	monitorprovider.ProviderDynatrace:     linkConnectionDynatrace,
	monitorprovider.ProviderElasticsearch: linkConnectionELK,
}

// kibanaFieldName is a field name safe to place in a Discover KQL query
// inside the template's rison string.
var kibanaFieldName = regexp.MustCompile(`^[A-Za-z0-9_@][A-Za-z0-9_.@-]{0,127}$`)

// ConnectionLinkSettings are one connection's deep link templates, stored as
// a revisioned analysis definition. UIURL names Kibana in front of an
// Elasticsearch connection, whose API host is not its UI; links may target
// only the connection's hosts and that UI host.
type ConnectionLinkSettings struct {
	UIURL         string `json:"ui_url,omitempty"`
	TraceTemplate string `json:"trace_template,omitempty"`
}

// connectionLinkTarget is a connection's hosts and default trace template,
// resolved from storage.
type connectionLinkTarget struct {
	hosts []string
	// acceptsUIURL is true for connections whose product UI is a separate
	// host the project admin declares.
	acceptsUIURL bool
	defaultTrace func(ConnectionLinkSettings) string
}

func connectionLinksDefinitionID(kind, connectionID string) string {
	return "connection-links:" + kind + ":" + connectionID
}

// resolveConnectionLinkTarget loads a connection of the project and the hosts
// its links may target.
func (s *Server) resolveConnectionLinkTarget(project, kind, connectionID string) (*connectionLinkTarget, error) {
	switch kind {
	case linkConnectionDynatrace:
		store, ok := s.store.(storage.DynatraceConnectionStore)
		if !ok {
			return nil, errors.New("Dynatrace connection storage unavailable")
		}
		connection, err := store.GetDynatraceConnection(connectionID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, errLinkStorage
		}
		if connection == nil || connection.ProjectID != project {
			return nil, errConnectionNotFound
		}
		hosts := compactNonEmptyStrings(deeplink.HostOf(connection.EnvironmentURL), deeplink.HostOf(connection.PlatformURL))
		base := strings.TrimRight(firstNonEmptyString(connection.PlatformURL, connection.EnvironmentURL), "/")
		return &connectionLinkTarget{hosts: hosts, defaultTrace: func(ConnectionLinkSettings) string {
			if deeplink.HostOf(base) == "" {
				return ""
			}
			return base + "/ui/apps/dynatrace.distributedtracing/explorer?traceId={trace_id}"
		}}, nil
	case linkConnectionELK:
		store, ok := s.store.(interface {
			GetELKConnection(string) (*storage.ELKConnection, error)
		})
		if !ok {
			return nil, errors.New("Elasticsearch connection storage unavailable")
		}
		connection, err := store.GetELKConnection(connectionID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, errLinkStorage
		}
		if connection == nil || connection.ProjectID != project {
			return nil, errConnectionNotFound
		}
		// Search the connection's trace field when it has a plain name; a
		// free-text query otherwise.
		query := "%22{trace_id}%22"
		if field := strings.TrimSpace(connection.TraceIDField); kibanaFieldName.MatchString(field) {
			query = field + ":" + query
		}
		return &connectionLinkTarget{hosts: compactNonEmptyStrings(deeplink.HostOf(connection.Endpoint)), acceptsUIURL: true, defaultTrace: func(settings ConnectionLinkSettings) string {
			// Discover lives in Kibana, which is not the search endpoint; with
			// no Kibana URL there is no default.
			ui := strings.TrimRight(settings.UIURL, "/")
			if deeplink.HostOf(ui) == "" {
				return ""
			}
			return ui + "/app/discover#/?_g=(time:(from:'{from_utc}',to:'{to_utc}'))&_a=(query:(language:kuery,query:'" + query + "'))"
		}}, nil
	}
	return nil, errConnectionNotFound
}

var (
	errConnectionNotFound = errors.New("connection not found in project")
	errLinkStorage        = errors.New("link settings unavailable")
)

// allowedHosts adds the configured UI host to the connection's own hosts.
func (t *connectionLinkTarget) allowedHosts(settings ConnectionLinkSettings) []string {
	hosts := append([]string(nil), t.hosts...)
	if host := deeplink.HostOf(settings.UIURL); t.acceptsUIURL && host != "" {
		hosts = append(hosts, host)
	}
	return hosts
}

// traceTemplate is the saved template, or the connection's default.
func (t *connectionLinkTarget) traceTemplate(settings ConnectionLinkSettings) string {
	if strings.TrimSpace(settings.TraceTemplate) != "" {
		return settings.TraceTemplate
	}
	return t.defaultTrace(settings)
}

// validateConnectionLinkSettings checks the UI URL and the template in
// effect, which is the connection's default when none is saved.
func validateConnectionLinkSettings(target *connectionLinkTarget, settings ConnectionLinkSettings) error {
	if raw := strings.TrimSpace(settings.UIURL); raw != "" {
		if !target.acceptsUIURL {
			return errors.New("ui_url applies only to Elasticsearch (Kibana) connections")
		}
		// Braces would put placeholders into the default template's path.
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(raw) > 2048 || strings.ContainsAny(raw, "{} \t\r\n") {
			return errors.New("ui_url must be an https origin with an optional path")
		}
	}
	if template := target.traceTemplate(settings); template != "" {
		if err := deeplink.Validate(template, target.allowedHosts(settings)); err != nil {
			return err
		}
	}
	return nil
}

// loadConnectionLinkSettings reads the latest saved settings and revision;
// zero settings when none were saved.
func (s *Server) loadConnectionLinkSettings(project, kind, connectionID string) (ConnectionLinkSettings, int64, error) {
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		return ConnectionLinkSettings{}, 0, errors.New("link settings storage unavailable")
	}
	definition, err := store.GetAnalysisDefinition(project, connectionLinksDefinitionID(kind, connectionID), 0)
	if errors.Is(err, sql.ErrNoRows) {
		return ConnectionLinkSettings{}, 0, nil
	}
	if err != nil {
		return ConnectionLinkSettings{}, 0, errLinkStorage
	}
	if definition == nil || definition.Kind != connectionLinksDefinitionKind {
		return ConnectionLinkSettings{}, 0, nil
	}
	revision := definition.Revision
	if definition.Deleted {
		return ConnectionLinkSettings{}, revision, nil
	}
	var settings ConnectionLinkSettings
	if err := json.Unmarshal(definition.Definition, &settings); err != nil {
		return ConnectionLinkSettings{}, revision, errLinkStorage
	}
	return settings, revision, nil
}

// ConnectionLinksResponse reports saved settings and the template in effect.
type ConnectionLinksResponse struct {
	Kind          string                 `json:"kind"`
	ConnectionID  string                 `json:"connection_id"`
	Revision      int64                  `json:"revision"`
	Settings      ConnectionLinkSettings `json:"settings"`
	TraceTemplate string                 `json:"effective_trace_template,omitempty"`
}

// connectionLinksRequest is a PUT body; Revision is the one being replaced.
type connectionLinksRequest struct {
	Revision int64 `json:"revision"`
	ConnectionLinkSettings
}

func (s *Server) handleConnectionLinks(w http.ResponseWriter, r *http.Request) {
	project, kind, connectionID := r.PathValue("id"), r.PathValue("kind"), r.PathValue("connection_id")
	target, err := s.resolveConnectionLinkTarget(project, kind, connectionID)
	if errors.Is(err, errConnectionNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	settings, revision, err := s.loadConnectionLinkSettings(project, kind, connectionID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if r.Method == http.MethodPut {
		var request connectionLinksRequest
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if !decodeJSONBody(w, r, &request) {
			return
		}
		next := ConnectionLinkSettings{UIURL: strings.TrimSpace(request.UIURL), TraceTemplate: strings.TrimSpace(request.TraceTemplate)}
		if err := validateConnectionLinkSettings(target, next); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		body, _ := json.Marshal(next)
		store := s.store.(storage.AnalysisDefinitionStore)
		saved, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{
			ID: connectionLinksDefinitionID(kind, connectionID), ProjectID: project, Kind: connectionLinksDefinitionKind,
			Name: kind + " connection links", Definition: body,
		}, request.Revision)
		if err != nil {
			writeError(w, http.StatusConflict, "link settings revision conflict or resource limit")
			return
		}
		s.recordAuditEvent(r, "connection_links.save", "success", "connection_links", saved.ID, project, map[string]any{"revision": saved.Revision, "kind": kind})
		settings, revision = next, saved.Revision
	}
	writeJSON(w, http.StatusOK, ConnectionLinksResponse{Kind: kind, ConnectionID: connectionID, Revision: revision, Settings: settings, TraceTemplate: target.traceTemplate(settings)})
}

// ExemplarLink opens one exemplar's trace in a provider.
type ExemplarLink struct {
	Provider     string `json:"provider"`
	ConnectionID string `json:"connection_id"`
	URL          string `json:"url"`
}

// LinkedExemplar is an archived exemplar with its provider links.
type LinkedExemplar struct {
	ArchiveExemplar
	Links []ExemplarLink `json:"links"`
}

// ExemplarPage is one page of a run's archived exemplars. CaptureTruncated
// means the archive kept fewer exemplars than the run selected, not that
// more pages follow; Total counts the matching exemplars across all pages.
// LinksUnavailable means the link settings could not be read, so exemplars
// carry no links.
type ExemplarPage struct {
	Exemplars        []LinkedExemplar `json:"exemplars"`
	Total            int              `json:"total"`
	CaptureTruncated bool             `json:"capture_truncated"`
	LinksUnavailable bool             `json:"links_unavailable,omitempty"`
}

// traceLinkTarget is one of a run's trace-capable connections with its
// template compiled once for the page.
type traceLinkTarget struct {
	provider, connectionID string
	template               *deeplink.Template
}

// runTraceLinkTargets are the run's trace-capable connections: its legacy
// ELK and Dynatrace captures and its trace-lookup monitor sources. A
// connection that is gone, or whose saved template no longer fits its
// hosts, has no links.
func (s *Server) runTraceLinkTargets(test *ManagedTest) ([]traceLinkTarget, error) {
	type ref struct{ provider, id string }
	refs := map[ref]bool{}
	if test.ELKCapture != nil && test.ELKCapture.ConnectionID != "" {
		refs[ref{monitorprovider.ProviderElasticsearch, test.ELKCapture.ConnectionID}] = true
	}
	if test.DynatraceCapture != nil && test.DynatraceCapture.ConnectionID != "" {
		refs[ref{monitorprovider.ProviderDynatrace, test.DynatraceCapture.ConnectionID}] = true
	}
	if monitors, ok := s.store.(storage.RunMonitorStore); ok {
		jobs, err := monitors.ListRunMonitorJobs(test.ProjectID, test.ID)
		if err != nil {
			return nil, err
		}
		for _, job := range jobs {
			if d, ok := monitorprovider.Lookup(job.Source.Pack); ok && d.SupportsTraceLookup && job.Source.ConnectionID != "" {
				refs[ref{d.ProviderName, job.Source.ConnectionID}] = true
			}
		}
	}
	ordered := make([]ref, 0, len(refs))
	for r := range refs {
		if traceLinkConnectionKinds[r.provider] != "" {
			ordered = append(ordered, r)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].provider != ordered[j].provider {
			return ordered[i].provider < ordered[j].provider
		}
		return ordered[i].id < ordered[j].id
	})
	var out []traceLinkTarget
	for _, r := range ordered {
		kind := traceLinkConnectionKinds[r.provider]
		target, err := s.resolveConnectionLinkTarget(test.ProjectID, kind, r.id)
		if errors.Is(err, errConnectionNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		settings, _, err := s.loadConnectionLinkSettings(test.ProjectID, kind, r.id)
		if err != nil {
			return nil, err
		}
		template := target.traceTemplate(settings)
		if template == "" {
			continue
		}
		if compiled, err := deeplink.Compile(template, target.allowedHosts(settings)); err == nil {
			out = append(out, traceLinkTarget{provider: r.provider, connectionID: r.id, template: compiled})
		}
	}
	return out, nil
}

// handleGetRunArchiveExemplars pages through a run's post-run exemplars,
// optionally for one group and transaction name, each with deep links into
// the run's trace providers.
func (s *Server) handleGetRunArchiveExemplars(w http.ResponseWriter, r *http.Request) {
	test, record, lease, ok := s.requireRunArchiveTest(w, r)
	if !ok {
		return
	}
	defer lease.Release()
	page, ok := parseArchivePage(w, r)
	if !ok {
		return
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive manifest")
		return
	}
	out := ExemplarPage{Exemplars: []LinkedExemplar{}, CaptureTruncated: manifest.ExemplarsTruncated}
	if manifest.ExemplarsFile == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	file, _, err := openArchiveArtifactForRead(record.ArchivePath, manifest.ExemplarsFile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive exemplars")
		return
	}
	defer file.Close()
	// Exemplars are read from the archive; links are secondary, so a link
	// settings failure leaves them unlinked rather than failing the page.
	targets, err := s.runTraceLinkTargets(test)
	if err != nil {
		slog.Warn("exemplar trace links unavailable", "test_id", test.ID, "error", err)
		out.LinksUnavailable, targets = true, nil
	}
	name, group := strings.TrimSpace(r.URL.Query().Get("name")), strings.TrimSpace(r.URL.Query().Get("group"))
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		var exemplar ArchiveExemplar
		if err := json.Unmarshal(scanner.Bytes(), &exemplar); err != nil {
			writeError(w, http.StatusInternalServerError, "archived exemplar is invalid")
			return
		}
		if name != "" && exemplar.Name != name || group != "" && exemplar.Group != group {
			continue
		}
		out.Total++
		if out.Total <= page.Offset || len(out.Exemplars) >= page.Limit {
			continue
		}
		linked := LinkedExemplar{ArchiveExemplar: exemplar, Links: []ExemplarLink{}}
		values := deeplink.Values{TraceID: exemplar.TraceID, From: exemplar.Timestamp.Add(-exemplarLinkWindow), To: exemplar.Timestamp.Add(exemplarLinkWindow)}
		for _, t := range targets {
			if link, err := t.template.Expand(values); err == nil {
				linked.Links = append(linked.Links, ExemplarLink{Provider: t.provider, ConnectionID: t.connectionID, URL: link})
			}
		}
		out.Exemplars = append(out.Exemplars, linked)
	}
	if err := scanner.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read archive exemplars")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
