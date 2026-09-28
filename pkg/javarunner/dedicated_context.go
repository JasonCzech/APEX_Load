package javarunner

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"unicode"
	"unicode/utf8"
)

const DedicatedHostContextContract = "apexload.java-host-context/v1"

var ErrDedicatedHostContext = errors.New("java_host_context_invalid")
var dedicatedSessionName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// DedicatedHostContext binds the Vuser identity before construction. It carries
// no arbitrary environment, code, credentials or action parameters. The broker
// must bind its canonical encoding into the approved immutable pod request.
// Workload images can therefore be shared while each host gets a distinct
// session. This is preparation metadata, not permission to initialize Java.
type DedicatedHostContext struct {
	Contract         string `json:"contract"`
	SessionID        string `json:"session_id"`
	VUserID          int64  `json:"vuser_id"`
	GroupName        string `json:"group_name"`
	ScenarioID       string `json:"scenario_id"`
	IgnoreThinkTime  bool   `json:"ignore_think_time"`
	ThinkTimeLimitMS int64  `json:"think_time_limit_ms"`
}

func (c DedicatedHostContext) Validate() error {
	if c.Contract != DedicatedHostContextContract || !dedicatedSessionName.MatchString(c.SessionID) || c.VUserID < 0 || c.VUserID > 2147483647 || c.ThinkTimeLimitMS < 0 {
		return ErrDedicatedHostContext
	}
	for _, v := range []string{c.GroupName, c.ScenarioID} {
		if len(v) > 1024 || v == "" || !utf8.ValidString(v) {
			return ErrDedicatedHostContext
		}
		for _, r := range v {
			if unicode.IsControl(r) {
				return ErrDedicatedHostContext
			}
		}
	}
	return nil
}
func EncodeDedicatedHostContext(c DedicatedHostContext) (string, error) {
	if e := c.Validate(); e != nil {
		return "", e
	}
	data, e := json.Marshal(c)
	if e != nil {
		return "", ErrDedicatedHostContext
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// Canonical round-trip equality rejects duplicate keys, omitted required values,
// alternate spellings and unknown fields before they can affect identity hashes.
func DecodeDedicatedHostContext(raw string) (DedicatedHostContext, error) {
	var c DedicatedHostContext
	if len(raw) < 1 || len(raw) > 8192 {
		return c, ErrDedicatedHostContext
	}
	data, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return c, ErrDedicatedHostContext
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF || c.Validate() != nil {
		return DedicatedHostContext{}, ErrDedicatedHostContext
	}
	canonical, e := EncodeDedicatedHostContext(c)
	if e != nil || canonical != raw {
		return DedicatedHostContext{}, ErrDedicatedHostContext
	}
	return c, nil
}
