package api

import (
	"fmt"
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
	Total        int
	Paged        bool
	PageOffset   int
	nodeMeta     map[string]RunArchiveAnalysisNode
	edgeMeta     map[string]RunArchiveAnalysisEdge
	nodeAcc      map[string]*archiveAnalysisStatsAccumulator
	edgeAcc      map[string]*archiveAnalysisStatsAccumulator
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
	Quantiles     *archiveQuantileAccumulator
}

func buildRunArchiveAnalysis(current archiveAnalysisRun, baseline *archiveAnalysisRun, filter transactionEventFilter, page archivePage) RunArchiveAnalysisResponse {
	runs := []archiveAnalysisRun{current}
	if baseline != nil {
		runs = append([]archiveAnalysisRun{*baseline}, runs...)
	}
	nodes, edges := buildRunArchiveAnalysisGraph(runs)
	paged := current.Transactions
	pageOffset := current.PageOffset
	if !current.Paged {
		paged = pageArchiveItems(current.Transactions, page)
		pageOffset = page.Offset
	}
	drilldown := make([]RunArchiveTransactionDrilldown, 0, len(paged))
	for index, tx := range paged {
		drilldown = append(drilldown, archiveTransactionDrilldown(current.ID, pageOffset+index, tx))
	}
	response := RunArchiveAnalysisResponse{
		SchemaVersion: runArchiveAnalysisSchemaVersion,
		TestID:        current.ID,
		Graph:         RunArchiveAnalysisGraph{Nodes: nodes, Edges: edges},
		Transactions:  drilldown,
		Count:         len(drilldown),
		Total:         archiveAnalysisRunTotal(current),
		Limit:         page.Limit,
		Offset:        page.Offset,
		Filters:       archiveAnalysisFilterSummary(filter),
	}
	if baseline != nil {
		response.BaselineID = baseline.ID
	}
	return response
}

func archiveAnalysisRunTotal(run archiveAnalysisRun) int {
	if run.Total > 0 || len(run.Transactions) == 0 {
		return run.Total
	}
	return len(run.Transactions)
}

func buildRunArchiveAnalysisGraph(runs []archiveAnalysisRun) ([]RunArchiveAnalysisNode, []RunArchiveAnalysisEdge) {
	nodeAcc := map[string]map[string]*archiveAnalysisStatsAccumulator{}
	edgeAcc := map[string]map[string]*archiveAnalysisStatsAccumulator{}
	nodeMeta := map[string]RunArchiveAnalysisNode{}
	edgeMeta := map[string]RunArchiveAnalysisEdge{}

	for _, run := range runs {
		if run.nodeMeta != nil || run.edgeMeta != nil {
			for id, node := range run.nodeMeta {
				nodeMeta[id] = node
				if nodeAcc[id] == nil {
					nodeAcc[id] = map[string]*archiveAnalysisStatsAccumulator{}
				}
				nodeAcc[id][run.ID] = run.nodeAcc[id]
			}
			for id, edge := range run.edgeMeta {
				edgeMeta[id] = edge
				if edgeAcc[id] == nil {
					edgeAcc[id] = map[string]*archiveAnalysisStatsAccumulator{}
				}
				edgeAcc[id][run.ID] = run.edgeAcc[id]
			}
			continue
		}
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
	if acc.Quantiles == nil {
		acc.Quantiles = &archiveQuantileAccumulator{}
	}
	acc.Quantiles.Add(tx.DurationMs)
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
	if acc.Quantiles != nil {
		stats.P95Ms = acc.Quantiles.Quantile(0.95)
		stats.P99Ms = acc.Quantiles.Quantile(0.99)
	}
	return stats
}

func archiveAnalysisPercentile(values []float64, q float64) float64 {
	return percentileFromSortedSamples(values, q)
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

func archiveAnalysisRunFromRecord(id string, name string, record *storage.RunArchiveRecord, filter transactionEventFilter, page *archivePage) (archiveAnalysisRun, error) {
	run := archiveAnalysisRun{
		ID:       id,
		Name:     name,
		nodeMeta: make(map[string]RunArchiveAnalysisNode),
		edgeMeta: make(map[string]RunArchiveAnalysisEdge),
		nodeAcc:  make(map[string]*archiveAnalysisStatsAccumulator),
		edgeAcc:  make(map[string]*archiveAnalysisStatsAccumulator),
	}
	pageLimit := 0
	pageOffset := 0
	pageKeep := 0
	pageSortBy := filter.SortBy
	pageSortDir := filter.SortDir
	if page != nil {
		pageLimit = page.Limit
		if pageLimit <= 0 {
			pageLimit = 100
		}
		if pageLimit > maxArchivePageSize {
			return archiveAnalysisRun{}, fmt.Errorf("archive page limit exceeds %d", maxArchivePageSize)
		}
		pageOffset = page.Offset
		if pageOffset < 0 {
			pageOffset = 0
		}
		pageKeep = pageOffset + pageLimit
		if pageKeep < pageLimit || pageKeep > maxArchiveSortWindow {
			return archiveAnalysisRun{}, fmt.Errorf("sorted archive window exceeds %d records", maxArchiveSortWindow)
		}
		if pageSortBy == "" {
			// Analysis historically sorted by timestamp when sort_by was omitted.
			pageSortBy = "time"
			pageSortDir = "asc"
		}
		run.Transactions = make([]TransactionEvent, 0, min(pageKeep, pageLimit*2))
		run.Paged = true
		run.PageOffset = pageOffset
	}
	matched := 0
	err := forEachArchiveTransaction(record, func(tx *TransactionEvent) error {
		if !transactionMatchesArchiveFilter(tx, filter) {
			return nil
		}
		matched++
		if err := mergeArchiveAnalysisRunTransaction(&run, *tx); err != nil {
			return err
		}
		if page != nil {
			run.Transactions = append(run.Transactions, compactArchiveAnalysisTransaction(*tx))
			if len(run.Transactions) >= pageKeep*2 {
				sortArchiveTransactions(run.Transactions, pageSortBy, pageSortDir)
				run.Transactions = run.Transactions[:pageKeep]
			}
		}
		return nil
	})
	if err != nil {
		return archiveAnalysisRun{}, err
	}
	run.Total = matched
	if page != nil {
		sortArchiveTransactions(run.Transactions, pageSortBy, pageSortDir)
		if len(run.Transactions) > pageKeep {
			run.Transactions = run.Transactions[:pageKeep]
		}
		if pageOffset >= len(run.Transactions) {
			run.Transactions = []TransactionEvent{}
		} else {
			run.Transactions = run.Transactions[pageOffset:]
		}
	}
	return run, nil
}

func sortArchiveAnalysisTransactions(transactions []TransactionEvent, filter transactionEventFilter) {
	if filter.SortBy != "" {
		sortArchiveTransactions(transactions, filter.SortBy, filter.SortDir)
	} else {
		sort.SliceStable(transactions, func(i, j int) bool {
			return transactions[i].Timestamp.Before(transactions[j].Timestamp)
		})
	}
}

func mergeArchiveAnalysisRunTransaction(run *archiveAnalysisRun, tx TransactionEvent) error {
	for _, node := range archiveAnalysisNodesForTransaction(tx) {
		if _, exists := run.nodeMeta[node.ID]; !exists && len(run.nodeMeta) >= maxArchiveAnalysisDimensions {
			return fmt.Errorf("archive analysis exceeds %d distinct nodes", maxArchiveAnalysisDimensions)
		}
		run.nodeMeta[node.ID] = node
		mergeArchiveAnalysisRunAccumulator(run.nodeAcc, node.ID, run, tx)
	}
	for _, edge := range archiveAnalysisEdgesForTransaction(tx) {
		if _, exists := run.edgeMeta[edge.ID]; !exists && len(run.edgeMeta) >= maxArchiveAnalysisDimensions {
			return fmt.Errorf("archive analysis exceeds %d distinct edges", maxArchiveAnalysisDimensions)
		}
		run.edgeMeta[edge.ID] = edge
		mergeArchiveAnalysisRunAccumulator(run.edgeAcc, edge.ID, run, tx)
	}
	return nil
}

func mergeArchiveAnalysisRunAccumulator(values map[string]*archiveAnalysisStatsAccumulator, key string, run *archiveAnalysisRun, tx TransactionEvent) {
	acc := values[key]
	if acc == nil {
		acc = &archiveAnalysisStatsAccumulator{RunID: run.ID, RunName: run.Name}
		values[key] = acc
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
	if acc.Quantiles == nil {
		acc.Quantiles = &archiveQuantileAccumulator{}
	}
	acc.Quantiles.Add(tx.DurationMs)
}

// compactArchiveAnalysisTransaction drops request/response snapshots and other
// nested diagnostic payloads that the graph/drilldown response never exposes.
// This keeps the bounded sort window independent of archived body sizes.
func compactArchiveAnalysisTransaction(tx TransactionEvent) TransactionEvent {
	return TransactionEvent{
		Timestamp:           tx.Timestamp,
		Group:               tx.Group,
		Region:              tx.Region,
		ActionName:          tx.ActionName,
		BusinessTransaction: tx.BusinessTransaction,
		DependencyType:      tx.DependencyType,
		DependencyTarget:    tx.DependencyTarget,
		DurationMs:          tx.DurationMs,
		StatusCode:          tx.StatusCode,
		Success:             tx.Success,
		ErrorMessage:        tx.ErrorMessage,
		BytesReceived:       tx.BytesReceived,
		TraceID:             tx.TraceID,
		CorrelationID:       tx.CorrelationID,
		SourceTransaction:   tx.SourceTransaction,
		SourceAction:        tx.SourceAction,
		SourceActionName:    tx.SourceActionName,
	}
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
