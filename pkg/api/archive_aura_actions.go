package api

import (
	"encoding/json"
	"sort"
	"strings"
)

// A browser step on a Salesforce Lightning page records the Aura actions it
// triggered (protocol_evidence.aura_actions). The run report rolls them up by
// transaction: each action's count, the time of the requests that carried it
// (a batch's time counts for every action in it), and its errors.

const (
	// maxAuraActionNames bounds the distinct action names a run reports;
	// the rest are reported together as otherAuraActions.
	maxAuraActionNames = 100
	// maxAuraActionsPerTransaction is how many actions a transaction lists,
	// by total time.
	maxAuraActionsPerTransaction = 10
	otherAuraActions             = "(other actions)"
)

// AuraActionRollup is one Aura action in one transaction over the run.
type AuraActionRollup struct {
	Transaction string  `json:"transaction"`
	Name        string  `json:"name"`
	Apex        bool    `json:"apex,omitempty"`
	Count       int64   `json:"count"`
	TotalMs     float64 `json:"total_ms"`
	Errors      int64   `json:"errors,omitempty"`
}

type archiveAuraActionAccumulator struct {
	names map[string]bool
	rows  map[[2]string]*AuraActionRollup
}

func newArchiveAuraActionAccumulator() *archiveAuraActionAccumulator {
	return &archiveAuraActionAccumulator{names: map[string]bool{}, rows: map[[2]string]*AuraActionRollup{}}
}

func (a *archiveAuraActionAccumulator) merge(tx TransactionEvent) {
	evidence, _ := tx.ProtocolEvidence["aura_actions"].(map[string]any)
	actions, _ := evidence["actions"].([]any)
	for _, raw := range actions {
		action, _ := raw.(map[string]any)
		name, _ := action["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		apex, _ := action["apex"].(bool)
		if !a.names[name] && len(a.names) >= maxAuraActionNames {
			name, apex = otherAuraActions, false
		} else {
			a.names[name] = true
		}
		key := [2]string{tx.SourceTransaction, name}
		row := a.rows[key]
		if row == nil {
			row = &AuraActionRollup{Transaction: tx.SourceTransaction, Name: name, Apex: apex}
			a.rows[key] = row
		}
		row.Count++
		row.TotalMs += evidenceFloat(action["duration_ms"])
		if state, _ := action["state"].(string); state != "" && state != "SUCCESS" {
			row.Errors++
		}
	}
}

// result is each transaction's actions with the most total time, in
// transaction order.
func (a *archiveAuraActionAccumulator) result() []AuraActionRollup {
	byTransaction := map[string][]AuraActionRollup{}
	for _, row := range a.rows {
		byTransaction[row.Transaction] = append(byTransaction[row.Transaction], *row)
	}
	transactions := make([]string, 0, len(byTransaction))
	for transaction := range byTransaction {
		transactions = append(transactions, transaction)
	}
	sort.Strings(transactions)
	var out []AuraActionRollup
	for _, transaction := range transactions {
		rows := byTransaction[transaction]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].TotalMs != rows[j].TotalMs {
				return rows[i].TotalMs > rows[j].TotalMs
			}
			return rows[i].Name < rows[j].Name
		})
		if len(rows) > maxAuraActionsPerTransaction {
			rows = rows[:maxAuraActionsPerTransaction]
		}
		out = append(out, rows...)
	}
	return out
}

func evidenceFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	}
	return 0
}

// auraTransactionLabel names a rollup row's transaction.
func auraTransactionLabel(transaction string) string {
	if transaction == "" {
		return "Outside transactions"
	}
	return transaction
}
