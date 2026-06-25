package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type UsageAccountingResponse struct {
	GeneratedAt time.Time               `json:"generated_at"`
	ProjectID   string                  `json:"project_id,omitempty"`
	Totals      UsageAccountingBucket   `json:"totals"`
	ByProject   []UsageAccountingBucket `json:"by_project,omitempty"`
	ByProtocol  []UsageAccountingBucket `json:"by_protocol,omitempty"`
	ByRegion    []UsageAccountingBucket `json:"by_region,omitempty"`
	ByUser      []UsageAccountingBucket `json:"by_user,omitempty"`
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

type UsageAccountingSnapshotsResponse struct {
	Snapshots []UsageAccountingSnapshotView `json:"snapshots"`
	Count     int                           `json:"count"`
	Limit     int                           `json:"limit"`
	Offset    int                           `json:"offset"`
}

type UsageAccountingSnapshotView struct {
	ID          string                  `json:"id"`
	ProjectID   string                  `json:"project_id,omitempty"`
	GeneratedAt time.Time               `json:"generated_at"`
	CreatedAt   time.Time               `json:"created_at"`
	Usage       UsageAccountingResponse `json:"usage"`
}

func (s *Server) handleGetUsageAccounting(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	writeJSON(w, http.StatusOK, s.usageAccounting(projectID))
}

func (s *Server) handleListUsageAccountingSnapshots(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "metadata store is not configured")
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	limit := usageSnapshotQueryInt(r, "limit", 100, 500)
	offset := usageSnapshotQueryInt(r, "offset", 0, 0)
	snapshots, err := s.store.ListUsageAccountingSnapshots(projectID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list usage snapshots: "+err.Error())
		return
	}
	views, err := usageAccountingSnapshotViews(snapshots)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decode usage snapshot: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, UsageAccountingSnapshotsResponse{
		Snapshots: views,
		Count:     len(views),
		Limit:     limit,
		Offset:    offset,
	})
}

func (s *Server) handleCreateUsageAccountingSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "metadata store is not configured")
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	usage := s.usageAccounting(projectID)
	payload, err := json.Marshal(usage)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode usage snapshot: "+err.Error())
		return
	}
	now := time.Now().UTC()
	snapshot := &storage.UsageAccountingSnapshot{
		ID:          uuid.NewString(),
		ProjectID:   projectID,
		GeneratedAt: usage.GeneratedAt,
		PayloadJSON: string(payload),
		CreatedAt:   now,
	}
	if err := s.store.SaveUsageAccountingSnapshot(snapshot); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save usage snapshot: "+err.Error())
		return
	}
	view, err := usageAccountingSnapshotView(snapshot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decode usage snapshot: "+err.Error())
		return
	}
	s.recordAuditEvent(r, "usage.snapshot.capture", "success", "usage_accounting_snapshot", snapshot.ID, projectID, map[string]any{
		"run_count":        usage.Totals.RunCount,
		"vuser_seconds":    usage.Totals.VUserSeconds,
		"duration_seconds": usage.Totals.DurationSeconds,
	})
	writeJSON(w, http.StatusCreated, view)
}

func (s *Server) handleExportUsageAccounting(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID != "" {
		projectID = normalizeProjectID(projectID)
	}
	usage := s.usageAccounting(projectID)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="apexload-usage-accounting.csv"`)

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

func usageAccountingSnapshotViews(snapshots []*storage.UsageAccountingSnapshot) ([]UsageAccountingSnapshotView, error) {
	views := make([]UsageAccountingSnapshotView, 0, len(snapshots))
	for _, snapshot := range snapshots {
		view, err := usageAccountingSnapshotView(snapshot)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func usageAccountingSnapshotView(snapshot *storage.UsageAccountingSnapshot) (UsageAccountingSnapshotView, error) {
	if snapshot == nil {
		return UsageAccountingSnapshotView{}, nil
	}
	var usage UsageAccountingResponse
	if err := json.Unmarshal([]byte(snapshot.PayloadJSON), &usage); err != nil {
		return UsageAccountingSnapshotView{}, err
	}
	return UsageAccountingSnapshotView{
		ID:          snapshot.ID,
		ProjectID:   snapshot.ProjectID,
		GeneratedAt: snapshot.GeneratedAt,
		CreatedAt:   snapshot.CreatedAt,
		Usage:       usage,
	}, nil
}

func usageSnapshotQueryInt(r *http.Request, key string, fallback int, max int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	if max > 0 && value > max {
		return max
	}
	return value
}

func (s *Server) usageAccounting(projectID string) UsageAccountingResponse {
	return usageAccountingFromTests(projectID, s.runner.ListTests())
}

// scopedUsageAccounting returns usage limited to the caller's authorization scope.
// Global callers (all=true) see everything; a scoped caller who pinned an in-scope
// project_id sees that project; a scoped caller with no project sees the aggregate over
// only their administered projects — never the whole tenant.
func (s *Server) scopedUsageAccounting(projectID string, scope map[string]struct{}, all bool) UsageAccountingResponse {
	if all || projectID != "" {
		return s.usageAccounting(projectID)
	}
	return usageAccountingFromTestsScoped(scope, s.runner.ListTests())
}

func usageAccountingFromTests(projectID string, tests []*ManagedTest) UsageAccountingResponse {
	byProject := map[string]*UsageAccountingBucket{}
	byProtocol := map[string]*UsageAccountingBucket{}
	byRegion := map[string]*UsageAccountingBucket{}
	byUser := map[string]*UsageAccountingBucket{}
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
		addRunUsage(usageBucket(byUser, usageActorKey(test)), int64(test.TotalVUsers), durationSeconds, vuserSeconds, archiveBytes, status)

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
		ByUser:      usageSortedBuckets(byUser),
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

// usageAccountingFromTestsScoped aggregates usage across only the projects in scope
// (used for non-global admins reading user activity without a specific project_id, so
// they never see cross-tenant usage). An empty scope yields empty usage, not all of it.
func usageAccountingFromTestsScoped(scope map[string]struct{}, tests []*ManagedTest) UsageAccountingResponse {
	filtered := make([]*ManagedTest, 0, len(tests))
	for _, test := range tests {
		if test == nil {
			continue
		}
		if _, ok := scope[normalizeProjectID(test.ProjectID)]; ok {
			filtered = append(filtered, test)
		}
	}
	return usageAccountingFromTests("", filtered)
}

func usageAccountingForActor(actorSubject string, base UsageAccountingResponse, tests []*ManagedTest) UsageAccountingResponse {
	projectID := base.ProjectID
	filtered := []*ManagedTest{}
	for _, test := range tests {
		if test == nil {
			continue
		}
		if projectID != "" && normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		if usageActorKey(test) == actorSubject {
			filtered = append(filtered, test)
		}
	}
	return usageAccountingFromTests(projectID, filtered)
}

func usageActorKey(test *ManagedTest) string {
	if test == nil {
		return "unattributed"
	}
	if strings.TrimSpace(test.StartedBySubject) != "" {
		return strings.TrimSpace(test.StartedBySubject)
	}
	if strings.TrimSpace(test.CreatedBySubject) != "" {
		return strings.TrimSpace(test.CreatedBySubject)
	}
	return "unattributed"
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
	for _, bucket := range usage.ByUser {
		rows = append(rows, usageAccountingCSVRow(generatedAt, usage.ProjectID, "user", bucket.Key, bucket))
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
