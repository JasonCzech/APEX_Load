package javarunner

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDedicatedHostContextCanonicalRoundTrip(t *testing.T) {
	c := DedicatedHostContext{Contract: DedicatedHostContextContract, SessionID: "vuser-0", GroupName: "checkout group", ScenarioID: "scenario", IgnoreThinkTime: true, ThinkTimeLimitMS: 1500}
	raw, e := EncodeDedicatedHostContext(c)
	if e != nil {
		t.Fatal(e)
	}
	got, e := DecodeDedicatedHostContext(raw)
	if e != nil || got != c {
		t.Fatal(got, e)
	}
	data, _ := base64.RawURLEncoding.DecodeString(raw)
	for _, bad := range []string{"", strings.Repeat("a", 8193), raw + "=", base64.RawURLEncoding.EncodeToString(append(data, []byte("{}")...)), base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(data), `"vuser_id":0`, `"vuser_id":0,"vuser_id":0`, 1))), base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(data), `"vuser_id":0,`, "", 1)))} {
		if _, e := DecodeDedicatedHostContext(bad); e == nil {
			t.Fatal("malformed context accepted")
		}
	}
	for _, mutate := range []func(*DedicatedHostContext){func(c *DedicatedHostContext) { c.Contract = "wrong" }, func(c *DedicatedHostContext) { c.SessionID = "bad/id" }, func(c *DedicatedHostContext) { c.VUserID = -1 }, func(c *DedicatedHostContext) { c.VUserID = 2147483648 }, func(c *DedicatedHostContext) { c.GroupName = "bad\nname" }, func(c *DedicatedHostContext) { c.ScenarioID = strings.Repeat("a", 1025) }, func(c *DedicatedHostContext) { c.ThinkTimeLimitMS = -1 }} {
		bad := c
		mutate(&bad)
		if _, e := EncodeDedicatedHostContext(bad); e == nil {
			t.Fatal("invalid context accepted", bad)
		}
	}
}
