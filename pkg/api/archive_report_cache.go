package api

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/storage"
)

var runArchiveReportCache sync.Map   // map[string]*RunArchiveReport
var runArchiveManifestCache sync.Map // map[string]*RunArchiveManifest

func cachedRunArchiveReport(record *storage.RunArchiveRecord) (*RunArchiveReport, error) {
	key := runArchiveReportCacheKey(record)
	if key != "" {
		if cached, ok := runArchiveReportCache.Load(key); ok {
			return cloneRunArchiveReport(cached.(*RunArchiveReport))
		}
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return report, nil
	}
	cacheCopy, err := cloneRunArchiveReport(report)
	if err != nil {
		return nil, err
	}
	runArchiveReportCache.Store(key, cacheCopy)
	return report, nil
}

func runArchiveReportCacheKey(record *storage.RunArchiveRecord) string {
	if record == nil {
		return ""
	}
	parts := []string{
		strings.TrimSpace(record.TestID),
		strings.TrimSpace(record.ManifestPath),
		strings.TrimSpace(record.ManifestSHA256),
		strconv.FormatInt(record.CompletedAt.UnixNano(), 10),
		strconv.FormatInt(record.TransactionCount, 10),
		strconv.FormatInt(record.ResourceSampleCount, 10),
		strconv.FormatInt(record.IntervalSummaryCount, 10),
	}
	return strings.Join(parts, "|")
}

func cloneRunArchiveReport(report *RunArchiveReport) (*RunArchiveReport, error) {
	if report == nil {
		return nil, nil
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	var cloned RunArchiveReport
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}

func cachedRunArchiveManifest(record *storage.RunArchiveRecord) (*RunArchiveManifest, error) {
	if err := ensureArchiveMaterialized(record); err != nil {
		return nil, err
	}
	key := runArchiveReportCacheKey(record)
	if key != "" {
		if cached, ok := runArchiveManifestCache.Load(key); ok {
			return cloneRunArchiveManifest(cached.(*RunArchiveManifest))
		}
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return manifest, nil
	}
	cacheCopy, err := cloneRunArchiveManifest(manifest)
	if err != nil {
		return nil, err
	}
	runArchiveManifestCache.Store(key, cacheCopy)
	return manifest, nil
}

func cloneRunArchiveManifest(manifest *RunArchiveManifest) (*RunArchiveManifest, error) {
	if manifest == nil {
		return nil, nil
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	var cloned RunArchiveManifest
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}
