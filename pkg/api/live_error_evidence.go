package api

// Live error-pattern + evidence subsystem: aggregate live error patterns from
// transactions/results, capture and trim per-test error evidence, and serve it
// via ListLiveErrorEvidence. Extracted from metrics.go.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func updateLiveErrorPatterns(existing map[string]*LiveErrorPattern, live *LiveMetrics, testID string) map[string]*LiveErrorPattern {
	if existing == nil {
		existing = make(map[string]*LiveErrorPattern)
	}
	if live == nil || len(live.Transactions) == 0 {
		return existing
	}

	affectedGroups := groupsWithErrors(live.GroupMetrics)
	actions := make([]string, 0, len(live.Transactions))
	for action, tx := range live.Transactions {
		if tx == nil || tx.Errors <= 0 {
			continue
		}
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		tx := live.Transactions[action]
		source := liveErrorPatternSource(tx)
		signature := normalizeArchiveErrorSignature(source)
		observedAt := tx.LastErrorAt
		if observedAt.IsZero() {
			observedAt = live.Timestamp
		}
		pattern, ok := existing[signature]
		if !ok {
			pattern = &LiveErrorPattern{
				Signature: signature,
				Sample:    source,
				FirstSeen: observedAt,
			}
			existing[signature] = pattern
		}
		pattern.Count += tx.Errors
		pattern.LatestSeen = observedAt
		if pattern.FirstSeen.IsZero() || observedAt.Before(pattern.FirstSeen) {
			pattern.FirstSeen = observedAt
		}
		if pattern.StatusCode == 0 {
			pattern.StatusCode = tx.LastStatusCode
		}
		pattern.ErrorPct = txErrorPct(tx)
		pattern.Actions = appendUniqueSorted(pattern.Actions, action)
		pattern.AffectedGroups = mergeStringSets(pattern.AffectedGroups, affectedGroups)
		pattern.SnapshotURL = liveErrorPatternSnapshotURL(testID, pattern.Actions)
	}
	return existing
}

// trimLiveErrorPatternsLocked bounds the per-run error-pattern map so a long run
// against a target whose error messages carry high-cardinality identifiers
// (alphanumeric IDs, hashes, distinct URL slugs that survive signature
// normalization) cannot grow it without limit — each distinct normalized
// signature otherwise mints a permanent map entry for the whole run. It mirrors
// trimLiveErrorEvidenceLocked: when the map exceeds maxLiveErrorPatternsPerRun
// it evicts the lowest-value patterns first — fewest occurrences, then oldest
// last-seen — which are exactly the ones liveErrorPatternsList (sorted by Count
// desc) never surfaces. Callers must hold tr.mu.
func trimLiveErrorPatternsLocked(patterns map[string]*LiveErrorPattern) {
	if len(patterns) <= maxLiveErrorPatternsPerRun {
		return
	}
	signatures := make([]string, 0, len(patterns))
	for signature := range patterns {
		signatures = append(signatures, signature)
	}
	sort.Slice(signatures, func(left, right int) bool {
		lp := patterns[signatures[left]]
		rp := patterns[signatures[right]]
		if lp == nil || rp == nil {
			return lp == nil && rp != nil
		}
		if lp.Count != rp.Count {
			return lp.Count < rp.Count
		}
		if !lp.LatestSeen.Equal(rp.LatestSeen) {
			return lp.LatestSeen.Before(rp.LatestSeen)
		}
		return signatures[left] < signatures[right]
	})
	for i := 0; len(patterns) > maxLiveErrorPatternsPerRun && i < len(signatures); i++ {
		delete(patterns, signatures[i])
	}
}

func liveErrorPatternSource(tx *TxMetrics) string {
	if tx == nil {
		return "transaction failed"
	}
	return liveErrorPatternSourceFromValues(tx.LastErrorMessage, tx.LastStatusCode)
}

func liveErrorPatternSourceFromValues(errorMessage string, statusCode int) string {
	if message := strings.TrimSpace(errorMessage); message != "" {
		return message
	}
	if statusCode > 0 {
		return fmt.Sprintf("request failed with status %d", statusCode)
	}
	return "transaction failed"
}

func liveErrorEvidenceFromResult(result *protocol.Result) LiveErrorEvidence {
	if result == nil || result.Snapshot == nil || result.Success && !result.MessageDetailRetained {
		return LiveErrorEvidence{}
	}
	statusCode := result.StatusCode
	if statusCode == 0 && result.Snapshot.ResponseStatus > 0 {
		statusCode = result.Snapshot.ResponseStatus
	}
	source := liveErrorPatternSourceFromValues(result.ErrorMessage, statusCode)
	status := "failed"
	signature := normalizeArchiveErrorSignature(source)
	if result.Success {
		status = "success"
		signature = "__message_detail__"
	}
	timestamp := result.StartTime
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	return LiveErrorEvidence{
		ID:             stableLiveEvidenceID(result.ActionName, result.Group, result.CorrelationID, timestamp, result.Duration),
		Signature:      signature,
		Action:         strings.TrimSpace(result.ActionName),
		Group:          strings.TrimSpace(result.Group),
		Timestamp:      timestamp,
		DurationMs:     float64(result.Duration) / float64(time.Millisecond),
		Status:         status,
		StatusCode:     statusCode,
		ErrorMessage:   strings.TrimSpace(result.ErrorMessage),
		Snapshot:       cloneProtocolErrorSnapshot(result.Snapshot),
		Region:         strings.TrimSpace(result.Region),
		NetworkProfile: strings.TrimSpace(result.NetworkProfile),
		Hierarchy:      strings.TrimSpace(result.Hierarchy),
		Phase:          strings.TrimSpace(result.Phase),
	}
}

func stableLiveEvidenceID(action, group, correlation string, timestamp time.Time, duration time.Duration) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", action, group, correlation, timestamp.UnixNano(), duration)))
	return "tx_" + hex.EncodeToString(hash[:16])
}

func liveErrorEvidenceFromTransaction(tx generatorrpc.TransactionMetric) LiveErrorEvidence {
	if tx.Snapshot == nil {
		return LiveErrorEvidence{}
	}
	statusCode := int(tx.StatusCode)
	if statusCode == 0 && tx.Snapshot.ResponseStatus > 0 {
		statusCode = tx.Snapshot.ResponseStatus
	}
	source := liveErrorPatternSourceFromValues(tx.ErrorMessage, statusCode)
	status := "failed"
	signature := normalizeArchiveErrorSignature(source)
	if tx.Success {
		status = "success"
		signature = "__message_detail__"
	}
	timestamp := time.Now().UTC()
	if tx.StartTimeUnixMs > 0 {
		timestamp = time.UnixMilli(tx.StartTimeUnixMs)
	}
	return LiveErrorEvidence{
		ID:             stableLiveEvidenceID(tx.Name, tx.Group, tx.CorrelationID, timestamp, time.Duration(tx.DurationMs*float64(time.Millisecond))),
		Signature:      signature,
		Action:         strings.TrimSpace(tx.Name),
		Group:          strings.TrimSpace(tx.Group),
		Timestamp:      timestamp,
		DurationMs:     tx.DurationMs,
		Status:         status,
		StatusCode:     statusCode,
		ErrorMessage:   strings.TrimSpace(tx.ErrorMessage),
		Snapshot:       cloneProtocolErrorSnapshot(tx.Snapshot),
		Region:         strings.TrimSpace(tx.Region),
		NetworkProfile: strings.TrimSpace(tx.NetworkProfile),
		Hierarchy:      strings.TrimSpace(tx.Hierarchy),
		Phase:          strings.TrimSpace(tx.Phase),
	}
}

func groupsWithErrors(groups map[string]*GroupLiveMetrics) []string {
	if len(groups) == 0 {
		return nil
	}
	result := make([]string, 0, len(groups))
	for name, group := range groups {
		if group != nil && group.Errors > 0 {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func appendUniqueSorted(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	values = append(values, value)
	sort.Strings(values)
	return values
}

func mergeStringSets(left []string, right []string) []string {
	for _, value := range right {
		left = appendUniqueSorted(left, value)
	}
	return left
}

func liveErrorPatternSnapshotURL(testID string, actions []string) string {
	if strings.TrimSpace(testID) == "" {
		return ""
	}
	query := url.Values{}
	query.Set("success", "false")
	query.Set("limit", "20")
	if len(actions) == 1 {
		query.Set("action", actions[0])
	}
	return fmt.Sprintf("/api/v1/tests/%s/archive/transactions?%s", url.PathEscape(testID), query.Encode())
}

func liveErrorPatternEvidenceURL(testID string, signature string) string {
	if strings.TrimSpace(testID) == "" || strings.TrimSpace(signature) == "" {
		return ""
	}
	query := url.Values{}
	query.Set("signature", signature)
	query.Set("limit", strconv.Itoa(defaultLiveErrorEvidenceLimit))
	return fmt.Sprintf("/api/v1/tests/%s/errors/evidence?%s", url.PathEscape(testID), query.Encode())
}

func liveErrorPatternsList(patterns map[string]*LiveErrorPattern) []LiveErrorPattern {
	if len(patterns) == 0 {
		return nil
	}
	result := make([]LiveErrorPattern, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern == nil {
			continue
		}
		clone := *pattern
		clone.Actions = append([]string(nil), pattern.Actions...)
		clone.AffectedGroups = append([]string(nil), pattern.AffectedGroups...)
		result = append(result, clone)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Count == result[right].Count {
			return result[left].FirstSeen.Before(result[right].FirstSeen)
		}
		return result[left].Count > result[right].Count
	})
	return result
}

func (tr *TestRunner) appendLiveErrorEvidence(test *ManagedTest, evidence []LiveErrorEvidence) {
	if tr == nil || test == nil || len(evidence) == 0 {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.appendLiveErrorEvidenceLocked(test, evidence)
}

func (tr *TestRunner) appendLiveErrorEvidenceLocked(test *ManagedTest, evidence []LiveErrorEvidence) {
	if test == nil || len(evidence) == 0 {
		return
	}
	if test.liveErrorEvidence == nil {
		test.liveErrorEvidence = make(map[string][]LiveErrorEvidence)
	}
	for _, item := range evidence {
		if strings.TrimSpace(item.Signature) == "" {
			continue
		}
		if item.ID == "" {
			item.ID = "live-evidence-" + uuid.NewString()
		}
		if item.Timestamp.IsZero() {
			item.Timestamp = time.Now().UTC()
		}
		if item.Status == "" {
			item.Status = "failed"
		}
		item.Snapshot = cloneProtocolErrorSnapshot(item.Snapshot)
		items := append(test.liveErrorEvidence[item.Signature], item)
		if len(items) > maxLiveErrorEvidencePerSignature {
			items = items[len(items)-maxLiveErrorEvidencePerSignature:]
		}
		test.liveErrorEvidence[item.Signature] = items
	}
	trimLiveErrorEvidenceLocked(test)
}

func trimLiveErrorEvidenceLocked(test *ManagedTest) {
	if test == nil || len(test.liveErrorEvidence) == 0 {
		return
	}
	total := 0
	for _, items := range test.liveErrorEvidence {
		total += len(items)
	}
	if total <= maxLiveErrorEvidencePerRun {
		return
	}
	signatures := make([]string, 0, len(test.liveErrorEvidence))
	for signature := range test.liveErrorEvidence {
		signatures = append(signatures, signature)
	}
	sort.Slice(signatures, func(left, right int) bool {
		leftItems := test.liveErrorEvidence[signatures[left]]
		rightItems := test.liveErrorEvidence[signatures[right]]
		if len(leftItems) == 0 || len(rightItems) == 0 {
			return len(leftItems) < len(rightItems)
		}
		return leftItems[0].Timestamp.Before(rightItems[0].Timestamp)
	})
	for total > maxLiveErrorEvidencePerRun && len(signatures) > 0 {
		signature := signatures[0]
		items := test.liveErrorEvidence[signature]
		if len(items) <= 1 {
			delete(test.liveErrorEvidence, signature)
			signatures = signatures[1:]
			total -= len(items)
			continue
		}
		test.liveErrorEvidence[signature] = items[1:]
		total--
	}
}

func enrichLiveErrorPatternsWithEvidence(test *ManagedTest, patterns map[string]*LiveErrorPattern) {
	if test == nil || len(patterns) == 0 {
		return
	}
	for signature, pattern := range patterns {
		if pattern == nil {
			continue
		}
		items := test.liveErrorEvidence[signature]
		pattern.EvidenceCount = len(items)
		pattern.LatestEvidenceID = ""
		pattern.EvidenceURL = ""
		if len(items) > 0 {
			pattern.LatestEvidenceID = items[len(items)-1].ID
			pattern.EvidenceURL = liveErrorPatternEvidenceURL(test.ID, signature)
		}
	}
}

func (tr *TestRunner) ListLiveErrorEvidence(testID string, signature string, action string, limit int) ([]LiveErrorEvidence, bool) {
	if tr == nil {
		return nil, false
	}
	if limit <= 0 {
		limit = defaultLiveErrorEvidenceLimit
	}
	if limit > maxLiveErrorEvidenceLimit {
		limit = maxLiveErrorEvidenceLimit
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	testID = tr.resolveRefKeyLocked(testID)
	test, ok := tr.tests[testID]
	if !ok {
		return nil, false
	}
	items := test.liveErrorEvidence[signature]
	if len(items) == 0 {
		return []LiveErrorEvidence{}, true
	}
	filtered := make([]LiveErrorEvidence, 0, minInt(limit, len(items)))
	for i := len(items) - 1; i >= 0 && len(filtered) < limit; i-- {
		item := items[i]
		if action != "" && item.Action != action {
			continue
		}
		item.Snapshot = cloneProtocolErrorSnapshot(item.Snapshot)
		filtered = append(filtered, item)
	}
	return filtered, true
}

func (tr *TestRunner) GetLiveTransactionDetail(testID, eventID string) (LiveErrorEvidence, bool, bool) {
	if tr == nil {
		return LiveErrorEvidence{}, false, false
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	testID = tr.resolveRefKeyLocked(testID)
	test, exists := tr.tests[testID]
	if !exists {
		return LiveErrorEvidence{}, false, false
	}
	for _, items := range test.liveErrorEvidence {
		for _, item := range items {
			if item.ID == eventID {
				item.Snapshot = cloneProtocolErrorSnapshot(item.Snapshot)
				return item, true, true
			}
		}
	}
	return LiveErrorEvidence{}, true, false
}

func cloneProtocolErrorSnapshot(snapshot *protocol.ErrorSnapshot) *protocol.ErrorSnapshot {
	if snapshot == nil {
		return nil
	}
	cloned := *snapshot
	cloned.Resources = protocol.CloneResourceSnapshots(snapshot.Resources)
	cloned.RequestHeaders = cloneProtocolStringMap(snapshot.RequestHeaders)
	cloned.ResponseHeaders = cloneProtocolStringMap(snapshot.ResponseHeaders)
	cloned.ConsoleEntries = append([]protocol.ConsoleEntry(nil), snapshot.ConsoleEntries...)
	cloned.NetworkEntries = protocol.CloneNetworkEntries(snapshot.NetworkEntries)
	if snapshot.DOMSummary != nil {
		domSummary := *snapshot.DOMSummary
		domSummary.VisibleText = append([]string(nil), snapshot.DOMSummary.VisibleText...)
		cloned.DOMSummary = &domSummary
	}
	return &cloned
}

func cloneProtocolStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
