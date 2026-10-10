package dsl

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// HTTPQUICConfig tunes the QUIC connections the HTTP/3 policies open
// (config.http.quic). Every field is optional and takes the runtime default
// when unset. Receive windows, header limits, stream budgets and packet
// sizing are operator controls, not script fields.
type HTTPQUICConfig struct {
	HandshakeTimeoutMs int    `json:"handshake_timeout_ms,omitempty" jsonschema:"minimum=1000,maximum=60000"`
	MaxIdleTimeoutMs   int    `json:"max_idle_timeout_ms,omitempty" jsonschema:"minimum=1000,maximum=600000"`
	KeepAliveMs        int    `json:"keep_alive_ms,omitempty" jsonschema:"minimum=0,maximum=600000"`
	AltSvcCache        string `json:"alt_svc_cache,omitempty" jsonschema:"enum=,enum=per_vuser,enum=per_iteration"`
	// BrokenTTLMs is a pointer so an authored 0 (forget failures at once)
	// stays distinct from unset (five minutes).
	BrokenTTLMs *int `json:"broken_ttl_ms,omitempty" jsonschema:"minimum=0,maximum=600000"`

	// unsupported keeps authored keys this version does not define. They are
	// written back on marshal, so they survive the JSON clones that layer
	// group and scenario overrides, and validation reports them at every
	// layer instead of dropping them silently.
	unsupported map[string]json.RawMessage
}

var httpQUICFields = map[string]bool{
	"handshake_timeout_ms": true, "max_idle_timeout_ms": true, "keep_alive_ms": true,
	"alt_svc_cache": true, "broken_ttl_ms": true,
}

func (config *HTTPQUICConfig) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("config.http.quic must be an object: %w", err)
	}
	type plain HTTPQUICConfig
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*config = HTTPQUICConfig(decoded)
	for key, value := range fields {
		if !httpQUICFields[key] {
			if config.unsupported == nil {
				config.unsupported = map[string]json.RawMessage{}
			}
			config.unsupported[key] = value
		}
	}
	return nil
}

func (config HTTPQUICConfig) MarshalJSON() ([]byte, error) {
	type plain HTTPQUICConfig
	encoded, err := json.Marshal(plain(config))
	if err != nil || len(config.unsupported) == 0 {
		return encoded, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	for key, value := range config.unsupported {
		fields[key] = value
	}
	return json.Marshal(fields)
}

// validateHTTPQUICConfigIssues reports out-of-range values and authored keys
// the runtime cannot honor.
func validateHTTPQUICConfigIssues(config *HTTPQUICConfig) []string {
	if config == nil {
		return nil
	}
	issues := httpprotocol.ValidateQUICSettings(config.HandshakeTimeoutMs, config.MaxIdleTimeoutMs, config.KeepAliveMs, config.AltSvcCache, config.BrokenTTLMs)
	keys := make([]string, 0, len(config.unsupported))
	for key := range config.unsupported {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		issues = append(issues, fmt.Sprintf("config.http.quic.%s is not a script setting: QUIC receive windows, header limits, stream budgets and packet sizing are operator controls (%s)", strings.TrimSpace(key), "APEXLOAD_QUIC_*"))
	}
	return issues
}
