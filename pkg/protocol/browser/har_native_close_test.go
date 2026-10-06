package browser

import (
	"errors"
	playwright "github.com/mxschmitt/playwright-go"
	"testing"
)

type harUnsupportedNativeCloseFixture struct {
	playwright.BrowserContext
	calls int
}

func (f *harUnsupportedNativeCloseFixture) Close(...playwright.BrowserContextCloseOptions) error {
	f.calls++
	return nil
}

type harSupportedNativeCloseFixture struct {
	harUnsupportedNativeCloseFixture
	ownedCalls int
	err        error
}

func (f *harSupportedNativeCloseFixture) CloseHAROwned(...playwright.BrowserContextCloseOptions) error {
	f.ownedCalls++
	return f.err
}
func TestHAROwnershipNativeCloseCapabilityIsExplicit(t *testing.T) {
	old := &harUnsupportedNativeCloseFixture{}
	if err := closeOwnedHARBrowserContext(old); !errors.Is(err, errHAROwnershipUnproven) || old.calls != 0 {
		t.Fatal("ordinary early-nil Close was accepted as native proof", err)
	}
	if err := closeOwnedHARBrowserContext(nil); !errors.Is(err, errHAROwnershipUnproven) {
		t.Fatal("nil native context accepted", err)
	}
	failure := errors.New("synthetic complete close failure")
	fresh := &harSupportedNativeCloseFixture{err: failure}
	if err := closeOwnedHARBrowserContext(fresh); !errors.Is(err, failure) || fresh.ownedCalls != 1 || fresh.calls != 0 {
		t.Fatal("owned result/capability lost", err)
	}
	fresh.err = nil
	if err := closeOwnedHARBrowserContext(fresh); err != nil || fresh.ownedCalls != 2 || fresh.calls != 0 {
		t.Fatal("owned nil result unavailable", err)
	}
}
