package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/harsynth"
)

// BrowserHAREntryResponse is one retained browser request as a HAR entry: the
// last step of the browser intelligence drill-down (finding → document →
// resources → HAR entry). A request whose browser context recorded a native
// HAR is not in the synthetic HAR, so the response names that native HAR
// instead of deriving an entry.
type BrowserHAREntryResponse struct {
	RequestID        string `json:"request_id"`
	SourceGeneration string `json:"source_generation,omitempty"`
	// Fidelity is synthetic (Entry is set) or native (Artifact names the
	// native HAR that holds the request).
	Fidelity string          `json:"fidelity"`
	Artifact string          `json:"artifact,omitempty"`
	Entry    json.RawMessage `json:"entry,omitempty"`
	// HeadersOmitted is true when the archive keeps no headers (a Parquet
	// transactions file), as the synthetic HAR states.
	HeadersOmitted bool `json:"headers_omitted,omitempty"`
}

var errBrowserHAREntryFound = errors.New("browser HAR entry found")

// handleGetRunArchiveBrowserHAREntry returns the HAR entry of the retained
// browser request with request_id. It needs the HAR download permission:
// an entry carries what the synthetic HAR carries.
//
// OpenAPI-Responses: 400, 404, 500
func (s *Server) handleGetRunArchiveBrowserHAREntry(w http.ResponseWriter, r *http.Request) {
	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	if requestID == "" || len(requestID) > 128 {
		writeError(w, http.StatusBadRequest, "request_id is required (at most 128 bytes)")
		return
	}
	clearArchiveWriteDeadline(w)
	ctx, cancel := context.WithTimeout(r.Context(), configuredArchiveMaterializationTimeout())
	defer cancel()
	id := r.PathValue("id")
	archives := s.archiveDomainService()
	if _, ok := archives.GetTest(id); !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	record, ok := archives.GetRunArchiveContext(r.Context(), id)
	if !ok {
		writeError(w, http.StatusNotFound, "archive not found")
		return
	}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to materialize archive: "+err.Error())
		return
	}
	defer lease.Release()
	manifest, err := archives.GetRunArchiveManifest(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	response, err := findRunArchiveBrowserHAREntry(ctx, record.ArchivePath, manifest, requestID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, response)
	case errors.Is(err, errSyntheticHARSourceUnavailable):
		writeError(w, http.StatusNotFound, "this archive has no transactions to read browser requests from")
	case errors.Is(err, errBrowserHAREntryNotFound):
		writeError(w, http.StatusNotFound, "no retained browser request has this request_id")
	default:
		writeError(w, http.StatusInternalServerError, "failed to read the browser request")
	}
}

var errBrowserHAREntryNotFound = errors.New("browser HAR entry not found")

// findRunArchiveBrowserHAREntry scans the archive's transactions, as the
// synthetic HAR does, for the first network entry with requestID.
func findRunArchiveBrowserHAREntry(ctx context.Context, archivePath string, manifest *RunArchiveManifest, requestID string) (*BrowserHAREntryResponse, error) {
	if manifest == nil {
		return nil, errSyntheticHARSourceUnavailable
	}
	name, format := manifest.TransactionsFile, "jsonl"
	if name == "" {
		name, format = manifest.TransactionsParquetFile, "parquet"
	}
	if name == "" {
		return nil, errSyntheticHARSourceUnavailable
	}
	file, _, err := openArchiveArtifactForRead(archivePath, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	covered, _ := nativeHARCoveredGenerations(manifest)
	headers := format != "parquet"
	var response *BrowserHAREntryResponse
	visit := func(raw []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var event TransactionEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return fmt.Errorf("decode archived transaction: %w", err)
		}
		sanitizeArchivedTransaction(&event)
		entries, pageURL := event.NetworkEntries, event.PageURL
		if len(entries) == 0 && event.Snapshot != nil {
			entries = event.Snapshot.NetworkEntries
			if pageURL == "" {
				pageURL = event.Snapshot.PageURL
			}
		}
		for _, entry := range entries {
			evidence := entry.BrowserEvidence
			if evidence == nil || evidence.RequestID != requestID {
				continue
			}
			found := &BrowserHAREntryResponse{RequestID: requestID, SourceGeneration: evidence.SourceGeneration}
			if covered[evidence.SourceGeneration] {
				found.Fidelity = "native"
				for _, har := range manifest.BrowserHARArtifacts {
					if har.EvidenceGeneration == evidence.SourceGeneration && validDeclaredHAR(har) {
						found.Artifact = har.Name
						break
					}
				}
				response = found
				return errBrowserHAREntryFound
			}
			engine := ""
			if event.ClientMetrics != nil {
				engine = event.ClientMetrics.BrowserEngine
			}
			if !headers {
				entry.RequestHeaders, entry.ResponseHeaders = nil, nil
			}
			raw, err := syntheticHAREntryJSON(harsynth.Entry{RecordTime: event.Timestamp, ActionName: event.ActionName, PageURL: pageURL, BrowserEngine: engine, Network: entry}, headers)
			if err != nil {
				return err
			}
			found.Fidelity, found.Entry, found.HeadersOmitted = harsynth.Fidelity, raw, !headers
			response = found
			return errBrowserHAREntryFound
		}
		return nil
	}
	if format == "parquet" {
		err = visitBrowserResourceParquetRows(ctx, file, visit)
	} else {
		err = visitArchiveJSONLRows(ctx, file, visit)
	}
	if errors.Is(err, errBrowserHAREntryFound) {
		return response, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, errBrowserHAREntryNotFound
}

// syntheticHAREntryJSON writes one entry through the synthetic HAR writer
// (the same scrubbing and fields as a downloaded synthetic HAR) and returns
// that entry.
func syntheticHAREntryJSON(entry harsynth.Entry, headers bool) (json.RawMessage, error) {
	meta := harsynth.Metadata{CaptureMode: harsynth.CaptureRetainedNetworkEntries}
	if !headers {
		meta.ExtraOmittedFields = []string{"request.headers", "response.headers"}
	}
	var out bytes.Buffer
	writer := harsynth.NewWriter(&out, meta)
	if err := writer.Add(entry); err != nil {
		return nil, err
	}
	if _, err := writer.Close(); err != nil {
		return nil, err
	}
	var document struct {
		Log struct {
			Entries []json.RawMessage `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(out.Bytes(), &document); err != nil {
		return nil, fmt.Errorf("decode synthetic HAR entry: %w", err)
	}
	if len(document.Log.Entries) != 1 {
		return nil, errBrowserHAREntryNotFound
	}
	return document.Log.Entries[0], nil
}
