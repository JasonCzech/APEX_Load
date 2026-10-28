package protocol

import (
	"math"
	"time"
)

// BrowserOutcomeClock is an optional v1 observation of the actual post-action
// wait on the browser host. Its observed end precedes post-action delay and
// diagnostic capture. It is not the gesture's dispatch clock or a target clock.
// DeadlineAt is the effective wait deadline, which a parent context may shorten
// relative to BudgetMS. Historical evidence has no inferred clock.
type BrowserOutcomeClock struct {
	Version    int       `json:"version"`
	StartedAt  time.Time `json:"started_at"`
	DeadlineAt time.Time `json:"deadline_at"`
	ObservedAt time.Time `json:"observed_at"`
	BudgetMS   int       `json:"budget_ms"`
	ElapsedMS  float64   `json:"elapsed_ms"`
}

func (c *BrowserOutcomeClock) Valid() bool {
	if c == nil || c.Version != 1 || c.BudgetMS <= 0 || c.BudgetMS > 86400000 || math.IsNaN(c.ElapsedMS) || math.IsInf(c.ElapsedMS, 0) || c.ElapsedMS < 0 || c.ElapsedMS > 86400000 {
		return false
	}
	if c.StartedAt.IsZero() || c.StartedAt.Year() < 1970 || c.ObservedAt.Year() > 9999 || c.DeadlineAt.Year() > 9999 || c.StartedAt.Location() != time.UTC || c.ObservedAt.Location() != time.UTC || c.DeadlineAt.Location() != time.UTC || c.ObservedAt.Before(c.StartedAt) || !c.DeadlineAt.After(c.StartedAt) {
		return false
	}
	// Wall-clock jumps cannot turn a local wait into timing-qualified evidence.
	wall := c.ObservedAt.Sub(c.StartedAt).Seconds() * 1000
	return math.Abs(wall-c.ElapsedMS) <= 1 && c.DeadlineAt.Sub(c.StartedAt) <= time.Duration(c.BudgetMS)*time.Millisecond+time.Millisecond
}

func (c *BrowserOutcomeClock) Clone() *BrowserOutcomeClock {
	if !c.Valid() {
		return nil
	}
	out := *c
	return &out
}

func browserOutcomeTime(t time.Time) bool {
	return !t.IsZero() && t.Location() == time.UTC && t.Year() >= 1970 && t.Year() <= 9999
}

func (c *BrowserOutcomeClock) Begin(budget time.Duration) {
	if c == nil {
		return
	}
	started := time.Now().UTC()
	budgetMS := max(1, int(budget/time.Millisecond))
	budgetDur := time.Duration(budgetMS) * time.Millisecond
	*c = BrowserOutcomeClock{
		Version:    1,
		StartedAt:  started,
		DeadlineAt: started.Add(budgetDur),
		ObservedAt: started,
		BudgetMS:   budgetMS,
	}
}

func (c *BrowserOutcomeClock) Finish() {
	if c == nil || c.StartedAt.IsZero() {
		return
	}
	observed := time.Now().UTC()
	if observed.Before(c.StartedAt) {
		observed = c.StartedAt
	}
	c.ObservedAt = observed
	c.ElapsedMS = c.ObservedAt.Sub(c.StartedAt).Seconds() * 1000
}

func (c *BrowserOutcomeClock) ClampToDeadline() {
	if c == nil || c.StartedAt.IsZero() {
		return
	}
	if c.ObservedAt.Before(c.DeadlineAt) {
		c.ObservedAt = c.DeadlineAt
		c.ElapsedMS = c.ObservedAt.Sub(c.StartedAt).Seconds() * 1000
	}
}
