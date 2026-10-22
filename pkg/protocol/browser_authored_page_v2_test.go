package protocol

import "testing"

func TestBrowserAuthoredPageNativeCommitContract(t *testing.T) {
	b := authoredPageFixture()
	b.Version = 2
	b.Association = "native_navigation_commit"
	b.NavigationSequence = 1
	if !b.Valid() || b.Clone() == nil {
		t.Fatal("native commit refused")
	}
	for _, change := range []func(*BrowserAuthoredPageBinding){
		func(b *BrowserAuthoredPageBinding) { b.NavigationSequence = 0 },
		func(b *BrowserAuthoredPageBinding) { b.NavigationSequence = 9007199254740992 },
		func(b *BrowserAuthoredPageBinding) { b.Version = 1 },
		func(b *BrowserAuthoredPageBinding) { b.Association = "cdp_loader_join" },
	} {
		copy := *b
		change(&copy)
		if copy.Valid() {
			t.Fatal("mixed native commit proof accepted", copy)
		}
	}
	if BrowserAuthoredPageID(b.AuthoredStep) != b.PageID {
		t.Fatal("native association changed authored definition identity")
	}
}
