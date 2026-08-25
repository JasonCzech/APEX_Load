package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/api"
)

const maxTransactionSLARuleFileBytes = 1 << 20

func parseTransactionSLARule(raw string) (api.TransactionSLARule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return api.TransactionSLARule{}, fmt.Errorf("transaction rule cannot be empty")
	}
	var rule api.TransactionSLARule
	if strings.HasPrefix(raw, "{") {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&rule); err != nil {
			return rule, fmt.Errorf("decode transaction rule JSON: %w", err)
		}
		if err := requireTransactionRuleJSONEOF(decoder); err != nil {
			return rule, err
		}
	} else {
		parts := strings.Split(raw, "|")
		if len(parts) != 4 && len(parts) != 5 {
			return rule, fmt.Errorf("transaction rule must have transaction|metric|comparator|threshold[|severity]")
		}
		threshold, err := strconv.ParseFloat(parts[3], 64)
		if err != nil {
			return rule, fmt.Errorf("parse transaction rule threshold: %w", err)
		}
		rule = api.TransactionSLARule{Transaction: parts[0], Metric: parts[1], Comparator: parts[2], Threshold: threshold}
		if len(parts) == 5 {
			rule.Severity = parts[4]
		}
	}
	if rule.Severity == "" {
		rule.Severity = "fail"
	}
	if err := api.ValidateTransactionSLARules([]api.TransactionSLARule{rule}); err != nil {
		return rule, err
	}
	return rule, nil
}

func mergeTransactionSLARuleFile(args *runArgs) error {
	if args == nil || strings.TrimSpace(args.transactionRulesFile) == "" {
		return nil
	}
	file, err := os.Open(args.transactionRulesFile)
	if err != nil {
		return fmt.Errorf("open transaction rules file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTransactionSLARuleFileBytes+1))
	if err != nil {
		return fmt.Errorf("read transaction rules file: %w", err)
	}
	if len(data) > maxTransactionSLARuleFileBytes {
		return fmt.Errorf("transaction rules file exceeds %d bytes", maxTransactionSLARuleFileBytes)
	}

	var rules []api.TransactionSLARule
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {
		if err := decoder.Decode(&rules); err != nil {
			return fmt.Errorf("decode transaction rules file: %w", err)
		}
	} else {
		var envelope struct {
			TransactionRules []api.TransactionSLARule `json:"transaction_rules"`
		}
		if err := decoder.Decode(&envelope); err != nil {
			return fmt.Errorf("decode transaction rules file: %w", err)
		}
		rules = envelope.TransactionRules
	}
	if err := requireTransactionRuleJSONEOF(decoder); err != nil {
		return err
	}
	for index := range rules {
		if rules[index].Severity == "" {
			rules[index].Severity = "fail"
		}
	}
	args.transactionRules = append(args.transactionRules, rules...)
	return nil
}

func requireTransactionRuleJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("transaction rule JSON contains trailing values")
		}
		return fmt.Errorf("decode trailing transaction rule JSON: %w", err)
	}
	return nil
}
