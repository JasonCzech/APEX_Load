package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

func loadCIMonitorConfiguration(ctx context.Context, bindingsPath, rulesPath string) (*[]storage.MonitorBinding, []analysis.MonitorRule, error) {
	var bindings *[]storage.MonitorBinding
	var rules []analysis.MonitorRule
	if bindingsPath != "" {
		raw, _, err := loadTrustedCIFileSnapshot(ctx, bindingsPath, 96<<10, "monitor-bindings-file")
		if err != nil {
			return nil, nil, err
		}
		var selected []storage.MonitorBinding
		if err := decodeCIMonitorArray(raw, &selected); err != nil {
			return nil, nil, err
		}
		if selected == nil || len(selected) > storage.MaxRunMonitorSources {
			return nil, nil, errors.New("monitor bindings must be a JSON array with at most 32 sources")
		}
		seen := map[string]bool{}
		for _, binding := range selected {
			if binding.SourceID == "" || len(binding.SourceID) > 256 || seen[binding.SourceID] {
				return nil, nil, errors.New("monitor bindings require unique bounded source IDs")
			}
			seen[binding.SourceID] = true
		}
		bindings = &selected
	}
	if rulesPath != "" {
		raw, _, err := loadTrustedCIFileSnapshot(ctx, rulesPath, 256<<10, "monitor-rules-file")
		if err != nil {
			return nil, nil, err
		}
		if err := decodeCIMonitorArray(raw, &rules); err != nil {
			return nil, nil, err
		}
		if rules == nil || len(rules) > 128 {
			return nil, nil, errors.New("monitor rules must be a JSON array with at most 128 rules")
		}
		for _, rule := range rules {
			if err := rule.Validate(); err != nil {
				return nil, nil, err
			}
		}
		if len(rules) > 0 && (bindings == nil || len(*bindings) == 0) {
			return nil, nil, errors.New("monitor rules require explicit source bindings")
		}
		selected := map[string]bool{}
		if bindings != nil {
			for _, binding := range *bindings {
				selected[binding.SourceID] = true
			}
		}
		for _, rule := range rules {
			if !selected[rule.SourceID] {
				return nil, nil, fmt.Errorf("monitor rule source must appear in monitor bindings")
			}
		}
	}
	return bindings, rules, nil
}

func decodeCIMonitorArray(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("monitor configuration must contain an explicit JSON array with known fields")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return errors.New("monitor configuration contains trailing JSON")
	}
	return nil
}
