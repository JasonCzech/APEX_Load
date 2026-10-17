package api

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/runcontract"
)

// ValidateTransactionSLARules preserves the public API entry point.
func ValidateTransactionSLARules(rules []TransactionSLARule) error {
	return runcontract.ValidateTransactionSLARules(rules)
}

// Browser iterations a transaction SLA rule judges.
const (
	transactionSLAIterationsWarm = "warm"
	transactionSLAIterationsAll  = "all"
	transactionSLAIterationsCold = "cold"
)

// transactionSLAJudgesIteration reports whether a rule judges an event's
// browser iteration. Only iterations tagged cold differ: a session that
// never runs warm is not tagged, so every value but "cold" judges it.
func transactionSLAJudgesIteration(rule TransactionSLARule, event TransactionEvent) bool {
	cold := event.PropertyDimensions[protocol.BrowserIterationDimension] == protocol.IterationCold
	switch rule.Iterations {
	case transactionSLAIterationsAll:
		return true
	case transactionSLAIterationsCold:
		return cold
	}
	return !cold
}

func transactionSLASeverity(rule TransactionSLARule) string {
	if rule.Severity == "warning" {
		return "warning"
	}
	return "fail"
}
