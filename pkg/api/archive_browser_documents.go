package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

const browserDocumentsFileName = "browser_documents.jsonl"
const browserDocumentsParquetFileName = "browser_documents.parquet"
const maxArchiveBrowserDocumentMeasurements = 4096

func groupsRequestBrowserDocumentCapture(groups []TestGroup) bool {
	for _, group := range groups {
		if groupRequestsBrowserDocumentCapture(group) {
			return true
		}
	}
	return false
}

// groupRequestsBrowserDocumentCapture reports a group's document-metric
// opt-in (interaction capture is a separate feature).
func groupRequestsBrowserDocumentCapture(group TestGroup) bool {
	if group.script != nil {
		return dsl.BrowserDocumentCaptureRequested(group.script)
	}
	return group.BrowserDocumentCaptureRequested
}

func requiredBrowserFeaturesForGroup(group TestGroup) []string {
	if group.script != nil {
		return dsl.RequiredBrowserRuntimeFeatures(group.script)
	}
	if group.BrowserDocumentCaptureRequested {
		return []string{dsl.FeatureBrowserDocumentEvidenceV2}
	}
	return nil
}

type strictBrowserDocumentRecord protocol.BrowserDocumentEvidence

func (record *strictBrowserDocumentRecord) UnmarshalJSON(data []byte) error {
	evidence, err := protocol.DecodeBrowserDocumentEvidence(data)
	if err != nil {
		return err
	}
	*record = strictBrowserDocumentRecord(*evidence)
	return nil
}

type parquetBrowserDocumentRecord struct {
	SourceGeneration string `parquet:"name=source_generation, type=BYTE_ARRAY, convertedtype=UTF8"`
	EvidenceVersion  int32  `parquet:"name=evidence_version, type=INT32"`
	EvidenceJSON     string `parquet:"name=evidence_json, type=BYTE_ARRAY, convertedtype=UTF8"`
}

// Document lifecycle records have their own population and storage. They never
// become zero-duration transactions or contribute to legacy action Samples.
func (w *runArchiveWriter) RecordBrowserDocumentEvidence(evidence *protocol.BrowserDocumentEvidence) error {
	if w == nil {
		return errors.New("browser document archive is unavailable")
	}
	evidence = evidence.Clone()
	if evidence == nil {
		return errors.New("browser document evidence is malformed")
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	if len(encoded) > protocol.MaxBrowserDocumentEvidenceBytes {
		return errors.New("browser document evidence exceeds the record byte limit")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.flushErr != nil {
		return w.flushErr
	}
	if w.txFile == nil {
		return errors.New("browser document archive is closed")
	}
	if w.browserDocumentsEncoder == nil {
		file, err := replaceArchiveArtifactForWrite(w.rootDir, browserDocumentsFileName, 0o644, false)
		if err != nil {
			w.flushErr = fmt.Errorf("open browser document archive: %w", err)
			return w.flushErr
		}
		w.browserDocumentsFile = file
		w.browserDocumentsBuf = bufio.NewWriterSize(archiveCaptureFileWriter{file: file, budget: w.captureBudget}, 256*1024)
		w.browserDocumentsEncoder = json.NewEncoder(archiveCaptureRecordWriter{destination: w.browserDocumentsBuf, budget: w.captureBudget})
		w.manifest.BrowserDocumentsFile = browserDocumentsFileName
		w.manifest.BrowserDocumentsParquetFile = browserDocumentsParquetFileName
	}
	if err := w.browserDocumentsEncoder.Encode(evidence); err != nil {
		w.flushErr = fmt.Errorf("record browser document evidence: %w", err)
		return w.flushErr
	}
	w.manifest.BrowserDocumentRecordCount++
	return nil
}

func validArchiveSHA256(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size && value == hex.EncodeToString(data)
}

func validateBrowserDocumentManifest(manifest *RunArchiveManifest, requireProof bool) error {
	if manifest == nil {
		return errors.New("browser document manifest is unavailable")
	}
	if manifest.BrowserDocumentsFile == "" {
		if manifest.BrowserDocumentsParquetFile != "" || manifest.BrowserDocumentRecordCount != 0 || manifest.BrowserDocumentsSHA256 != "" || manifest.BrowserDocumentsParquetSHA256 != "" {
			return errors.New("browser document metadata requires its declared source artifact")
		}
		return nil
	}
	if manifest.BrowserDocumentsFile != browserDocumentsFileName || manifest.BrowserDocumentsParquetFile != browserDocumentsParquetFileName || manifest.BrowserDocumentRecordCount <= 0 || manifest.BrowserDocumentRecordCount > maxArchiveQueryRecords {
		return errors.New("browser document manifest has invalid artifact names or record count")
	}
	if requireProof && (!validArchiveSHA256(manifest.BrowserDocumentsSHA256) || !validArchiveSHA256(manifest.BrowserDocumentsParquetSHA256)) {
		return errors.New("browser document manifest is missing immutable artifact hashes")
	}
	return nil
}

func writeBrowserDocumentParquet(root string, manifest *RunArchiveManifest, budget *archiveOutputBudget) error {
	if err := validateBrowserDocumentManifest(manifest, false); err != nil {
		return err
	}
	if manifest.BrowserDocumentsFile == "" {
		return nil
	}
	count, err := streamJSONLToParquetChecked(filepath.Join(root, manifest.BrowserDocumentsFile), filepath.Join(root, manifest.BrowserDocumentsParquetFile), func(item strictBrowserDocumentRecord) (parquetBrowserDocumentRecord, error) {
		value := protocol.BrowserDocumentEvidence(item)
		evidence := value.Clone()
		if evidence == nil {
			return parquetBrowserDocumentRecord{}, errors.New("malformed browser document record")
		}
		data, err := json.Marshal(evidence)
		return parquetBrowserDocumentRecord{SourceGeneration: evidence.SourceGeneration, EvidenceVersion: int32(evidence.Version), EvidenceJSON: string(data)}, err
	}, budget)
	if err != nil {
		return fmt.Errorf("write browser document parquet: %w", err)
	}
	if count != manifest.BrowserDocumentRecordCount {
		return fmt.Errorf("browser document count is %d, want %d", count, manifest.BrowserDocumentRecordCount)
	}
	return nil
}

// Validate and hash the exact opened source while streaming bounded records.
// Callers must discard output when verification fails; no manifest is published.
func forEachBrowserDocumentArtifact(ctx context.Context, root, name, expectedSHA string, expectedCount int64, fn func(*protocol.BrowserDocumentEvidence) error) (err error) {
	file, _, err := openArchiveArtifactForRead(root, name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	digest := sha256.New()
	scanner := newArchiveJSONLScanner(io.TeeReader(file, digest))
	budget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)
	var count int64
	for scanner.Scan() {
		if err := budget.Observe(scanner.Bytes()); err != nil {
			return err
		}
		validated, err := protocol.DecodeBrowserDocumentEvidence(scanner.Bytes())
		if err != nil {
			return err
		}
		if err := fn(validated); err != nil {
			return err
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if count != expectedCount {
		return fmt.Errorf("browser document count is %d, want %d", count, expectedCount)
	}
	if hex.EncodeToString(digest.Sum(nil)) != expectedSHA {
		return errors.New("browser document artifact hash mismatch")
	}
	return ctx.Err()
}

type archiveBrowserMeasurement struct {
	observation protocol.BrowserDocumentObservation
	digest      [32]byte
}
type archiveBrowserDocumentAccumulator struct {
	measurements map[string]archiveBrowserMeasurement
	sourceDrops  map[string]int64
	dropped      int64
	incomplete   bool
}

func (a *archiveBrowserDocumentAccumulator) add(evidence *protocol.BrowserDocumentEvidence) {
	if a.measurements == nil {
		a.measurements = make(map[string]archiveBrowserMeasurement)
		a.sourceDrops = make(map[string]int64)
	}
	// Preserve the document-observation unit shared with live collection. A
	// request-identity loss makes coverage incomplete without inventing how many
	// document observations were lost; its raw counter remains in the sidecar.
	a.incomplete = a.incomplete || evidence.Incomplete || evidence.DroppedObservations > 0 || evidence.DroppedRequestIdentities > 0
	loss := evidence.DroppedObservations
	if old, present := a.sourceDrops[evidence.SourceGeneration]; present || len(a.sourceDrops) < maxArchiveBrowserDocumentMeasurements {
		if loss > old {
			a.dropped = min(int64(1e12), a.dropped+loss-old)
			a.sourceDrops[evidence.SourceGeneration] = loss
		}
	} else {
		a.incomplete = true
	}
	for _, observation := range evidence.Observations {
		key := observation.SourceGeneration + "\x00" + observation.MeasurementID
		raw, _ := json.Marshal(observation)
		digest := sha256.Sum256(raw)
		old, present := a.measurements[key]
		if present {
			if observation.Revision < old.observation.Revision {
				continue
			}
			if old.observation.State != "provisional" || observation.Revision == old.observation.Revision {
				if digest != old.digest {
					a.incomplete = true
				}
				continue
			}
		} else if len(a.measurements) >= maxArchiveBrowserDocumentMeasurements {
			// Without room to retain this identity, retries cannot establish a
			// distinct rejected population. Preserve incomplete coverage only.
			a.incomplete = true
			continue
		}
		a.measurements[key] = archiveBrowserMeasurement{observation: observation, digest: digest}
	}
}
func (a *archiveBrowserDocumentAccumulator) summary() *protocol.BrowserDocumentSummary {
	keys := make([]string, 0, len(a.measurements))
	for key := range a.measurements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	observations := make([]protocol.BrowserDocumentObservation, 0, len(keys))
	for _, key := range keys {
		observations = append(observations, a.measurements[key].observation)
	}
	return protocol.SummarizeBrowserDocumentObservations(observations, a.dropped, a.incomplete)
}
func readArchiveBrowserDocumentSummary(record *storage.RunArchiveRecord, manifest *RunArchiveManifest) (*protocol.BrowserDocumentSummary, []protocol.BrowserDocumentObservation, bool, error) {
	return readArchiveBrowserDocumentSummaryContext(context.Background(), record, manifest)
}

// readArchiveBrowserDocumentSummaryContext is readArchiveBrowserDocumentSummary
// bounded by ctx.
func readArchiveBrowserDocumentSummaryContext(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) (*protocol.BrowserDocumentSummary, []protocol.BrowserDocumentObservation, bool, error) {
	accumulator, captured, err := readArchiveBrowserDocumentMeasurements(ctx, record, manifest)
	if err != nil {
		return nil, nil, false, err
	}
	if accumulator == nil {
		if captured {
			return &protocol.BrowserDocumentSummary{Version: protocol.BrowserEvidenceVersion, Incomplete: true}, nil, false, nil
		}
		return nil, nil, false, nil
	}
	keys := make([]string, 0, len(accumulator.measurements))
	for key := range accumulator.measurements {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	truncated := len(keys) > protocol.MaxBrowserDocumentObservations
	if truncated {
		keys = keys[:protocol.MaxBrowserDocumentObservations]
	}
	observations := make([]protocol.BrowserDocumentObservation, 0, len(keys))
	for _, key := range keys {
		observations = append(observations, accumulator.measurements[key].observation)
	}
	return accumulator.summary(), observations, truncated, nil
}

// readArchiveBrowserDocumentMeasurements reads the latest retained state of
// every document measurement of an archive: each measurement once, however
// many progress revisions it recorded. It is nil when the archive retained no
// document evidence; captured says whether the run requested it.
func readArchiveBrowserDocumentMeasurements(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest) (*archiveBrowserDocumentAccumulator, bool, error) {
	if err := validateBrowserDocumentManifest(manifest, true); err != nil {
		return nil, false, err
	}
	if manifest.BrowserDocumentsFile == "" {
		return nil, manifest.BrowserDocumentCaptureRequested, nil
	}
	accumulator := &archiveBrowserDocumentAccumulator{incomplete: manifest.BrowserDocumentCoverageIncomplete}
	err := forEachBrowserDocumentArtifact(ctx, record.ArchivePath, manifest.BrowserDocumentsFile, manifest.BrowserDocumentsSHA256, manifest.BrowserDocumentRecordCount, func(evidence *protocol.BrowserDocumentEvidence) error { accumulator.add(evidence); return nil })
	if err != nil {
		return nil, true, err
	}
	return accumulator, true, nil
}

func mergeRemoteBrowserDocuments(root string, manifest *RunArchiveManifest, bundles []remoteArchiveBundle, budget *archiveOutputBudget) (err error) {
	qualifyBrowserDocumentSourceCoverage(manifest, bundles)
	if err := validateBrowserDocumentManifest(manifest, false); err != nil {
		return err
	}
	if manifest.BrowserDocumentsFile == "" {
		for _, bundle := range bundles {
			if err := validateBrowserDocumentManifest(&bundle.manifest, true); err != nil {
				return err
			}
			if bundle.manifest.BrowserDocumentsFile != "" || bundle.browserDocumentsPath != "" {
				return errors.New("undeclared merged browser source")
			}
		}
		return nil
	}
	destination, err := createArchiveArtifactExclusive(root, manifest.BrowserDocumentsFile, 0o644, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, destination.Close()) }()
	output := newArchiveMergeOutput(destination, budget)
	encoder := json.NewEncoder(output)
	groups := make(map[string]bool)
	for _, group := range manifest.Provenance.Groups {
		groups[group.Name] = true
	}
	var total int64
	for _, bundle := range bundles {
		if err := validateBrowserDocumentManifest(&bundle.manifest, true); err != nil {
			return err
		}
		if bundle.manifest.BrowserDocumentsFile == "" {
			if bundle.browserDocumentsPath != "" {
				return errors.New("undeclared remote browser source")
			}
			continue
		}
		if bundle.browserDocumentsPath == "" {
			return errors.New("declared remote browser source is missing")
		}
		err := forEachBrowserDocumentArtifact(context.Background(), filepath.Dir(bundle.browserDocumentsPath), filepath.Base(bundle.browserDocumentsPath), bundle.manifest.BrowserDocumentsSHA256, bundle.manifest.BrowserDocumentRecordCount, func(evidence *protocol.BrowserDocumentEvidence) error {
			for i := range evidence.Observations {
				if len(groups) > 0 && !groups[evidence.Observations[i].Group] {
					return errors.New("remote browser evidence has an unassigned group")
				}
				evidence.Observations[i].Region = bundle.region
			}
			if evidence.Clone() == nil {
				return errors.New("invalid controller browser placement")
			}
			if total >= maxArchiveQueryRecords {
				return errors.New("merged browser document record limit exceeded")
			}
			if err := encoder.Encode(evidence); err != nil {
				return err
			}
			total++
			return nil
		})
		if err != nil {
			return fmt.Errorf("merge browser evidence from %q: %w", bundle.target, err)
		}
	}
	if total != manifest.BrowserDocumentRecordCount {
		return fmt.Errorf("merged browser document count is %d, want %d", total, manifest.BrowserDocumentRecordCount)
	}
	if err := output.Flush(); err != nil {
		return err
	}
	return destination.Sync()
}

// Expected browser sources come from frozen controller group/shard admission.
// Collected HTTP or producer-only shards are not missing browser documents.
func qualifyBrowserDocumentSourceCoverage(manifest *RunArchiveManifest, bundles []remoteArchiveBundle) {
	if manifest == nil || !manifest.BrowserDocumentCaptureRequested {
		return
	}
	groups := make(map[string]bool)
	expected := make(map[string]bool)
	add := func(provenance RunArchiveProvenance) {
		for _, group := range provenance.Groups {
			if group.BrowserDocumentCaptureRequested {
				groups[group.Name] = true
			}
		}
		for _, shard := range provenance.GeneratorShards {
			if groups[shard.Group] {
				expected[shard.Target] = true
			}
		}
	}
	add(manifest.Provenance)
	if manifest.controllerInitialProvenance != nil {
		add(*manifest.controllerInitialProvenance)
	}
	if len(expected) == 0 {
		manifest.BrowserDocumentCoverageIncomplete = true
		return
	}
	collected := make(map[string]bool)
	for _, bundle := range bundles {
		if bundle.manifest.BrowserDocumentsFile != "" {
			collected[bundle.target] = true
		}
	}
	for target := range expected {
		if !collected[target] {
			manifest.BrowserDocumentCoverageIncomplete = true
		}
	}
	if manifest.SourceCoverage != nil {
		for _, source := range manifest.SourceCoverage.Sources {
			if expected[source.Target] && !source.Collected {
				manifest.BrowserDocumentCoverageIncomplete = true
			}
		}
	}
}

// requestedBrowserInteractionVersion is controller transport intent, not DSL.
func requestedBrowserInteractionVersion(script *dsl.Script) uint32 {
	if dsl.BrowserInteractionCaptureRequested(script) {
		return protocol.BrowserInteractionVersion2
	}
	return 0
}
