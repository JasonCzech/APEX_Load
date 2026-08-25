package api

import (
	"fmt"
	"math"
	"strings"
)

const (
	maxTransactionSLARules     = 128
	maxTransactionSLANameBytes = 256
)

var transactionSLAMetrics = map[string]struct{}{
	"average_duration_ms": {},
	"p90_duration_ms":     {},
	"p95_duration_ms":     {},
	"min_duration_ms":     {},
	"max_duration_ms":     {},
	"error_count":         {},
	"error_rate_pct":      {},
	"minimum_count":       {},
	"bytes_sent":          {},
	"bytes_received":      {},
	"tps":                 {},
}

var transactionSLAComparators = map[string]struct{}{
	"lt":  {},
	"lte": {},
	"gt":  {},
	"gte": {},
	"eq":  {},
	"neq": {},
}

// ValidateTransactionSLARules validates the bounded terminal rule contract.
// It deliberately does not trim or case-fold transaction names because the
// archive evaluator performs exact, case-sensitive matching.
func ValidateTransactionSLARules(rules []TransactionSLARule) error {
	if len(rules) > maxTransactionSLARules {
		return fmt.Errorf("transaction_rules contains %d rules; limit is %d", len(rules), maxTransactionSLARules)
	}
	for index, rule := range rules {
		path := fmt.Sprintf("transaction_rules[%d]", index)
		if rule.Transaction == "" || strings.TrimSpace(rule.Transaction) != rule.Transaction {
			return fmt.Errorf("%s.transaction must be a non-empty exact name without surrounding whitespace", path)
		}
		if len(rule.Transaction) > maxTransactionSLANameBytes {
			return fmt.Errorf("%s.transaction exceeds %d bytes", path, maxTransactionSLANameBytes)
		}
		if _, ok := transactionSLAMetrics[rule.Metric]; !ok {
			return fmt.Errorf("%s.metric %q is not supported", path, rule.Metric)
		}
		if _, ok := transactionSLAComparators[rule.Comparator]; !ok {
			return fmt.Errorf("%s.comparator %q is not supported", path, rule.Comparator)
		}
		if math.IsNaN(rule.Threshold) || math.IsInf(rule.Threshold, 0) || rule.Threshold < 0 {
			return fmt.Errorf("%s.threshold must be a finite non-negative number", path)
		}
		if rule.Severity != "" && rule.Severity != "fail" && rule.Severity != "warning" {
			return fmt.Errorf("%s.severity must be fail or warning", path)
		}
	}
	return nil
}

func transactionSLASeverity(rule TransactionSLARule) string {
	if rule.Severity == "warning" {
		return "warning"
	}
	return "fail"
}
