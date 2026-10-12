package scrub

import (
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestMemoMatchesUncachedScrubbing(t *testing.T) {
	inputs := []string{
		"",
		"ordinary text",
		"password=memo-canary&user=alice",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJlLXZhbHVl",
		`{"token":"memo-json-canary","name":"ok"}`,
		"https://user:pw@example.test/path?session=memo-url-canary&page=2",
		"jdbc:oracle:thin:scott/memo-dsn-canary@//db.example.test:1521/ORCL",
		"Transaction_Login_Step_01",
		strings.Repeat("x", 40),
	}
	memo := &Memo{}
	for round := 0; round < 2; round++ {
		for _, input := range inputs {
			if got, want := memo.Value(input), Value(input, Policy{}); got != want {
				t.Fatalf("Value(%q) = %q, want %q", input, got, want)
			}
			if got, want := memo.ValueLabel(input), ValueLabel(input, Policy{}); got != want {
				t.Fatalf("ValueLabel(%q) = %q, want %q", input, got, want)
			}
		}
	}
	// Each function keeps its own namespace: one input, two entries.
	if want := 2 * (len(inputs) - 1); len(memo.values) != want {
		t.Fatalf("memo entries = %d, want %d", len(memo.values), want)
	}
	var none *Memo
	for _, input := range inputs {
		if none.Value(input) != Value(input, Policy{}) || none.ValueLabel(input) != ValueLabel(input, Policy{}) {
			t.Fatalf("nil memo changed %q", input)
		}
	}
}

func TestMemoBounds(t *testing.T) {
	memo := &Memo{}
	for i := 0; i < maxMemoEntries+10; i++ {
		memo.Value("v" + strconv.Itoa(i))
	}
	if len(memo.values) != maxMemoEntries || memo.bytes > maxMemoBytes {
		t.Fatal("memo exceeded its entry or byte bound")
	}
	memo = &Memo{}
	memo.bytes = maxMemoBytes - 1
	if got := memo.Value("ordinary text"); got != "ordinary text" || len(memo.values) != 0 {
		t.Fatal("memo retained a value past its byte budget")
	}
}

func TestMemoConcurrentUse(t *testing.T) {
	memo := &Memo{}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				input := "password=c" + strconv.Itoa(i%20)
				if memo.Value(input) != Value(input, Policy{}) {
					t.Error("concurrent memo result differs")
					return
				}
			}
		}()
	}
	wg.Wait()
}
