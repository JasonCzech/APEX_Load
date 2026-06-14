package api

// browser — split from archive.go (same package, behavior-preserving).

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func buildRunArchiveBrowserDiagnostics(record *storage.RunArchiveRecord) (*BrowserDiagnosticsBundle, error) {
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, err
	}
	transactions, err := readAllArchiveTransactions(record)
	if err != nil {
		return nil, err
	}
	bundle := &BrowserDiagnosticsBundle{
		SchemaVersion: browserDiagnosticsSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Manifest:      manifest,
		Events:        make([]BrowserDiagnosticsEvent, 0),
	}
	for _, tx := range transactions {
		if !archiveTransactionHasBrowserDiagnostics(tx) {
			continue
		}
		event := BrowserDiagnosticsEvent{
			Index:         len(bundle.Events) + 1,
			Timestamp:     tx.Timestamp,
			Group:         tx.Group,
			ActionName:    tx.ActionName,
			DurationMs:    tx.DurationMs,
			StatusCode:    tx.StatusCode,
			Success:       tx.Success,
			ErrorMessage:  tx.ErrorMessage,
			ClientMetrics: tx.ClientMetrics,
		}
		if tx.ClientMetrics != nil {
			bundle.Overview.ClientMetricSamples++
		}
		if !tx.Success {
			bundle.Overview.FailedBrowserTransactions++
		}
		if snapshot := tx.Snapshot; snapshot != nil {
			event.PageURL = snapshot.PageURL
			event.PageTitle = snapshot.PageTitle
			event.ConsoleEntries = cloneProtocolConsoleEntries(snapshot.ConsoleEntries)
			event.NetworkEntries = cloneProtocolNetworkEntries(snapshot.NetworkEntries)
			event.DOMSummary = cloneProtocolDOMSummary(snapshot.DOMSummary)
			event.Snapshot = &BrowserDiagnosticSnapshot{
				RequestMethod:   snapshot.RequestMethod,
				RequestURL:      snapshot.RequestURL,
				RequestHeaders:  cloneStringMap(snapshot.RequestHeaders),
				RequestBody:     snapshot.RequestBody,
				ResponseStatus:  snapshot.ResponseStatus,
				ResponseHeaders: cloneStringMap(snapshot.ResponseHeaders),
				ResponseBody:    snapshot.ResponseBody,
			}
			event.screenshotBase64 = snapshot.ScreenshotBase64
			event.html = snapshot.HTML
		}
		if event.PageURL == "" {
			event.PageURL = tx.PageURL
		}
		if event.PageTitle == "" {
			event.PageTitle = tx.PageTitle
		}
		if len(event.ConsoleEntries) == 0 {
			event.ConsoleEntries = cloneProtocolConsoleEntries(tx.ConsoleEntries)
		}
		if len(event.NetworkEntries) == 0 {
			event.NetworkEntries = cloneProtocolNetworkEntries(tx.NetworkEntries)
		}
		if event.DOMSummary == nil {
			event.DOMSummary = cloneProtocolDOMSummary(tx.DOMSummary)
		}
		if event.screenshotBase64 != "" {
			bundle.Overview.ScreenshotArtifacts++
		}
		if event.html != "" {
			bundle.Overview.HTMLArtifacts++
		}
		if len(event.ConsoleEntries) > 0 {
			bundle.Overview.ConsoleEntries += int64(len(event.ConsoleEntries))
		}
		if len(event.NetworkEntries) > 0 {
			bundle.Overview.NetworkEntries += int64(len(event.NetworkEntries))
		}
		if event.DOMSummary != nil {
			bundle.Overview.DOMSnapshots++
		}
		bundle.Overview.BrowserTransactions++
		bundle.Events = append(bundle.Events, event)
	}
	return bundle, nil
}

func archiveTransactionHasBrowserDiagnostics(tx TransactionEvent) bool {
	if tx.ClientMetrics != nil {
		return true
	}
	if strings.HasPrefix(strings.ToLower(tx.ActionName), "browser_") {
		return true
	}
	if tx.PageURL != "" ||
		tx.PageTitle != "" ||
		len(tx.ConsoleEntries) > 0 ||
		len(tx.NetworkEntries) > 0 ||
		tx.DOMSummary != nil {
		return true
	}
	if tx.Snapshot == nil {
		return false
	}
	return tx.Snapshot.PageURL != "" ||
		tx.Snapshot.PageTitle != "" ||
		tx.Snapshot.HTML != "" ||
		tx.Snapshot.ScreenshotBase64 != "" ||
		len(tx.Snapshot.ConsoleEntries) > 0 ||
		len(tx.Snapshot.NetworkEntries) > 0 ||
		tx.Snapshot.DOMSummary != nil
}

func browserResourceTimingsFromTransaction(tx TransactionEvent) []BrowserResourceTiming {
	entries := tx.NetworkEntries
	if len(entries) == 0 && tx.Snapshot != nil {
		entries = tx.Snapshot.NetworkEntries
	}
	if len(entries) == 0 {
		return nil
	}
	timings := make([]BrowserResourceTiming, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.URL) == "" {
			continue
		}
		timing := BrowserResourceTiming{
			Timestamp:     tx.Timestamp,
			ActionName:    tx.ActionName,
			PageURL:       tx.PageURL,
			URL:           entry.URL,
			Method:        entry.Method,
			StatusCode:    entry.StatusCode,
			ResourceType:  entry.ResourceType,
			DurationMs:    entry.DurationMs,
			BytesReceived: entry.BytesReceived,
		}
		if timing.PageURL == "" && tx.Snapshot != nil {
			timing.PageURL = tx.Snapshot.PageURL
		}
		if tx.ClientMetrics != nil {
			timing.DeviceProfile = tx.ClientMetrics.DeviceProfile
			timing.DevicePlatform = tx.ClientMetrics.DevicePlatform
			timing.BrowserEngine = tx.ClientMetrics.BrowserEngine
		}
		timings = append(timings, timing)
	}
	return timings
}

func finalizeBrowserResourceTimings(entries []BrowserResourceTiming, limit int) []BrowserResourceTiming {
	if len(entries) == 0 {
		return nil
	}
	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].DurationMs == entries[right].DurationMs {
			return entries[left].URL < entries[right].URL
		}
		return entries[left].DurationMs > entries[right].DurationMs
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func writeRunArchiveBrowserDiagnosticsZip(bundle *BrowserDiagnosticsBundle, dst io.Writer) error {
	zipWriter := zip.NewWriter(dst)
	defer zipWriter.Close()

	for index := range bundle.Events {
		event := &bundle.Events[index]
		baseName := fmt.Sprintf("%03d-%s", event.Index, sanitizeFilename(event.ActionName))
		if event.screenshotBase64 != "" {
			event.Artifacts.ScreenshotFile = filepath.ToSlash(filepath.Join("screenshots", baseName+".b64"))
			if err := writeZipString(zipWriter, event.Artifacts.ScreenshotFile, event.screenshotBase64); err != nil {
				return err
			}
		}
		if event.html != "" {
			event.Artifacts.HTMLFile = filepath.ToSlash(filepath.Join("html", baseName+".html"))
			if err := writeZipString(zipWriter, event.Artifacts.HTMLFile, event.html); err != nil {
				return err
			}
		}
	}
	indexBytes, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	return writeZipBytes(zipWriter, "browser_diagnostics.json", indexBytes)
}

func writeZipString(zipWriter *zip.Writer, name string, value string) error {
	return writeZipBytes(zipWriter, name, []byte(value))
}

func writeZipBytes(zipWriter *zip.Writer, name string, value []byte) error {
	writer, err := zipWriter.Create(name)
	if err != nil {
		return err
	}
	_, err = writer.Write(value)
	return err
}
