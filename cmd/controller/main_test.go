package main

import "testing"

func TestRecorderProxyAddrFromEnv(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDER_PROXY_ADDR", " 127.0.0.1:19081 ")

	if got := recorderProxyAddrFromEnv(); got != "127.0.0.1:19081" {
		t.Fatalf("recorder proxy addr = %q, want trimmed address", got)
	}
}

func TestRecorderProxyAddrFromEnvEmpty(t *testing.T) {
	t.Setenv("APEXLOAD_RECORDER_PROXY_ADDR", " \t ")

	if got := recorderProxyAddrFromEnv(); got != "" {
		t.Fatalf("empty recorder proxy addr = %q, want empty string", got)
	}
}
