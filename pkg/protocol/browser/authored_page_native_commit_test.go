package browser

import (
	"context"
	playwright "github.com/mxschmitt/playwright-go"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type authoredIdentityFrameFixture struct {
	playwright.Frame
	evaluate func() (any, error)
}

func (f *authoredIdentityFrameFixture) Evaluate(string, ...any) (any, error) { return f.evaluate() }
func TestBrowserNativeAuthoredIdentityReadIsBoundedAfterCancellation(t *testing.T) {
	b := &Browser{}
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	frame := &authoredIdentityFrameFixture{evaluate: func() (any, error) {
		calls.Add(1)
		close(entered)
		<-release
		return []any{strings.Repeat("a", 32), strings.Repeat("b", 32)}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := b.readNativeAuthoredIdentity(ctx, frame, "fixture"); done <- err }()
	<-entered
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
	if identity, err := b.readNativeAuthoredIdentity(context.Background(), frame, "fixture"); err == nil || identity.document != "" {
		t.Fatal("second pending read admitted", identity, err)
	}
	if calls.Load() != 1 {
		t.Fatal("unbounded native readers", calls.Load())
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		pending := b.authoredIdentityReadPending
		b.mu.Unlock()
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finished reader retained slot")
		}
		time.Sleep(time.Millisecond)
	}
	frame = &authoredIdentityFrameFixture{evaluate: func() (any, error) { return []any{strings.Repeat("c", 32), strings.Repeat("d", 32)}, nil }}
	identity, err := b.readNativeAuthoredIdentity(context.Background(), frame, "fixture")
	if err != nil || identity.document != strings.Repeat("c", 32) {
		t.Fatal("late canceled identity reused", identity, err)
	}
}
func TestBrowserNativeAuthoredIdentityRejectsMissingAndMalformedTokens(t *testing.T) {
	for _, value := range []any{nil, map[string]any{}, []any{"short", "short"}, []any{strings.Repeat("a", 32), strings.Repeat("z", 32)}} {
		b := &Browser{}
		frame := &authoredIdentityFrameFixture{evaluate: func() (any, error) { return value, nil }}
		if identity, err := b.readNativeAuthoredIdentity(context.Background(), frame, "fixture"); err == nil || identity.document != "" {
			t.Fatal(value, identity, err)
		}
	}
	b := &Browser{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	frame := &authoredIdentityFrameFixture{evaluate: func() (any, error) { t.Error("canceled read reached native runtime"); return nil, nil }}
	if _, err := b.readNativeAuthoredIdentity(ctx, frame, "fixture"); err != context.Canceled {
		t.Fatal(err)
	}
}
