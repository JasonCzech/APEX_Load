package api

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// agentKindCodes are the short kind codes finding ids carry.
var agentKindCodes = map[string]string{
	agentKindSLABreach:     "sla",
	agentKindRegression:    "reg",
	agentKindErrorGroup:    "err",
	agentKindEvidenceGap:   "gap",
	agentKindValidity:      "val",
	agentKindThroughputGap: "tput",
	agentKindEnvironment:   "env",
	agentKindAnomaly:       "anom",
	agentKindRCAHypothesis: "rca",
	agentKindAutoStop:      "stop",
}

// agentFindingID fingerprints a finding by what it is about, never by its
// values: the same breach of the same transaction's metric gets the same id
// in every run, so an agent can tell a new issue from a recurring one. A
// renamed transaction is, by design, a new finding. Changing the key or the
// salt changes every id, so it also needs agentBriefBuilderVersion bumped.
func agentFindingID(kind string, key ...string) string {
	parts := make([]string, 0, len(key)+2)
	parts = append(parts, "v1", kind)
	for _, part := range key {
		parts = append(parts, normalizeAgentFingerprintPart(part))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	code := agentKindCodes[kind]
	if code == "" {
		code = "x"
	}
	return "fnd_" + code + "_" + hex.EncodeToString(sum[:])[:12]
}

// agentSLAFindingID fingerprints an SLA breach by transaction and metric,
// plus any scope the check narrows to: group, a non-default population,
// protocol and a non-default iteration selection. Per-group or per-protocol
// rules on one transaction stay separate, while a rule over the default
// scope still merges with the transaction's goal row.
func agentSLAFindingID(transaction, metric, group, population string) string {
	return agentSLARuleFindingID(transaction, metric, group, population, "", "")
}

func agentSLARuleFindingID(transaction, metric, group, population, protocol, iterations string) string {
	key := []string{transaction, metric}
	if group = strings.TrimSpace(group); group != "" {
		key = append(key, "group="+group)
	}
	// The evaluator reads an empty population as "transaction".
	if population = strings.ToLower(strings.TrimSpace(population)); population != "" && population != "transaction" {
		key = append(key, "population="+population)
	}
	if protocol = strings.TrimSpace(protocol); protocol != "" {
		key = append(key, "protocol="+protocol)
	}
	// Warm iterations are the default selection.
	if iterations = strings.ToLower(strings.TrimSpace(iterations)); iterations != "" && iterations != "warm" {
		key = append(key, "iterations="+iterations)
	}
	return agentFindingID(agentKindSLABreach, key...)
}

// agentRuleFindingID is a transaction SLA rule's finding id.
func agentRuleFindingID(rule TransactionSLARule, metric string) string {
	return agentSLARuleFindingID(rule.Transaction, metric, rule.Group, rule.Population, rule.Protocol, rule.Iterations)
}

func normalizeAgentFingerprintPart(part string) string {
	return strings.ToLower(strings.Join(strings.Fields(part), " "))
}
