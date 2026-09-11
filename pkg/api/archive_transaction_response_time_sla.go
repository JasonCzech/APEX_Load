package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const transactionResponseTimeSLARequiredMetPct = 95.0

type transactionResponseTimeSLAAccumulator struct {
	total    int64
	eligible int64
	excluded int64
	missed   int64
}

func transactionResponseTimeSLAConfig(manifest *RunArchiveManifest) (*GoalConfig, []TestGroup) {
	if manifest == nil {
		return nil, nil
	}
	if manifest.Provenance.GoalConfig != nil || len(manifest.Provenance.Groups) > 0 {
		return manifest.Provenance.GoalConfig, manifest.Provenance.Groups
	}
	if manifest.InitialProvenance != nil {
		return manifest.InitialProvenance.GoalConfig, manifest.InitialProvenance.Groups
	}
	return nil, nil
}

func effectiveTransactionResponseTimeSLAThresholds(manifest *RunArchiveManifest) map[string]float64 {
	goals, groups := transactionResponseTimeSLAConfig(manifest)
	var defaultMs *float64
	if goals != nil {
		defaultMs = goals.TransactionResponseTimeSLAMs
	}
	thresholds := make(map[string]float64, len(groups))
	for _, group := range groups {
		threshold := group.TransactionResponseTimeSLAMs
		if threshold == nil {
			threshold = defaultMs
		}
		if threshold != nil && *threshold > 0 {
			thresholds[strings.TrimSpace(group.Name)] = *threshold
		}
	}
	return thresholds
}

func applyTransactionResponseTimeSLAs(root string, manifest *RunArchiveManifest) error {
	thresholds := effectiveTransactionResponseTimeSLAThresholds(manifest)
	if len(thresholds) == 0 {
		return nil
	}
	file, _, err := openArchiveArtifactForRead(root, manifest.TransactionsFile)
	if err != nil {
		return fmt.Errorf("open transaction evidence for response-time SLA evaluation: %w", err)
	}
	defer file.Close()

	accumulators := make(map[string]*transactionResponseTimeSLAAccumulator)
	observedGroups := make(map[string]bool, len(thresholds))
	decoder := json.NewDecoder(file)
	for {
		var event TransactionEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("decode transaction evidence for response-time SLA evaluation: %w", err)
		}
		if !event.BusinessTransaction {
			continue
		}
		group := strings.TrimSpace(event.Group)
		threshold, configured := thresholds[group]
		if !configured {
			continue
		}
		transaction := strings.TrimSpace(event.ActionName)
		if transaction == "" {
			continue
		}
		observedGroups[group] = true
		key := group + "\x00" + transaction
		acc := accumulators[key]
		if acc == nil {
			acc = &transactionResponseTimeSLAAccumulator{}
			accumulators[key] = acc
		}
		acc.total++
		if !event.Success {
			acc.excluded++
			continue
		}
		acc.eligible++
		if event.DurationMs > threshold {
			acc.missed++
		}
	}

	results := make([]TransactionResponseTimeSLAResult, 0, len(accumulators)+len(thresholds))
	for key, acc := range accumulators {
		parts := strings.SplitN(key, "\x00", 2)
		result := buildTransactionResponseTimeSLAResult(parts[0], parts[1], thresholds[parts[0]], acc)
		results = append(results, result)
	}
	for group, threshold := range thresholds {
		if observedGroups[group] {
			continue
		}
		results = append(results, buildTransactionResponseTimeSLAResult(group, "", threshold, nil))
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Group == results[j].Group {
			return results[i].Transaction < results[j].Transaction
		}
		return results[i].Group < results[j].Group
	})

	allEvaluated := true
	for _, result := range results {
		switch result.Status {
		case "fail":
			manifest.SLABreached = true
			manifest.SLADetails = append(manifest.SLADetails, fmt.Sprintf("%s / %s met its %.0f ms response-time SLA for %.1f%% of successful samples (required %.0f%%)", result.Group, result.Transaction, result.ThresholdMs, result.MetPct, result.RequiredMetPct))
		case "not_evaluated":
			allEvaluated = false
		}
	}
	manifest.TransactionResponseTimeSLAResults = results
	goals, _ := transactionResponseTimeSLAConfig(manifest)
	hasOtherCriteria := goalConfigHasArchiveIndependentSLA(goals) || (goals != nil && len(goals.TransactionRules) > 0)
	if hasOtherCriteria {
		manifest.SLAEvaluated = manifest.SLAEvaluated && allEvaluated
	} else {
		manifest.SLAEvaluated = allEvaluated
	}
	return nil
}

func buildTransactionResponseTimeSLAResult(group, transaction string, threshold float64, acc *transactionResponseTimeSLAAccumulator) TransactionResponseTimeSLAResult {
	result := TransactionResponseTimeSLAResult{Group: group, Transaction: transaction, ThresholdMs: threshold, RequiredMetPct: transactionResponseTimeSLARequiredMetPct, Status: "not_evaluated"}
	if acc == nil {
		return result
	}
	result.TotalCount = acc.total
	result.EligibleSuccessCount = acc.eligible
	result.ExcludedErrorCount = acc.excluded
	result.MissedCount = acc.missed
	if acc.eligible == 0 {
		return result
	}
	result.MissedPct = float64(acc.missed) * 100 / float64(acc.eligible)
	result.MetPct = 100 - result.MissedPct
	result.Status = "pass"
	if result.MetPct < transactionResponseTimeSLARequiredMetPct {
		result.Status = "fail"
	}
	return result
}
