package api

import (
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const runArchiveAnalysisSchemaVersion = "apexload.archive_analysis.v1"

type RunArchiveAnalysisResponse struct {
	SchemaVersion string                           `json:"schema_version"`
	TestID        string                           `json:"test_id"`
	BaselineID    string                           `json:"baseline_id,omitempty"`
	Graph         RunArchiveAnalysisGraph          `json:"graph"`
	Transactions  []RunArchiveTransactionDrilldown `json:"transactions"`
	Count         int                              `json:"count"`
	Total         int                              `json:"total"`
	Limit         int                              `json:"limit"`
	Offset        int                              `json:"offset"`
	Filters       map[string]any                   `json:"filters,omitempty"`
}

type RunArchiveAnalysisGraph struct {
	Nodes []RunArchiveAnalysisNode `json:"nodes"`
	Edges []RunArchiveAnalysisEdge `json:"edges"`
}

type RunArchiveAnalysisNode struct {
	ID    string                    `json:"id"`
	Kind  string                    `json:"kind"`
	Label string                    `json:"label"`
	Runs  []RunArchiveAnalysisStats `json:"runs"`
}

type RunArchiveAnalysisEdge struct {
	ID   string                    `json:"id"`
	From string                    `json:"from"`
	To   string                    `json:"to"`
	Kind string                    `json:"kind"`
	Runs []RunArchiveAnalysisStats `json:"runs"`
}

type RunArchiveAnalysisStats struct {
	RunID         string  `json:"run_id"`
	RunName       string  `json:"run_name,omitempty"`
	Transactions  int64   `json:"transactions"`
	Errors        int64   `json:"errors"`
	ErrorPct      float64 `json:"error_pct"`
	AvgResponseMs float64 `json:"avg_response_ms"`
	MinResponseMs float64 `json:"min_response_ms,omitempty"`
	MaxResponseMs float64 `json:"max_response_ms,omitempty"`
	P95Ms         float64 `json:"p95_ms,omitempty"`
	P99Ms         float64 `json:"p99_ms,omitempty"`
	BytesReceived int64   `json:"bytes_received,omitempty"`
}

type RunArchiveTransactionDrilldown struct {
	Index             int       `json:"index"`
	Timestamp         time.Time `json:"timestamp"`
	RunID             string    `json:"run_id"`
	Group             string    `json:"group,omitempty"`
	Region            string    `json:"region,omitempty"`
	ActionName        string    `json:"action_name,omitempty"`
	DependencyType    string    `json:"dependency_type,omitempty"`
	DependencyTarget  string    `json:"dependency_target,omitempty"`
	DurationMs        float64   `json:"duration_ms"`
	StatusCode        int       `json:"status_code,omitempty"`
	Success           bool      `json:"success"`
	ErrorMessage      string    `json:"error_message,omitempty"`
	BytesReceived     int64     `json:"bytes_received,omitempty"`
	TraceID           string    `json:"trace_id,omitempty"`
	CorrelationID     string    `json:"correlation_id,omitempty"`
	SourceTransaction string    `json:"source_transaction,omitempty"`
	SourceAction      string    `json:"source_action,omitempty"`
	Path              []string  `json:"path,omitempty"`
}

type archiveAnalysisRun struct {
	ID           string
	Name         string
	Transactions []TransactionEvent
}

type archiveAnalysisStatsAccumulator struct {
	RunID         string
	RunName       string
	Transactions  int64
	Errors        int64
	TotalDuration float64
	MinResponseMs float64
	MaxResponseMs float64
	BytesReceived int64
	Durations     []float64
}

func buildRunArchiveAnalysis(current archiveAnalysisRun, baseline *archiveAnalysisRun, filter transactionEventFilter, page archivePage) RunArchiveAnalysisResponse {
	runs := []archiveAnalysisRun{current}
	if baseline != nil {
		runs = append([]archiveAnalysisRun{*baseline}, runs...)
	}
	nodes, edges := buildRunArchiveAnalysisGraph(runs)
	paged := pageArchiveItems(current.Transactions, page)
	drilldown := make([]RunArchiveTransactionDrilldown, 0, len(paged))
	for index, tx := range paged {
		drilldown = append(drilldown, archiveTransactionDrilldown(current.ID, page.Offset+index, tx))
	}
	response := RunArchiveAnalysisResponse{
		SchemaVersion: runArchiveAnalysisSchemaVersion,
		TestID:        current.ID,
		Graph:         RunArchiveAnalysisGraph{Nodes: nodes, Edges: edges},
		Transactions:  drilldown,
		Count:         len(drilldown),
		Total:         len(current.Transactions),
		Limit:         page.Limit,
		Offset:        page.Offset,
		Filters:       archiveAnalysisFilterSummary(filter),
	}
	if baseline != nil {
		response.BaselineID = baseline.ID
	}
	return response
}

func buildRunArchiveAnalysisGraph(runs []archiveAnalysisRun) ([]RunArchiveAnalysisNode, []RunArchiveAnalysisEdge) {
	nodeAcc := map[string]map[string]*archiveAnalysisStatsAccumulator{}
	edgeAcc := map[string]map[string]*archiveAnalysisStatsAccumulator{}
	nodeMeta := map[string]RunArchiveAnalysisNode{}
	edgeMeta := map[string]RunArchiveAnalysisEdge{}

	for _, run := range runs {
		for _, tx := range run.Transactions {
			for _, node := range archiveAnalysisNodesForTransaction(tx) {
				mergeArchiveAnalysisNode(nodeMeta, nodeAcc, node, run, tx)
			}
			for _, edge := range archiveAnalysisEdgesForTransaction(tx) {
				mergeArchiveAnalysisEdge(edgeMeta, edgeAcc, edge, run, tx)
			}
		}
	}

	nodeIDs := make([]string, 0, len(nodeMeta))
	for id := range nodeMeta {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	nodes := make([]RunArchiveAnalysisNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		node := nodeMeta[id]
		node.Runs = finalizeArchiveAnalysisRuns(nodeAcc[id])
		nodes = append(nodes, node)
	}

	edgeIDs := make([]string, 0, len(edgeMeta))
	for id := range edgeMeta {
		edgeIDs = append(edgeIDs, id)
	}
	sort.Strings(edgeIDs)
	edges := make([]RunArchiveAnalysisEdge, 0, len(edgeIDs))
	for _, id := range edgeIDs {
		edge := edgeMeta[id]
		edge.Runs = finalizeArchiveAnalysisRuns(edgeAcc[id])
		edges = append(edges, edge)
	}
	return nodes, edges
}

func archiveAnalysisNodesForTransaction(tx TransactionEvent) []RunArchiveAnalysisNode {
	nodes := make([]RunArchiveAnalysisNode, 0, 5)
	if tx.Region != "" {
		nodes = append(nodes, archiveAnalysisNode("region", tx.Region))
	}
	if tx.Group != "" {
		nodes = append(nodes, archiveAnalysisNode("group", tx.Group))
	}
	if tx.ActionName != "" {
		nodes = append(nodes, archiveAnalysisNode("action", tx.ActionName))
	}
	if tx.DependencyTarget != "" {
		label := tx.DependencyTarget
		if tx.DependencyType != "" {
			label = tx.DependencyType + ":" + tx.DependencyTarget
		}
		nodes = append(nodes, archiveAnalysisNode("dependency", label))
	}
	if tx.SourceTransaction != "" {
		nodes = append(nodes, archiveAnalysisNode("source_transaction", tx.SourceTransaction))
	}
	return nodes
}

func archiveAnalysisEdgesForTransaction(tx TransactionEvent) []RunArchiveAnalysisEdge {
	edges := make([]RunArchiveAnalysisEdge, 0, 4)
	actionID := archiveAnalysisNodeID("action", tx.ActionName)
	if tx.Region != "" && tx.ActionName != "" {
		edges = append(edges, archiveAnalysisEdge("region_action", archiveAnalysisNodeID("region", tx.Region), actionID))
	}
	if tx.Group != "" && tx.ActionName != "" {
		edges = append(edges, archiveAnalysisEdge("group_action", archiveAnalysisNodeID("group", tx.Group), actionID))
	}
	if tx.ActionName != "" && tx.DependencyTarget != "" {
		label := tx.DependencyTarget
		if tx.DependencyType != "" {
			label = tx.DependencyType + ":" + tx.DependencyTarget
		}
		edges = append(edges, archiveAnalysisEdge("action_dependency", actionID, archiveAnalysisNodeID("dependency", label)))
	}
	if tx.SourceTransaction != "" && tx.ActionName != "" {
		edges = append(edges, archiveAnalysisEdge("source_transaction_action", archiveAnalysisNodeID("source_transaction", tx.SourceTransaction), actionID))
	}
	return edges
}

func archiveAnalysisNode(kind string, label string) RunArchiveAnalysisNode {
	return RunArchiveAnalysisNode{ID: archiveAnalysisNodeID(kind, label), Kind: kind, Label: label}
}

func archiveAnalysisEdge(kind string, from string, to string) RunArchiveAnalysisEdge {
	return RunArchiveAnalysisEdge{ID: kind + ":" + from + "->" + to, Kind: kind, From: from, To: to}
}

func archiveAnalysisNodeID(kind string, label string) string {
	return kind + ":" + strings.ToLower(SlugForArchiveAnalysis(label))
}

func SlugForArchiveAnalysis(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return archiveErrorWhitespacePattern.ReplaceAllString(value, "_")
}

func mergeArchiveAnalysisNode(meta map[string]RunArchiveAnalysisNode, acc map[string]map[string]*archiveAnalysisStatsAccumulator, node RunArchiveAnalysisNode, run archiveAnalysisRun, tx TransactionEvent) {
	meta[node.ID] = node
	mergeArchiveAnalysisAccumulator(acc, node.ID, run, tx)
}

func mergeArchiveAnalysisEdge(meta map[string]RunArchiveAnalysisEdge, acc map[string]map[string]*archiveAnalysisStatsAccumulator, edge RunArchiveAnalysisEdge, run archiveAnalysisRun, tx TransactionEvent) {
	meta[edge.ID] = edge
	mergeArchiveAnalysisAccumulator(acc, edge.ID, run, tx)
}

func mergeArchiveAnalysisAccumulator(byKey map[string]map[string]*archiveAnalysisStatsAccumulator, key string, run archiveAnalysisRun, tx TransactionEvent) {
	byRun := byKey[key]
	if byRun == nil {
		byRun = map[string]*archiveAnalysisStatsAccumulator{}
		byKey[key] = byRun
	}
	acc := byRun[run.ID]
	if acc == nil {
		acc = &archiveAnalysisStatsAccumulator{RunID: run.ID, RunName: run.Name}
		byRun[run.ID] = acc
	}
	acc.Transactions++
	if !tx.Success {
		acc.Errors++
	}
	acc.TotalDuration += tx.DurationMs
	if acc.Transactions == 1 || tx.DurationMs < acc.MinResponseMs {
		acc.MinResponseMs = tx.DurationMs
	}
	if tx.DurationMs > acc.MaxResponseMs {
		acc.MaxResponseMs = tx.DurationMs
	}
	acc.BytesReceived += tx.BytesReceived
	acc.Durations = append(acc.Durations, tx.DurationMs)
}

func finalizeArchiveAnalysisRuns(byRun map[string]*archiveAnalysisStatsAccumulator) []RunArchiveAnalysisStats {
	runIDs := make([]string, 0, len(byRun))
	for runID := range byRun {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)
	stats := make([]RunArchiveAnalysisStats, 0, len(runIDs))
	for _, runID := range runIDs {
		acc := byRun[runID]
		stats = append(stats, finalizeArchiveAnalysisStats(acc))
	}
	return stats
}

func finalizeArchiveAnalysisStats(acc *archiveAnalysisStatsAccumulator) RunArchiveAnalysisStats {
	stats := RunArchiveAnalysisStats{
		RunID:         acc.RunID,
		RunName:       acc.RunName,
		Transactions:  acc.Transactions,
		Errors:        acc.Errors,
		MinResponseMs: acc.MinResponseMs,
		MaxResponseMs: acc.MaxResponseMs,
		BytesReceived: acc.BytesReceived,
	}
	if acc.Transactions > 0 {
		stats.ErrorPct = float64(acc.Errors) / float64(acc.Transactions) * 100
		stats.AvgResponseMs = acc.TotalDuration / float64(acc.Transactions)
	}
	sort.Float64s(acc.Durations)
	stats.P95Ms = archiveAnalysisPercentile(acc.Durations, 0.95)
	stats.P99Ms = archiveAnalysisPercentile(acc.Durations, 0.99)
	return stats
}

func archiveAnalysisPercentile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1) * q)
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func archiveTransactionDrilldown(runID string, index int, tx TransactionEvent) RunArchiveTransactionDrilldown {
	path := make([]string, 0, 4)
	for _, value := range []string{tx.Region, tx.Group, tx.ActionName, tx.DependencyTarget} {
		if strings.TrimSpace(value) != "" {
			path = append(path, value)
		}
	}
	return RunArchiveTransactionDrilldown{
		Index:             index,
		Timestamp:         tx.Timestamp,
		RunID:             runID,
		Group:             tx.Group,
		Region:            tx.Region,
		ActionName:        tx.ActionName,
		DependencyType:    tx.DependencyType,
		DependencyTarget:  tx.DependencyTarget,
		DurationMs:        tx.DurationMs,
		StatusCode:        tx.StatusCode,
		Success:           tx.Success,
		ErrorMessage:      tx.ErrorMessage,
		BytesReceived:     tx.BytesReceived,
		TraceID:           tx.TraceID,
		CorrelationID:     tx.CorrelationID,
		SourceTransaction: tx.SourceTransaction,
		SourceAction:      firstNonEmpty(tx.SourceActionName, tx.SourceAction),
		Path:              path,
	}
}

func filterArchiveAnalysisTransactions(transactions []TransactionEvent, filter transactionEventFilter) []TransactionEvent {
	out := make([]TransactionEvent, 0, len(transactions))
	for _, tx := range transactions {
		if transactionMatchesArchiveFilter(&tx, filter) {
			out = append(out, tx)
		}
	}
	return out
}

func archiveAnalysisRunFromRecord(id string, name string, record *storage.RunArchiveRecord, filter transactionEventFilter) (archiveAnalysisRun, error) {
	transactions, err := readAllArchiveTransactions(record)
	if err != nil {
		return archiveAnalysisRun{}, err
	}
	filtered := filterArchiveAnalysisTransactions(transactions, filter)
	if filter.SortBy != "" {
		sortArchiveTransactions(filtered, filter.SortBy, filter.SortDir)
	} else {
		sort.SliceStable(filtered, func(i, j int) bool {
			return filtered[i].Timestamp.Before(filtered[j].Timestamp)
		})
	}
	return archiveAnalysisRun{ID: id, Name: name, Transactions: filtered}, nil
}

func archiveAnalysisFilterSummary(filter transactionEventFilter) map[string]any {
	out := map[string]any{}
	if filter.Action != "" {
		out["action"] = filter.Action
	}
	if filter.SOAPOperation != "" {
		out["soap_operation"] = filter.SOAPOperation
	}
	if filter.Group != "" {
		out["group"] = filter.Group
	}
	if filter.CorrelationID != "" {
		out["correlation_id"] = filter.CorrelationID
	}
	if filter.TraceID != "" {
		out["trace_id"] = filter.TraceID
	}
	if filter.DependencyType != "" {
		out["dependency_type"] = filter.DependencyType
	}
	if filter.DependencyTarget != "" {
		out["dependency_target"] = filter.DependencyTarget
	}
	if filter.Status != "" {
		out["status"] = filter.Status
	}
	if filter.Success != nil {
		out["success"] = *filter.Success
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
