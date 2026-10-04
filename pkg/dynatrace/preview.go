package dynatrace

import (
	"context"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Preview completes a bounded setup query against a frozen recent window.
// Unlike durable collection, it owns and cancels any unfinished query handle.
func (c *Client) Preview(ctx context.Context, s Selection, from, to time.Time) (Result, error) {
	result := Result{Points: []analysis.MetricPoint{}, Evidence: []Diagnostic{}, Topology: []Entity{}, Coverage: map[string]Coverage{}}
	if err := s.Validate(); err != nil {
		return result, err
	}
	if from.Location() != time.UTC || to.Location() != time.UTC || from.IsZero() || !to.After(from) || to.Sub(from) > time.Minute {
		return result, errors.New("preview requires a bounded frozen UTC window")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// Share transport/authentication/request quotas without mutating the caller's
	// client, and apply the frozen complete-operation scan budget before execute.
	previewClient := *c
	client := &previewClient
	scanReserved := int64(0)
	client.scanHook = func(attempt string, bytes int64) error {
		if bytes < 0 || bytes > c.cfg.MaxRunScanBytes-scanReserved {
			return ErrBudget
		}
		if c.scanHook != nil {
			if err := c.scanHook(attempt, bytes); err != nil {
				return err
			}
		}
		scanReserved += bytes
		return nil
	}
	s.QueryOffset, s.Cursor = 0, ""
	pending := ""
	defer func() {
		if pending != "" {
			if cursor, err := decodeCursor(pending); err == nil {
				_ = client.cancelAbandoned(cursor)
			}
		}
	}()
	known, scanned, reserved := true, int64(0), int64(0)
	truncated := false
	for pass := 0; pass < 64; pass++ {
		next, err := client.Collect(ctx, s, from, to, "")
		if err == nil || next.Progress.Cursor != "" {
			pending = next.Progress.Cursor
		}
		for key, coverage := range next.Coverage {
			old := result.Coverage[key]
			coverage.Points += old.Points
			coverage.Evidence += old.Evidence
			coverage.Truncated = coverage.Truncated || old.Truncated
			if coverage.Truncated && coverage.Status == "complete" {
				coverage.Status = "partial"
			}
			result.Coverage[key] = coverage
		}
		points, evidence, topology := min(len(next.Points), 512-len(result.Points)), min(len(next.Evidence), 100-len(result.Evidence)), min(len(next.Topology), 200-len(result.Topology))
		result.Points = append(result.Points, next.Points[:points]...)
		result.Evidence = append(result.Evidence, next.Evidence[:evidence]...)
		result.Topology = append(result.Topology, next.Topology[:topology]...)
		truncated = truncated || points < len(next.Points) || evidence < len(next.Evidence) || topology < len(next.Topology)
		known = known && next.Progress.QueryCostKnown
		scanned += next.Progress.ScannedBytes
		reserved += next.Progress.ScanReservationBytes
		result.Progress = next.Progress
		result.Progress.Cursor, result.Progress.CursorExpiresAt, result.Progress.ScanReservationIDs, result.Progress.ScanSettlements = "", nil, nil, nil
		result.Progress.QueryCostKnown, result.Progress.ScannedBytes, result.Progress.ScanReservationBytes = known, scanned, reserved
		if truncated {
			result.Coverage["retention"] = Coverage{Status: "partial", From: from, To: to, Truncated: true, Message: "preview result retention limit reached"}
		}
		if err != nil {
			return result, err
		}
		if next.Progress.Complete {
			return result, nil
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if next.Progress.NextQuery == s.QueryOffset && next.Progress.Cursor == s.Cursor {
			return result, errors.New("preview could not advance bounded query progress")
		}
		s.QueryOffset, s.Cursor = next.Progress.NextQuery, next.Progress.Cursor
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}
	return result, errors.New("preview query continuation limit reached")
}
