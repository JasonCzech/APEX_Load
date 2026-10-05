package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func executeBrowserDocumentCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("apexctl analysis browser-documents", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("api", firstNonEmptyCLI(os.Getenv("APEXLOAD_API"), "http://127.0.0.1:8080"), "controller URL")
	id := flags.String("id", "", "managed run ID")
	mode := flags.String("mode", "live", "live or archive document evidence")
	apiKey := flags.String("api-key", os.Getenv("APEXLOAD_API_KEY"), "controller API key")
	bearer := flags.String("bearer-token", os.Getenv("APEXLOAD_BEARER_TOKEN"), "controller bearer token")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err := fmt.Fprintln(out, "Usage: apexctl analysis browser-documents --id RUN [--mode live|archive] [--api URL] [--api-key KEY|--bearer-token TOKEN]\nReads the run-wide independent document population. Live provisional counts are cumulative history and overlap finals; archive counts use latest revisions and pending provisional states. No application or telemetry verdict is inferred.")
			return err
		}
		return err
	}
	if flags.NArg() != 0 || *id == "" {
		return errors.New("browser-documents requires --id and does not accept positional arguments or population filters")
	}
	address := strings.TrimRight(strings.TrimSpace(*endpoint), "/")
	if err := validateCLIControllerURL(address); err != nil {
		return err
	}
	return NewApiClientWithAuth(address, *apiKey, *bearer).readBrowserDocuments(ctx, *id, *mode, out)
}

// This projection preserves the distinct document population and omits unrelated
// run metadata. Legacy absent evidence stays absent, without a healthy zero.
type browserDocumentCLIProjection struct {
	IdentityState        string                                `json:"identity_state"`
	TestID               string                                `json:"test_id"`
	Mode                 string                                `json:"mode"`
	Population           string                                `json:"population"`
	PublicationSemantics string                                `json:"publication_semantics"`
	EvidenceState        string                                `json:"evidence_state"`
	UnavailableReason    string                                `json:"unavailable_reason,omitempty"`
	Summary              *protocol.BrowserDocumentSummary      `json:"browser_documents,omitempty"`
	Observations         []protocol.BrowserDocumentObservation `json:"browser_document_evidence,omitempty"`
	Truncated            bool                                  `json:"browser_document_evidence_truncated,omitempty"`
}

// Response identity extraction keeps canonical wire names and rejects duplicate
// decoded keys. Reports and summary metadata may contain unrelated read context;
// none of it grants identity authority or enters this command's output.
func browserDocumentIdentityObject(data []byte, names ...string) (map[string]json.RawMessage, error) {
	object, err := analysisCLIObject(data, 8<<20, nil, 256)
	if err != nil {
		return nil, errors.New("invalid browser document response identity")
	}
	for key := range object {
		for _, name := range names {
			if key != name && strings.EqualFold(key, name) {
				return nil, errors.New("ambiguous browser document response identity")
			}
		}
	}
	return object, nil
}
func browserDocumentIdentityText(object map[string]json.RawMessage, name string, required bool) (string, error) {
	raw, present := object[name]
	if !present && !required {
		return "", nil
	}
	value, err := analysisCLIText(raw, 128, required)
	if err != nil || analysisCLIScopeAdmission(value, "") != nil {
		return "", errors.New("invalid browser document response identity")
	}
	return value, nil
}
func (c *ApiClient) browserDocumentResponseIdentity(ctx context.Context, requested, mode string, data []byte) (string, string, error) {
	object, err := browserDocumentIdentityObject(data, "test_id", "manifest")
	if err != nil {
		return "", "", err
	}
	top, err := browserDocumentIdentityText(object, "test_id", false)
	if err != nil {
		return "", "", err
	}
	canonical, project := "", ""
	if raw, present := object["manifest"]; present && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		manifest, err := browserDocumentIdentityObject(raw, "test_id", "provenance")
		if err != nil {
			return "", "", err
		}
		canonical, err = browserDocumentIdentityText(manifest, "test_id", false)
		if err != nil {
			return "", "", err
		}
		if raw, present := manifest["provenance"]; present && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			provenance, err := browserDocumentIdentityObject(raw, "project_id")
			if err != nil {
				return "", "", err
			}
			project, err = browserDocumentIdentityText(provenance, "project_id", false)
			if err != nil {
				return "", "", err
			}
		}
	}
	if top != "" && canonical != "" && top != canonical {
		return "", "", errors.New("conflicting browser document response identities")
	}
	if mode != "archive" || canonical == "" {
		returned := top
		if returned == "" && mode != "archive" {
			returned = canonical
		}
		if returned == "" {
			return requested, "historical-requested-unverified", nil
		}
		if returned != requested {
			return "", "", errors.New("browser document response does not match requested run")
		}
		return returned, "response-matched", nil
	}
	if canonical == requested {
		return canonical, "response-matched", nil
	}
	// The archive manifest uses a canonical ID, whereas the public run routes
	// also accept RefKeys. One bounded authenticated no-history read must prove
	// that the exact requested alias identifies this exact canonical run.
	lookup, err := c.workflowRequest(ctx, http.MethodGet, "/api/v1/tests/"+url.PathEscape(requested), nil, "")
	if err != nil {
		if contextErr := analysisCLIContextLive(ctx); contextErr != nil {
			return "", "", contextErr
		}
		return "", "", errors.New("browser document run identity could not be verified")
	}
	identity, err := browserDocumentIdentityObject(lookup, "id", "ref_key", "project_id")
	if err != nil {
		return "", "", errors.New("browser document run identity could not be verified")
	}
	id, idErr := browserDocumentIdentityText(identity, "id", true)
	ref, refErr := browserDocumentIdentityText(identity, "ref_key", true)
	observedProject, projectErr := browserDocumentIdentityText(identity, "project_id", false)
	if idErr != nil || refErr != nil || projectErr != nil || id != canonical || ref != requested || project != "" && observedProject != project {
		return "", "", errors.New("browser document run identity could not be verified")
	}
	return canonical, "canonical-ref-key-matched", nil
}

func (c *ApiClient) readBrowserDocuments(ctx context.Context, id, mode string, out io.Writer) error {
	if err := analysisCLIScopeAdmission(id, ""); err != nil {
		return err
	}
	if mode != "live" && mode != "archive" {
		return errors.New("--mode must be live or archive")
	}
	if err := analysisCLIContextLive(ctx); err != nil {
		return err
	}
	path := "/api/v1/tests/" + url.PathEscape(id)
	if mode == "live" {
		path += "/metrics?history_limit=1"
	} else {
		path += "/archive/report?include_release_trend=false"
	}
	data, err := c.workflowRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	var wire struct {
		Summary json.RawMessage `json:"browser_documents"`
		Metrics *struct {
			Summary json.RawMessage `json:"browser_documents"`
		} `json:"metrics"`
		Observations []json.RawMessage `json:"browser_document_evidence"`
		Truncated    bool              `json:"browser_document_evidence_truncated"`
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("invalid browser document response")
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return errors.New("invalid browser document response")
	}
	returnedID, identityState, err := c.browserDocumentResponseIdentity(ctx, id, mode, data)
	if err != nil {
		return err
	}
	projection := browserDocumentCLIProjection{TestID: returnedID, IdentityState: identityState, Mode: mode, Population: "document-lifecycle-metric", EvidenceState: "absent", UnavailableReason: "document_evidence_absent"}
	summary := wire.Summary
	if mode == "live" {
		summary = nil
		if wire.Metrics != nil {
			summary = wire.Metrics.Summary
		}
		projection.PublicationSemantics = "cumulative-first-seen-provisional-history-overlaps-final"
	} else {
		projection.PublicationSemantics = "latest-measurement-revision-provisional-is-pending"
	}
	if len(summary) != 0 && !bytes.Equal(bytes.TrimSpace(summary), []byte("null")) {
		projection.Summary, err = protocol.DecodeBrowserDocumentSummary(summary)
		if err != nil {
			return errors.New("unsupported or invalid browser document summary")
		}
		projection.EvidenceState, projection.UnavailableReason = "present", ""
	}
	if mode == "archive" {
		if len(wire.Observations) > protocol.MaxBrowserDocumentObservations || len(wire.Observations) > 0 && projection.Summary == nil {
			return errors.New("invalid browser document observation population")
		}
		seen := make(map[string]bool, len(wire.Observations))
		for _, raw := range wire.Observations {
			var identity struct {
				SourceGeneration string `json:"source_generation"`
			}
			if json.Unmarshal(raw, &identity) != nil {
				return errors.New("invalid browser document observation")
			}
			envelope, err := json.Marshal(struct {
				Version                  int               `json:"version"`
				SourceGeneration         string            `json:"source_generation"`
				Observations             []json.RawMessage `json:"observations"`
				DroppedObservations      int64             `json:"dropped_observations"`
				Incomplete               bool              `json:"incomplete"`
				IssuedRequests           int64             `json:"issued_requests"`
				InFlightRequests         int64             `json:"in_flight_requests"`
				DroppedRequestIdentities int64             `json:"dropped_request_identities"`
			}{Version: protocol.BrowserEvidenceVersion, SourceGeneration: identity.SourceGeneration, Observations: []json.RawMessage{raw}})
			if err != nil {
				return errors.New("invalid browser document observation")
			}
			copy, err := protocol.DecodeBrowserDocumentEvidence(envelope)
			if err != nil || len(copy.Observations) != 1 {
				return errors.New("unsupported or invalid browser document observation")
			}
			observation := copy.Observations[0]
			key := observation.SourceGeneration + "\x00" + observation.MeasurementID
			if seen[key] {
				return errors.New("unsupported or invalid browser document observation")
			}
			seen[key] = true
			projection.Observations = append(projection.Observations, copy.Observations[0])
		}
		projection.Truncated = wire.Truncated
	}
	encoded, err := json.MarshalIndent(projection, "", "  ")
	if err != nil {
		return errors.New("could not encode browser document evidence")
	}
	if err := analysisCLIContextLive(ctx); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}
