package protocol

import (
	"sync"
	"testing"
)

func TestReplayHAROptionsAndBodyBudget(t *testing.T) {
	for _, bodies := range []string{"none", "request", "response", "both"} {
		o, err := (ReplayHAROptions{Bodies: bodies}).Normalize()
		if err != nil || o.MaxBodyBytes != 65536 || o.MaxTotalBodyBytes != 4194304 {
			t.Fatalf("%+v %v", o, err)
		}
		if o.KeepBody(true) != (bodies == "request" || bodies == "both") || o.KeepBody(false) != (bodies == "response" || bodies == "both") {
			t.Fatal("body selection")
		}
	}
	for _, o := range []ReplayHAROptions{{Bodies: "all"}, {MaxBodyBytes: -1}, {MaxBodyBytes: 1<<20 + 1}, {MaxTotalBodyBytes: 16<<20 + 1}} {
		if _, err := o.Normalize(); err == nil {
			t.Fatalf("accepted invalid %+v", o)
		}
	}
	c, _ := NewReplayHARCapture(ReplayHAROptions{Bodies: "both", MaxBodyBytes: 8, MaxTotalBodyBytes: 8})
	for _, tc := range []struct {
		data, mime string
		complete   bool
		state      string
	}{{"123456789", "text/plain", true, "size_limit"}, {"x", "application/octet-stream", true, "binary"}, {"x", "text/plain", false, "unavailable"}, {"1234", "text/plain", true, "retained"}, {"12345", "text/plain", true, "total_limit"}, {"", "text/plain", true, "retained"}} {
		got := c.Body([]byte(tc.data), tc.mime, tc.complete)
		if got.State != tc.state {
			t.Fatalf("%+v want %s", got, tc.state)
		}
		if tc.state != "retained" && got.Text != "" {
			t.Fatal("omitted body leaked text")
		}
	}
	c, _ = NewReplayHARCapture(ReplayHAROptions{MaxBodyBytes: 8, MaxTotalBodyBytes: 8})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.Body([]byte("1234"), "text/plain", true) }()
	}
	wg.Wait()
	if c.used != 8 {
		t.Fatalf("budget=%d", c.used)
	}
}
func TestReplayHARCloneDetachesBody(t *testing.T) {
	e := NetworkEntry{HAR: NewReplayHARExchange()}
	e.HAR.RequestBody = &ReplayHARBody{Text: "original", State: "retained"}
	copy := CloneNetworkEntries([]NetworkEntry{e})
	copy[0].HAR.RequestBody.Text = "changed"
	if e.HAR.RequestBody.Text != "original" {
		t.Fatal("body alias")
	}
}
