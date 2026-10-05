package prometheus

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

// CollectPlan commits a single operation cursor through numeric evaluations,
// sampled diagnostics and frozen exact-window queries. Nested calls share the
// deadline/request budget; an unfinished terminal window is never skipped.
func (c *Client) CollectPlan(ctx context.Context, selection Selection, from, to time.Time, runID string, windows []monitorprovider.EvaluationWindow) (Result, error) {
	s := selection.Defaults()
	result := Result{Coverage: map[string]monitorprovider.Coverage{}, Capabilities: Capabilities{BackendProfile: c.cfg.BackendProfile}}
	if err := (JobConfig{c.cfg, s}).Validate(); err != nil {
		return result, err
	}
	from, to = from.UTC(), to.UTC()
	if from.IsZero() || !to.After(from) || to.Sub(from) > 32*24*time.Hour || runID == "" || len(runID) > 256 {
		return result, errors.New("invalid Prometheus collection window")
	}
	normalTotal := len(s.Queries)
	if s.Targets {
		normalTotal++
	}
	if s.Alerts {
		normalTotal++
	}
	total := normalTotal + len(windows)
	if total > 256 || s.QueryOffset > total {
		return result, errors.New("Prometheus operation plan exceeds its cursor bounds")
	}
	queries := map[string]Query{}
	for _, q := range s.Queries {
		queries[q.ID] = q
	}
	for _, window := range windows {
		q, exists := queries[window.QueryID]
		if !exists || q.TerminalExpression == "" || window.From.IsZero() || !window.To.After(window.From) || window.From.Before(from) || window.To.After(to) {
			return result, errors.New("terminal window must select an approved query inside the collection window")
		}
	}
	if s.QueryOffset >= len(s.Queries) && s.QueryTime != nil {
		return result, errors.New("terminal and diagnostic cursors cannot contain numeric continuation times")
	}
	ctx, cancel := c.boundedContext(ctx)
	defer cancel()
	result.Progress = monitorprovider.QueryProgress{TotalQueries: total, NextQuery: s.QueryOffset, NextTime: s.QueryTime}
	if s.QueryOffset < normalTotal {
		normal, err := c.Collect(ctx, s, from, to, runID)
		result = normal
		result.Progress.TotalQueries = total
		if err != nil || !normal.Progress.Complete {
			result.Progress.Complete = false
			return result, err
		}
		result.Progress.Complete = false
	}
	if result.Coverage == nil {
		result.Coverage = map[string]monitorprovider.Coverage{}
	}
	start := max(0, s.QueryOffset-normalTotal)
	for index := start; index < len(windows); index++ {
		window := windows[index]
		result.Progress.NextQuery = normalTotal + index
		result.Progress.NextTime = nil
		key := fmt.Sprintf("terminal:%d:%s", index, window.QueryID)
		if c.cfg.MaxPoints-len(result.Points) < c.cfg.MaxSeries {
			result.Coverage[key] = monitorprovider.Coverage{Status: "partial", From: window.From, To: window.To, Message: "Terminal query awaits the next point budget"}
			return result, nil
		}
		terminalSelection := s
		terminalSelection.Queries = []Query{queries[window.QueryID]}
		terminalSelection.Targets, terminalSelection.Alerts = false, false
		terminalSelection.QueryOffset, terminalSelection.QueryTime = 0, nil
		terminal, err := c.CollectTerminal(ctx, terminalSelection, window.From, window.To, runID)
		if err != nil {
			status := statusFor(err)
			if errors.Is(err, ErrBudget) || errors.Is(err, context.DeadlineExceeded) {
				status = "partial"
			}
			result.Coverage[key] = monitorprovider.Coverage{Status: status, From: window.From, To: window.To, Message: "Terminal query window remains uncommitted"}
			if status == "partial" {
				return result, nil
			}
			return result, err
		}
		result.Points = append(result.Points, terminal.Points...)
		if coverage, exists := terminal.Coverage[window.QueryID]; exists {
			result.Coverage[key] = coverage
		}
		result.Progress.NextQuery = normalTotal + index + 1
	}
	result.Progress.NextQuery = total
	result.Progress.NextTime = nil
	result.Progress.Complete = true
	return result, nil
}
