package api

// browser — split from archive.go (same package, behavior-preserving).

import (
	"archive/zip"
	"context"
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
	lease, err := acquireArchiveMaterialized(context.Background(), record)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, err
	}
	bundle := &BrowserDiagnosticsBundle{
		SchemaVersion: browserDiagnosticsSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Manifest:      manifest,
		Events:        make([]BrowserDiagnosticsEvent, 0),
	}
	const maxBrowserDiagnosticEvents = 5_000
	const maxBrowserDiagnosticPayloadBytes = 128 << 20
	payloadBytes := 0
	if err := forEachArchiveTransaction(record, func(tx *TransactionEvent) error {
		if tx == nil {
			return nil
		}
		if !archiveTransactionHasBrowserDiagnostics(*tx) {
			return nil
		}
		if len(bundle.Events) >= maxBrowserDiagnosticEvents {
			return fmt.Errorf("browser diagnostics exceed bounded event limit of %d", maxBrowserDiagnosticEvents)
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
		payloadBytes += browserDiagnosticEventPayloadBytes(&event)
		if payloadBytes > maxBrowserDiagnosticPayloadBytes {
			return fmt.Errorf("browser diagnostics exceed bounded payload limit of %d bytes", maxBrowserDiagnosticPayloadBytes)
		}
		bundle.Events = append(bundle.Events, event)
		return nil
	}); err != nil {
		return nil, err
	}
	return bundle, nil
}

func browserDiagnosticEventPayloadBytes(event *BrowserDiagnosticsEvent) int {
	if event == nil {
		return 0
	}
	size := len(event.ErrorMessage) + len(event.PageURL) + len(event.PageTitle) + len(event.screenshotBase64) + len(event.html)
	if event.Snapshot != nil {
		size += len(event.Snapshot.RequestURL) + len(event.Snapshot.RequestBody) + len(event.Snapshot.ResponseBody)
		for key, value := range event.Snapshot.RequestHeaders {
			size += len(key) + len(value)
		}
		for key, value := range event.Snapshot.ResponseHeaders {
			size += len(key) + len(value)
		}
	}
	for _, entry := range event.ConsoleEntries {
		size += len(entry.Level) + len(entry.Text) + len(entry.Source)
	}
	for _, entry := range event.NetworkEntries {
		size += len(entry.URL) + len(entry.Method) + len(entry.ResourceType) + len(entry.RequestBody)
		for key, value := range entry.RequestHeaders {
			size += len(key) + len(value)
		}
		for key, value := range entry.ResponseHeaders {
			size += len(key) + len(value)
		}
	}
	return size
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

func appendTopBrowserResourceTimingsFromTransaction(current []BrowserResourceTiming, tx TransactionEvent, limit int) []BrowserResourceTiming {
	entries := tx.NetworkEntries
	if len(entries) == 0 && tx.Snapshot != nil {
		entries = tx.Snapshot.NetworkEntries
	}
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
		current = insertTopBrowserResourceTiming(current, timing, limit)
	}
	return current
}

func insertTopBrowserResourceTiming(entries []BrowserResourceTiming, timing BrowserResourceTiming, limit int) []BrowserResourceTiming {
	if limit <= 0 {
		return append(entries, timing)
	}
	insertAt := sort.Search(len(entries), func(index int) bool {
		return browserResourceTimingRanksBefore(timing, entries[index])
	})
	if insertAt >= limit {
		return entries
	}
	entries = append(entries, BrowserResourceTiming{})
	copy(entries[insertAt+1:], entries[insertAt:])
	entries[insertAt] = timing
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func finalizeBrowserResourceTimings(entries []BrowserResourceTiming, limit int) []BrowserResourceTiming {
	if len(entries) == 0 {
		return nil
	}
	sort.SliceStable(entries, func(left, right int) bool {
		return browserResourceTimingRanksBefore(entries[left], entries[right])
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func browserResourceTimingRanksBefore(left, right BrowserResourceTiming) bool {
	if left.DurationMs == right.DurationMs {
		return left.URL < right.URL
	}
	return left.DurationMs > right.DurationMs
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
