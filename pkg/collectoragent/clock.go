package collectoragent

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// The controller stamps its heartbeat replies with the time it received the
// request and the time it replied, in Unix nanoseconds on its clock. With the
// collector's own send and receive times they give a four-timestamp offset.
const (
	ClockReceivedHeader = "X-Apex-Clock-Received"
	ClockSentHeader     = "X-Apex-Clock-Sent"
)

// clockEstimateMaxAge is how long a heartbeat's measurement describes points;
// older points are stamped with an unknown offset instead.
const clockEstimateMaxAge = 5 * time.Minute

// collectorClock holds the latest heartbeat's offset measurement.
type collectorClock struct {
	mu         sync.Mutex
	estimate   analysis.ClockEstimate
	measuredAt time.Time
	known      bool
}

// observe records one heartbeat exchange. sent and received are this
// collector's times; the reply's headers carry the controller's.
func (c *collectorClock) observe(sent, received time.Time, header http.Header) {
	controllerReceived, ok1 := unixNanoHeader(header, ClockReceivedHeader)
	controllerSent, ok2 := unixNanoHeader(header, ClockSentHeader)
	if !ok1 || !ok2 {
		return
	}
	estimate, ok := analysis.EstimateClockOffset(sent, controllerReceived, controllerSent, received)
	if !ok {
		return
	}
	c.mu.Lock()
	c.estimate, c.measuredAt, c.known = estimate, received, true
	c.mu.Unlock()
}

// timing describes points read at now: the latest measured offset when it is
// recent, otherwise an unknown one.
func (c *collectorClock) timing(now time.Time) *analysis.PointTiming {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.known || now.Sub(c.measuredAt) > clockEstimateMaxAge || now.Before(c.measuredAt) {
		return &analysis.PointTiming{ClockSource: analysis.ClockSourceCollector, OffsetStatus: analysis.OffsetUnknown}
	}
	return analysis.MeasuredTiming(analysis.ClockSourceCollector, c.estimate)
}

func unixNanoHeader(header http.Header, name string) (time.Time, bool) {
	raw := header.Get(name)
	if raw == "" {
		return time.Time{}, false
	}
	nanos, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || nanos <= 0 {
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
}
