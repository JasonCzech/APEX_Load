package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func messageDetailRuntimeScript(protocolName, mode string) *dsl.Script {
	return &dsl.Script{
		Protocol: protocolName,
		Config:   dsl.ScriptConfig{Results: dsl.ResultsConfig{MessageDetail: &dsl.MessageDetailConfig{Mode: mode}}},
	}
}

func TestNewAcceptsAdaptiveMessageDetailForHTTP(t *testing.T) {
	if _, err := New(Config{Script: messageDetailRuntimeScript("http", "adaptive"), VUsers: 1, Duration: time.Second}); err != nil {
		t.Fatalf("New() rejected adaptive capture: %v", err)
	}
}

func TestNewRejectsSampledMessageDetailForUnsupportedProtocol(t *testing.T) {
	_, err := New(Config{Script: messageDetailRuntimeScript("database", "sampled"), VUsers: 1, Duration: time.Second})
	if err == nil || !strings.Contains(err.Error(), "requires the http protocol") {
		t.Fatalf("New() error = %v, want protocol admission error", err)
	}
}

func TestNewMultiGroupAcceptsAdaptiveMessageDetail(t *testing.T) {
	_, err := NewMultiGroup(MultiGroupConfig{
		Groups:   []GroupConfig{{Name: "adaptive", Script: messageDetailRuntimeScript("http", "adaptive"), VUsers: 1}},
		Duration: time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup() rejected adaptive capture: %v", err)
	}
}
