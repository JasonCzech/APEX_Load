package api

import (
	"encoding/csv"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type UsageAccountingResponse struct {
	GeneratedAt time.Time               `json:"generated_at"`
	ProjectID   string                  `json:"project_id,omitempty"`
	Totals      UsageAccountingBucket   `json:"totals"`
	ByProject   []UsageAccountingBucket `json:"by_project,omitempty"`
	ByProtocol  []UsageAccountingBucket `json:"by_protocol,omitempty"`
	ByRegion    []UsageAccountingBucket `json:"by_region,omitempty"`
	Notes       []string                `json:"notes,omitempty"`
}

type UsageAccountingBucket struct {
	Key             string         `json:"key,omitempty"`
	RunCount        int            `json:"run_count"`
	GroupCount      int            `json:"group_count,omitempty"`
	TotalVUsers     int64          `json:"total_vusers"`
	DurationSeconds int64          `json:"duration_seconds"`
	VUserSeconds    int64          `json:"vuser_seconds"`
	ArchiveBytes    int64          `json:"archive_bytes,omitempty"`
	StatusCounts    map[string]int `json:"status_counts,omitempty"`
}

func (s *Server) handleGetUsageAccounting(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	writeJSON(w, http.StatusOK, s.usageAccounting(projectID))
}

func (s *Server) handleExportUsageAccounting(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	usage := s.usageAccounting(projectID)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="stormrunner-usage-accounting.csv"`)

	writer := csv.NewWriter(w)
	_ = writer.Write([]string{
		"generated_at",
		"project_id",
		"scope",
		"key",
		"run_count",
		"group_count",
		"total_vusers",
		"duration_seconds",
		"vuser_seconds",
		"archive_bytes",
		"status_counts",
	})
	for _, row := range usageAccountingCSVRows(usage) {
		_ = writer.Write(row)
	}
	writer.Flush()
}

func (s *Server) usageAccounting(projectID string) UsageAccountingResponse {
	tests := s.runner.ListTests()
	byProject := map[string]*UsageAccountingBucket{}
	byProtocol := map[string]*UsageAccountingBucket{}
	byRegion := map[string]*UsageAccountingBucket{}
	totals := &UsageAccountingBucket{}

	for _, test := range tests {
		if test == nil {
			continue
		}
		testProjectID := normalizeProjectID(test.ProjectID)
		if projectID != "" && testProjectID != projectID {
			continue
		}
		durationSeconds := usageDurationSeconds(test.Duration)
		vuserSeconds := int64(test.TotalVUsers) * durationSeconds
		archiveBytes := usageArchiveBytes(test)
		status := strings.TrimSpace(test.Status)

		addRunUsage(totals, int64(test.TotalVUsers), durationSeconds, vuserSeconds, archiveBytes, status)
		addRunUsage(usageBucket(byProject, testProjectID), int64(test.TotalVUsers), durationSeconds, vuserSeconds, archiveBytes, status)

		seenProtocols := map[string]struct{}{}
		for _, group := range test.Groups {
			protocol := usageKey(group.Protocol, "unknown")
			groupVUserSeconds := int64(group.VUsers) * durationSeconds
			addGroupUsage(usageBucket(byProtocol, protocol), int64(group.VUsers), durationSeconds, groupVUserSeconds, status)
			seenProtocols[protocol] = struct{}{}
		}
		for protocol := range seenProtocols {
			usageBucket(byProtocol, protocol).RunCount++
		}

		regions := usageRegionsForTest(test)
		for _, region := range regions {
			bucket := usageBucket(byRegion, region)
			addDistributedRunUsage(bucket, int64(test.TotalVUsers), durationSeconds, vuserSeconds, status, len(regions))
		}
	}

	response := UsageAccountingResponse{
		GeneratedAt: time.Now().UTC(),
		ProjectID:   projectID,
		Totals:      *totals,
		ByProject:   usageSortedBuckets(byProject),
		ByProtocol:  usageSortedBuckets(byProtocol),
		ByRegion:    usageSortedBuckets(byRegion),
		Notes: []string{
			"Usage is derived from persisted and in-memory run metadata.",
			"Region allocation uses requested assignment metadata; exact generator placement accounting will be refined with the formal fleet model.",
		},
	}
	if projectID != "" {
		response.ByProject = nil
	}
	return response
}

func usageAccountingCSVRows(usage UsageAccountingResponse) [][]string {
	generatedAt := usage.GeneratedAt.Format(time.RFC3339)
	rows := [][]string{usageAccountingCSVRow(generatedAt, usage.ProjectID, "totals", "", usage.Totals)}
	for _, bucket := range usage.ByProject {
		rows = append(rows, usageAccountingCSVRow(generatedAt, usage.ProjectID, "project", bucket.Key, bucket))
	}
	for _, bucket := range usage.ByProtocol {
		rows = append(rows, usageAccountingCSVRow(generatedAt, usage.ProjectID, "protocol", bucket.Key, bucket))
	}
	for _, bucket := range usage.ByRegion {
		rows = append(rows, usageAccountingCSVRow(generatedAt, usage.ProjectID, "region", bucket.Key, bucket))
	}
	return rows
}

func usageAccountingCSVRow(generatedAt string, projectID string, scope string, key string, bucket UsageAccountingBucket) []string {
	return []string{
		generatedAt,
		projectID,
		scope,
		key,
		intToString(bucket.RunCount),
		intToString(bucket.GroupCount),
		int64ToString(bucket.TotalVUsers),
		int64ToString(bucket.DurationSeconds),
		int64ToString(bucket.VUserSeconds),
		int64ToString(bucket.ArchiveBytes),
		formatUsageStatusCounts(bucket.StatusCounts),
	}
}

func formatUsageStatusCounts(statusCounts map[string]int) string {
	if len(statusCounts) == 0 {
		return ""
	}
	statuses := make([]string, 0, len(statusCounts))
	for status := range statusCounts {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		parts = append(parts, status+"="+intToString(statusCounts[status]))
	}
	return strings.Join(parts, ";")
}

func intToString(value int) string {
	return strconv.Itoa(value)
}

func int64ToString(value int64) string {
	return strconv.FormatInt(value, 10)
}

func usageBucket(buckets map[string]*UsageAccountingBucket, key string) *UsageAccountingBucket {
	key = usageKey(key, "unassigned")
	if bucket, ok := buckets[key]; ok {
		return bucket
	}
	bucket := &UsageAccountingBucket{Key: key}
	buckets[key] = bucket
	return bucket
}

func addRunUsage(bucket *UsageAccountingBucket, totalVUsers int64, durationSeconds int64, vuserSeconds int64, archiveBytes int64, status string) {
	bucket.RunCount++
	bucket.TotalVUsers += totalVUsers
	bucket.DurationSeconds += durationSeconds
	bucket.VUserSeconds += vuserSeconds
	bucket.ArchiveBytes += archiveBytes
	incrementUsageStatus(bucket, status)
}

func addGroupUsage(bucket *UsageAccountingBucket, totalVUsers int64, durationSeconds int64, vuserSeconds int64, status string) {
	bucket.GroupCount++
	bucket.TotalVUsers += totalVUsers
	bucket.DurationSeconds += durationSeconds
	bucket.VUserSeconds += vuserSeconds
	incrementUsageStatus(bucket, status)
}

func addDistributedRunUsage(bucket *UsageAccountingBucket, totalVUsers int64, durationSeconds int64, vuserSeconds int64, status string, divisor int) {
	if divisor <= 0 {
		divisor = 1
	}
	bucket.RunCount++
	bucket.TotalVUsers += divideUsage(totalVUsers, divisor)
	bucket.DurationSeconds += durationSeconds
	bucket.VUserSeconds += divideUsage(vuserSeconds, divisor)
	incrementUsageStatus(bucket, status)
}

func incrementUsageStatus(bucket *UsageAccountingBucket, status string) {
	status = usageKey(status, "UNKNOWN")
	if bucket.StatusCounts == nil {
		bucket.StatusCounts = map[string]int{}
	}
	bucket.StatusCounts[status]++
}

func usageDurationSeconds(duration time.Duration) int64 {
	seconds := int64(duration.Seconds())
	if seconds < 0 {
		return 0
	}
	return seconds
}

func usageArchiveBytes(test *ManagedTest) int64 {
	if test == nil || test.Archive == nil || strings.TrimSpace(test.Archive.ArchivePath) == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(test.Archive.ArchivePath, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

func usageRegionsForTest(test *ManagedTest) []string {
	if test == nil || test.Assignment == nil {
		return []string{"unassigned"}
	}
	regions := map[string]struct{}{}
	for region := range test.Assignment.RegionWeights {
		regions[usageKey(region, "unassigned")] = struct{}{}
	}
	for _, target := range test.Assignment.SelectedTargets {
		spec := parseGeneratorTargetSpec(target)
		if spec.Region != "" {
			regions[usageKey(spec.Region, "unassigned")] = struct{}{}
		}
	}
	if len(regions) == 0 {
		return []string{"unassigned"}
	}
	out := make([]string, 0, len(regions))
	for region := range regions {
		out = append(out, region)
	}
	sort.Strings(out)
	return out
}

func usageSortedBuckets(values map[string]*UsageAccountingBucket) []UsageAccountingBucket {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]UsageAccountingBucket, 0, len(keys))
	for _, key := range keys {
		out = append(out, *values[key])
	}
	return out
}

func usageKey(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func divideUsage(value int64, divisor int) int64 {
	if divisor <= 1 {
		return value
	}
	return value / int64(divisor)
}
