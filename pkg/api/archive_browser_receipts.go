package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const browserReceiptsFileName = "browser_receipts.jsonl"

func (w *runArchiveWriter) RecordBrowserDocumentReceiptEvidence(evidence *protocol.BrowserDocumentReceiptEvidence) error {
	if w == nil {
		return errors.New("browser receipt archive unavailable")
	}
	evidence = evidence.Clone()
	if evidence == nil {
		return errors.New("browser receipt evidence malformed")
	}
	data, err := json.Marshal(evidence)
	if err != nil || len(data) > protocol.MaxBrowserDocumentReceiptBytes {
		return errors.New("browser receipt evidence exceeds record bound")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.flushErr != nil {
		return w.flushErr
	}
	if w.txFile == nil {
		return errors.New("browser receipt archive closed")
	}
	if w.manifest.BrowserReceiptRecordCount >= maxArchiveQueryRecords {
		w.flushErr = errors.New("browser receipt record limit exceeded")
		return w.flushErr
	}
	if w.browserReceiptsFile == nil {
		file, err := replaceArchiveArtifactForWrite(w.rootDir, browserReceiptsFileName, 0o644, false)
		if err != nil {
			w.flushErr = err
			return err
		}
		w.browserReceiptsFile = file
		w.manifest.BrowserReceiptsFile = browserReceiptsFileName
	}
	output := archiveCaptureRecordWriter{destination: archiveCaptureFileWriter{file: w.browserReceiptsFile, budget: w.captureBudget}, budget: w.captureBudget}
	record := append(data, '\n')
	written, err := output.Write(record)
	if err == nil && written != len(record) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.flushErr = err
		return err
	}
	w.manifest.BrowserReceiptRecordCount++
	return nil
}

func validateBrowserReceiptManifest(m *RunArchiveManifest, proof bool) error {
	if m == nil {
		return errors.New("browser receipt manifest unavailable")
	}
	if m.BrowserReceiptsFile == "" {
		if m.BrowserReceiptRecordCount != 0 || m.BrowserReceiptsSHA256 != "" {
			return errors.New("browser receipt metadata lacks artifact")
		}
		return nil
	}
	if m.BrowserReceiptsFile != browserReceiptsFileName || m.BrowserReceiptRecordCount <= 0 || m.BrowserReceiptRecordCount > maxArchiveQueryRecords || proof && !validArchiveSHA256(m.BrowserReceiptsSHA256) {
		return errors.New("browser receipt manifest invalid")
	}
	return nil
}

func forEachBrowserReceiptArtifact(ctx context.Context, root, name, digest string, count int64, fn func(*protocol.BrowserDocumentReceiptEvidence) error) (err error) {
	file, _, err := openArchiveArtifactForRead(root, name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	hash := sha256.New()
	scanner := newArchiveJSONLScanner(io.TeeReader(file, hash))
	budget := newArchiveScanBudget(ctx, maxArchiveQueryRecords, maxArchiveQueryBytes)
	var actual int64
	for scanner.Scan() {
		if err = budget.Observe(scanner.Bytes()); err != nil {
			return err
		}
		value, e := protocol.DecodeBrowserDocumentReceiptEvidence(scanner.Bytes())
		if e != nil {
			return e
		}
		if err = fn(value); err != nil {
			return err
		}
		actual++
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	if actual != count || hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("browser receipt artifact count or hash mismatch")
	}
	return ctx.Err()
}

func mergeRemoteBrowserReceipts(root string, m *RunArchiveManifest, bundles []remoteArchiveBundle, budget *archiveOutputBudget) (err error) {
	if err = validateBrowserReceiptManifest(m, false); err != nil {
		return err
	}
	if m.BrowserReceiptsFile == "" {
		for _, bundle := range bundles {
			if err := validateBrowserReceiptManifest(&bundle.manifest, true); err != nil {
				return err
			}
			if bundle.manifest.BrowserReceiptsFile != "" || bundle.browserReceiptsPath != "" {
				return errors.New("undeclared merged receipt source")
			}
		}
		return nil
	}
	file, err := createArchiveArtifactExclusive(root, m.BrowserReceiptsFile, 0o644, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	output := newArchiveMergeOutput(file, budget)
	encoder := json.NewEncoder(output)
	var count int64
	for _, bundle := range bundles {
		if err = validateBrowserReceiptManifest(&bundle.manifest, true); err != nil {
			return err
		}
		if bundle.manifest.BrowserReceiptsFile == "" {
			if bundle.browserReceiptsPath != "" {
				return errors.New("undeclared browser receipt source")
			}
			continue
		}
		if bundle.browserReceiptsPath == "" {
			return errors.New("missing browser receipt source")
		}
		err = forEachBrowserReceiptArtifact(context.Background(), filepath.Dir(bundle.browserReceiptsPath), filepath.Base(bundle.browserReceiptsPath), bundle.manifest.BrowserReceiptsSHA256, bundle.manifest.BrowserReceiptRecordCount, func(e *protocol.BrowserDocumentReceiptEvidence) error {
			if err := encoder.Encode(e); err != nil {
				return err
			}
			count++
			return nil
		})
		if err != nil {
			return err
		}
	}
	if count != m.BrowserReceiptRecordCount {
		return errors.New("merged browser receipt count mismatch")
	}
	if err := output.Flush(); err != nil {
		return err
	}
	return file.Sync()
}

func (w *runArchiveWriter) MarkBrowserDocumentReceiptIncomplete() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.manifest.BrowserReceiptIncomplete = true
}
