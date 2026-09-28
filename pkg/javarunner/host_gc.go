package javarunner

import (
	"math"
	"strconv"
	"strings"
)

// GCPauseEvent is emitted once per host, not once per slot. Consumers correlate
// (host generation, sequence) with that host's admitted slots and transaction
// timestamps. It must never be subtracted from measured transaction durations.
type GCPauseEvent struct {
	Sequence      uint64 `json:"sequence"`
	StartUnixNano int64  `json:"start_unix_nano"`
	DurationNano  int64  `json:"duration_nano"`
}
type GCPauseSnapshot struct {
	Status  string         `json:"status"`
	Evicted uint64         `json:"evicted"`
	Events  []GCPauseEvent `json:"events"`
}

func validGCPauses(s GCPauseSnapshot) bool {
	if s.Status != "available" && s.Status != "unsupported" && s.Status != "failed" {
		return false
	}
	if len(s.Events) > 128 || s.Evicted > math.MaxInt64 || (s.Status == "unsupported" && (len(s.Events) != 0 || s.Evicted != 0)) {
		return false
	}
	for i, e := range s.Events {
		if e.Sequence != s.Evicted+uint64(i)+1 || e.Sequence > math.MaxInt64 || e.StartUnixNano <= 0 || e.DurationNano < 0 || e.DurationNano > math.MaxInt64-e.StartUnixNano {
			return false
		}
	}
	return s.Evicted == 0 || len(s.Events) == 128
}

func parseGCPauses(fields map[string]string) (*GCPauseSnapshot, error) {
	status, hasStatus := fields["gc_pause_status"]
	body, hasEvents := fields["gc_pause_events"]
	evicted, hasEvicted := fields["gc_pause_evicted"]
	if !hasStatus && !hasEvents && !hasEvicted {
		return nil, nil
	} // older explicit pilot
	if !hasStatus || !hasEvents || !hasEvicted || len(body) > 8192 {
		return nil, ErrPooledFrame
	}
	n, err := strconv.ParseUint(evicted, 10, 63)
	if err != nil {
		return nil, ErrPooledFrame
	}
	s := &GCPauseSnapshot{Status: status, Evicted: n}
	if body != "" {
		if !strings.HasSuffix(body, "\n") {
			return nil, ErrPooledFrame
		}
		for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
			values := strings.Split(line, ",")
			if len(values) != 3 || len(s.Events) >= 128 {
				return nil, ErrPooledFrame
			}
			sequence, e1 := strconv.ParseUint(values[0], 10, 63)
			start, e2 := strconv.ParseInt(values[1], 10, 64)
			duration, e3 := strconv.ParseInt(values[2], 10, 64)
			if e1 != nil || e2 != nil || e3 != nil {
				return nil, ErrPooledFrame
			}
			s.Events = append(s.Events, GCPauseEvent{sequence, start, duration})
		}
	}
	if !validGCPauses(*s) {
		return nil, ErrPooledFrame
	}
	return s, nil
}
