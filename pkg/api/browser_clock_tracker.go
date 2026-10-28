package api

import (
	"cmp"
	"slices"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type browserClockBracketProvider interface {
	BrowserClockBrackets(testID string) []generatorrpc.ObservedBrowserClockBracket
}

type browserClockTracker struct {
	mu        sync.Mutex
	authority protocol.BrowserClockDomain
	startedAt time.Time
	owners    map[string][]protocol.BrowserClockBracket
}

func (t *ManagedTest) initBrowserClockTracker() *browserClockTracker {
	if t == nil {
		return nil
	}
	if t.browserClockTracker != nil {
		return t.browserClockTracker
	}
	authority, err := protocol.NewBrowserClockDomain(t.ID)
	if err != nil {
		return nil
	}
	t.browserClockTracker = &browserClockTracker{
		authority: authority,
		startedAt: time.Now(),
		owners:    make(map[string][]protocol.BrowserClockBracket),
	}
	return t.browserClockTracker
}

func (t *ManagedTest) recordBrowserClockBrackets(brackets []generatorrpc.ObservedBrowserClockBracket) {
	if t == nil || t.browserClockTracker == nil || len(brackets) == 0 {
		return
	}
	tracker := t.browserClockTracker
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if !tracker.authority.Valid() || tracker.startedAt.IsZero() {
		return
	}
	for _, b := range brackets {
		if b.TestID != t.ID || !b.Sample.Valid() || b.SentAt.IsZero() || b.ReceivedAt.IsZero() || b.ReceivedAt.Before(b.SentAt) {
			continue
		}
		sendNS := b.SentAt.Sub(tracker.startedAt).Nanoseconds()
		recvNS := b.ReceivedAt.Sub(tracker.startedAt).Nanoseconds()
		if recvNS < 0 {
			continue
		}
		if sendNS < 0 {
			sendNS = 0
		}
		entry := protocol.BrowserClockBracket{
			Authority:        b.Sample.Authority,
			OwnerID:          b.Sample.OwnerID,
			ControllerSendNS: sendNS,
			ControllerRecvNS: recvNS,
			OwnerMonoNS:      b.Sample.MonoNS,
		}
		existing := tracker.owners[entry.OwnerID]
		duplicate := false
		for _, prior := range existing {
			if prior == entry {
				duplicate = true
				break
			}
		}
		if duplicate || len(existing) >= protocol.MaxBrowserClockBracketsPerOwner {
			continue
		}
		existing = append(existing, entry)
		slices.SortFunc(existing, func(a, b protocol.BrowserClockBracket) int {
			if c := cmp.Compare(a.OwnerMonoNS, b.OwnerMonoNS); c != 0 {
				return c
			}
			if c := cmp.Compare(a.ControllerSendNS, b.ControllerSendNS); c != 0 {
				return c
			}
			return cmp.Compare(a.ControllerRecvNS, b.ControllerRecvNS)
		})
		tracker.owners[entry.OwnerID] = existing
	}
}

func (t *ManagedTest) harvestClientBrowserClockBrackets(clients map[string]generatorClient) {
	if t == nil || t.browserClockTracker == nil || len(clients) == 0 {
		return
	}
	for _, client := range clients {
		if provider, ok := client.(browserClockBracketProvider); ok {
			t.recordBrowserClockBrackets(provider.BrowserClockBrackets(t.ID))
		}
	}
}

func (t *ManagedTest) browserDistributedClockEnvelope() *protocol.BrowserDistributedClockEnvelope {
	if t == nil || t.browserClockTracker == nil {
		return nil
	}
	tracker := t.browserClockTracker
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if !tracker.authority.Valid() || tracker.startedAt.IsZero() || len(tracker.owners) == 0 {
		return nil
	}
	owners := make(map[string][]protocol.BrowserClockBracket, len(tracker.owners))
	for ownerID, brackets := range tracker.owners {
		owners[ownerID] = append([]protocol.BrowserClockBracket(nil), brackets...)
	}
	return &protocol.BrowserDistributedClockEnvelope{
		Version:            1,
		Controller:         tracker.authority,
		AuthorityStartedAt: tracker.startedAt.UTC(),
		MaxSkewPPM:         protocol.DefaultBrowserClockMaxSkewPPM,
		MaxBracketRTTNS:    protocol.DefaultBrowserClockMaxBracketRTTNS,
		MaxBracketGapNS:    protocol.DefaultBrowserClockMaxBracketGapNS,
		Owners:             owners,
	}
}
