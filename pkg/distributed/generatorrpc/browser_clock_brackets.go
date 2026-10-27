package generatorrpc

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	browserClockBracketMetadataKey  = "x-apexload-browser-clock-bracket"
	maxTrackedBrowserBracketTestIDs = 64
)

// ObservedBrowserClockBracket records one generator BrowserSourceJournal
// monotonic sample bracketed by the controller's RPC send and receive times.
type ObservedBrowserClockBracket struct {
	TestID     string
	Sample     protocol.BrowserClockBracketSample
	SentAt     time.Time
	ReceivedAt time.Time
}

type browserClockBracketHeaderPayload struct {
	TestID    string                      `json:"test_id"`
	Authority protocol.BrowserClockDomain `json:"authority"`
	OwnerID   string                      `json:"owner_id"`
	MonoNS    int64                       `json:"mono_ns"`
}

func encodeBrowserClockBracketHeader(testID string, sample protocol.BrowserClockBracketSample) (string, bool) {
	if testID == "" || !sample.Valid() {
		return "", false
	}
	raw, err := json.Marshal(browserClockBracketHeaderPayload{
		TestID:    testID,
		Authority: sample.Authority,
		OwnerID:   sample.OwnerID,
		MonoNS:    sample.MonoNS,
	})
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func decodeBrowserClockBracketHeader(raw string) (string, protocol.BrowserClockBracketSample, bool) {
	var payload browserClockBracketHeaderPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", protocol.BrowserClockBracketSample{}, false
	}
	sample := protocol.BrowserClockBracketSample{
		Authority: payload.Authority,
		OwnerID:   payload.OwnerID,
		MonoNS:    payload.MonoNS,
	}
	if payload.TestID == "" || !sample.Valid() {
		return "", protocol.BrowserClockBracketSample{}, false
	}
	return payload.TestID, sample, true
}

func (service *Service) prepareTestBrowserClockBracketOnCommit(ctx context.Context, testID string) (protocol.BrowserClockBracketSample, bool) {
	if service == nil || service.archiveManager == nil || testID == "" {
		return protocol.BrowserClockBracketSample{}, false
	}
	if writer := service.archiveManager.writer(testID); writer != nil && writer.browserDocumentProofEnabled() {
		if journal, err := writer.BrowserDocumentSourceJournal(); err == nil && journal != nil {
			service.archiveManager.registerBrowserJournal(testID, journal)
		}
	}
	sample, ok := service.archiveManager.sampleBrowserClockBracket(testID)
	if ok && ctx != nil {
		if encoded, valid := encodeBrowserClockBracketHeader(testID, sample); valid {
			_ = grpc.SetHeader(ctx, metadata.Pairs(browserClockBracketMetadataKey, encoded))
		}
	}
	return sample, ok
}

func (service *Service) sendTestBrowserClockBracketHeader(ctx context.Context, testID string) {
	if service == nil || service.archiveManager == nil || testID == "" || ctx == nil {
		return
	}
	sample, ok := service.archiveManager.sampleBrowserClockBracket(testID)
	if !ok {
		return
	}
	if encoded, valid := encodeBrowserClockBracketHeader(testID, sample); valid {
		_ = grpc.SetHeader(ctx, metadata.Pairs(browserClockBracketMetadataKey, encoded))
	}
}

func (service *Service) activeTestsBrowserClockBracketMetadata(testIDs []string) metadata.MD {
	if service == nil || service.archiveManager == nil || len(testIDs) == 0 {
		return nil
	}
	var values []string
	for _, testID := range testIDs {
		sample, ok := service.archiveManager.sampleBrowserClockBracket(testID)
		if !ok {
			continue
		}
		if encoded, valid := encodeBrowserClockBracketHeader(testID, sample); valid {
			values = append(values, encoded)
		}
	}
	if len(values) == 0 {
		return nil
	}
	return metadata.MD{browserClockBracketMetadataKey: values}
}

func safeStreamHeader(stream interface{ Header() (metadata.MD, error) }) (md metadata.MD, err error) {
	if stream == nil {
		return nil, nil
	}
	defer func() {
		if recover() != nil {
			md, err = nil, nil
		}
	}()
	return stream.Header()
}

func (c *Client) observeBrowserClockBracketMetadata(md metadata.MD, sentAt, receivedAt time.Time) []ObservedBrowserClockBracket {
	if c == nil || len(md) == 0 || sentAt.IsZero() || receivedAt.IsZero() || receivedAt.Before(sentAt) {
		return nil
	}
	values := md.Get(browserClockBracketMetadataKey)
	if len(values) == 0 {
		return nil
	}
	out := make([]ObservedBrowserClockBracket, 0, len(values))
	for _, raw := range values {
		testID, sample, ok := decodeBrowserClockBracketHeader(raw)
		if !ok {
			continue
		}
		observed := ObservedBrowserClockBracket{
			TestID:     testID,
			Sample:     sample,
			SentAt:     sentAt,
			ReceivedAt: receivedAt,
		}
		out = append(out, observed)
		c.recordBrowserClockBracket(observed)
	}
	return out
}

func (c *Client) recordBrowserClockBracket(observed ObservedBrowserClockBracket) {
	if c == nil || c.connection == nil || observed.TestID == "" || !observed.Sample.Valid() {
		return
	}
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	if c.connection.browserBrackets == nil {
		c.connection.browserBrackets = make(map[string][]ObservedBrowserClockBracket)
	}
	existing, exists := c.connection.browserBrackets[observed.TestID]
	if !exists {
		for len(c.connection.browserBracketOrder) >= maxTrackedBrowserBracketTestIDs {
			evict := c.connection.browserBracketOrder[0]
			c.connection.browserBracketOrder = c.connection.browserBracketOrder[1:]
			delete(c.connection.browserBrackets, evict)
		}
		c.connection.browserBracketOrder = append(c.connection.browserBracketOrder, observed.TestID)
	}
	if len(existing) >= protocol.MaxBrowserClockBracketsPerOwner {
		return
	}
	c.connection.browserBrackets[observed.TestID] = append(existing, observed)
}

func (c *Client) BrowserClockBrackets(testID string) []ObservedBrowserClockBracket {
	if c == nil || c.connection == nil || testID == "" {
		return nil
	}
	c.connection.mu.Lock()
	defer c.connection.mu.Unlock()
	brackets := c.connection.browserBrackets[testID]
	if len(brackets) == 0 {
		return nil
	}
	return append([]ObservedBrowserClockBracket(nil), brackets...)
}
