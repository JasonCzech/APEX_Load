package playwright

import (
	"sync"
	"testing"
)

func apexNavigationEvent(request Request) map[string]any {
	event := map[string]any{"url": "https://fixture.invalid/same", "name": ""}
	if request != nil {
		event["newDocument"] = map[string]any{"request": &channel{object: request}}
	}
	return event
}

func TestAPEXNavigationCommitNativeIdentityAndInvalidation(t *testing.T) {
	f := &frameImpl{}
	if got := APEXFrameNavigationCommit(f); got.Sequence != 0 || got.Request != nil {
		t.Fatal("initializer inferred a commit", got)
	}
	if got := APEXFrameNavigationCommit(nil); got.Sequence != 0 || got.Request != nil {
		t.Fatal(got)
	}
	var empty *frameImpl
	if got := APEXFrameNavigationCommit(empty); got.Sequence != 0 || got.Request != nil {
		t.Fatal(got)
	}
	first, second := &requestImpl{}, &requestImpl{}
	f.onFrameNavigated(apexNavigationEvent(first))
	snapshot := APEXFrameNavigationCommit(f)
	if snapshot.Sequence != 1 || snapshot.Request != first {
		t.Fatal(snapshot)
	}
	f.onFrameNavigated(apexNavigationEvent(second))
	if snapshot.Request != first || snapshot.Sequence != 1 {
		t.Fatal("snapshot mutated")
	}
	if got := APEXFrameNavigationCommit(f); got.Sequence != 2 || got.Request != second {
		t.Fatal("same URL confused native request", got)
	}
	for _, tc := range []struct {
		name  string
		event map[string]any
	}{
		{"same document", apexNavigationEvent(nil)},
		{"failed", map[string]any{"url": "https://fixture.invalid/same", "name": "", "error": "failed", "newDocument": map[string]any{"request": &channel{object: first}}}},
		{"missing request", map[string]any{"url": "https://fixture.invalid/same", "name": "", "newDocument": map[string]any{}}},
		{"unknown channel", map[string]any{"url": "https://fixture.invalid/same", "name": "", "newDocument": map[string]any{"request": &channel{object: f}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.onFrameNavigated(apexNavigationEvent(first))
			before := APEXFrameNavigationCommit(f)
			f.onFrameNavigated(tc.event)
			after := APEXFrameNavigationCommit(f)
			if after.Sequence != before.Sequence+1 || after.Request != nil {
				t.Fatalf("invalidated request retained: %+v", after)
			}
		})
	}
}
func TestAPEXNavigationCommitConcurrentSnapshots(t *testing.T) {
	f := &frameImpl{}
	a, b := &requestImpl{}, &requestImpl{}
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; i < 10000; i++ {
			if i%2 == 0 {
				f.onFrameNavigated(apexNavigationEvent(a))
			} else {
				f.onFrameNavigated(apexNavigationEvent(b))
			}
		}
	})
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			for n := 0; n < 10000; n++ {
				snapshot := APEXFrameNavigationCommit(f)
				if snapshot.Sequence == 0 {
					if snapshot.Request != nil {
						t.Error("inferred initial request")
					}
					continue
				}
				expected := Request(b)
				if snapshot.Sequence%2 == 1 {
					expected = a
				}
				if snapshot.Request != expected {
					t.Error("torn sequence/request snapshot")
					return
				}
			}
		})
	}
	wg.Wait()
	if got := APEXFrameNavigationCommit(f); got.Sequence != 10000 || got.Request != b {
		t.Fatal(got)
	}
}
