package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/harsynth"
)

// Synthetic HAR fallback (D6). A run archive's retained browser network
// entries become a HAR 1.2 for the browser contexts that have no native HAR
// in the archive: contexts recorded without native capture, and generators
// older than native HAR transfer. It is derived from the archive when asked
// for and never stored, so archives written before it existed have one too,
// and the same archive always yields the same bytes. Entries go through the
// same sanitizer as the safe transaction download first, so a synthetic HAR
// never shows more than that download does.

// RunArchiveSyntheticHARName is the synthetic HAR's name among a run
// archive's HARs.
const RunArchiveSyntheticHARName = "browser_har_synthetic.har"

// runArchiveSyntheticHARSummary describes a derived synthetic HAR.
type runArchiveSyntheticHARSummary struct {
	harsynth.Summary
	SourceFormat string
}

var errSyntheticHARSourceUnavailable = errors.New("run archive has no transaction source for a synthetic HAR")

// nativeHARCoveredGenerations names the browser evidence generations whose
// contexts have a native HAR in the archive; their entries are left out of
// the synthetic HAR (counted as native_har).
func nativeHARCoveredGenerations(manifest *RunArchiveManifest) map[string]bool {
	covered := map[string]bool{}
	if manifest == nil {
		return covered
	}
	for _, har := range manifest.BrowserHARArtifacts {
		if har.EvidenceGeneration != "" {
			covered[har.EvidenceGeneration] = true
		}
	}
	return covered
}

// writeRunArchiveSyntheticHAR streams the archive's transactions (JSONL, or
// Parquet when the archive has no JSONL) into a synthetic HAR on out.
func writeRunArchiveSyntheticHAR(ctx context.Context, archivePath string, manifest *RunArchiveManifest, out io.Writer) (runArchiveSyntheticHARSummary, error) {
	if ctx == nil || manifest == nil {
		return runArchiveSyntheticHARSummary{}, errSyntheticHARSourceUnavailable
	}
	name, format := manifest.TransactionsFile, "jsonl"
	if name == "" {
		name, format = manifest.TransactionsParquetFile, "parquet"
	}
	if name == "" {
		return runArchiveSyntheticHARSummary{}, errSyntheticHARSourceUnavailable
	}
	file, _, err := openArchiveArtifactForRead(archivePath, name)
	if err != nil {
		return runArchiveSyntheticHARSummary{}, err
	}
	defer file.Close()
	meta := harsynth.Metadata{CaptureMode: harsynth.CaptureRetainedNetworkEntries}
	headers := format != "parquet"
	if !headers {
		// The Parquet resource projection keeps no headers (a legacy file's
		// are dropped too, so every Parquet-derived HAR says the same).
		meta.ExtraOmittedFields = []string{"request.headers", "response.headers"}
	}
	writer := harsynth.NewWriter(out, meta)
	covered := nativeHARCoveredGenerations(manifest)
	visit := func(raw []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var event TransactionEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return fmt.Errorf("decode archived transaction: %w", err)
		}
		sanitizeArchivedTransaction(&event)
		return addSyntheticHAREntries(writer, &event, covered, headers)
	}
	if format == "parquet" {
		err = visitBrowserResourceParquetRows(ctx, file, visit)
	} else {
		err = visitArchiveJSONLRows(ctx, file, visit)
	}
	if err != nil {
		return runArchiveSyntheticHARSummary{}, err
	}
	summary, err := writer.Close()
	return runArchiveSyntheticHARSummary{Summary: summary, SourceFormat: format}, err
}

// addSyntheticHAREntries adds one transaction's network entries: its own,
// or its error snapshot's when it has none (the resource reader's rule).
func addSyntheticHAREntries(writer *harsynth.Writer, event *TransactionEvent, covered map[string]bool, headers bool) error {
	entries, pageURL := event.NetworkEntries, event.PageURL
	if len(entries) == 0 && event.Snapshot != nil {
		entries = event.Snapshot.NetworkEntries
		if pageURL == "" {
			pageURL = event.Snapshot.PageURL
		}
	}
	engine := ""
	if event.ClientMetrics != nil {
		engine = event.ClientMetrics.BrowserEngine
	}
	for _, entry := range entries {
		if evidence := entry.BrowserEvidence; evidence != nil && covered[evidence.SourceGeneration] {
			writer.Omit(harsynth.OmittedCoveredByHAR)
			continue
		}
		if !headers {
			entry.RequestHeaders, entry.ResponseHeaders = nil, nil
		}
		if err := writer.Add(harsynth.Entry{RecordTime: event.Timestamp, ActionName: event.ActionName, PageURL: pageURL, BrowserEngine: engine, Network: entry}); err != nil {
			return err
		}
	}
	return nil
}

// visitArchiveJSONLRows calls visit with each non-blank archived record.
func visitArchiveJSONLRows(ctx context.Context, source io.Reader, visit func([]byte) error) error {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 0, 64<<10), maxBrowserResourceReadRecordBytes)
	budget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)
	for scanner.Scan() {
		if err := budget.Observe(scanner.Bytes()); err != nil {
			return err
		}
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		if err := visit(scanner.Bytes()); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// writeReplaySyntheticHAR writes a replay's retained network entries (each
// event's own, or its error snapshot's when it has none) as a synthetic HAR.
// The events are the replay report's, already redacted at capture.
func writeReplaySyntheticHAR(events []engine.ReplayActionEvent, out io.Writer) (harsynth.Summary, error) {
	writer := harsynth.NewWriter(out, harsynth.Metadata{CaptureMode: harsynth.CaptureReplayNetworkEntries})
	for _, event := range events {
		entries, pageURL := event.Network, event.PageURL
		if len(entries) == 0 && event.Snapshot != nil {
			entries = event.Snapshot.NetworkEntries
			if pageURL == "" {
				pageURL = event.Snapshot.PageURL
			}
		}
		for _, entry := range entries {
			if err := writer.Add(harsynth.Entry{RecordTime: event.StartedAt, ActionName: event.ActionName, PageURL: pageURL, Network: entry}); err != nil {
				return harsynth.Summary{}, err
			}
		}
	}
	return writer.Close()
}
