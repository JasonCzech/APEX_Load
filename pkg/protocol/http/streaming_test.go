package http

import (
	"bytes"
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestParseStreamingProfileFromRuntimeConfig(t *testing.T) {
	profile := parseStreamingProfile(map[string]any{
		"web_streaming_mode":                 "fixed",
		"web_streaming_buffer_size_seconds":  float64(12),
		"web_streaming_fixed_bitrate_kbps":   float64(1800),
		"web_streaming_bandwidth_estimation": "instant",
	})

	if profile.mode != "fixed" {
		t.Fatalf("mode = %q, want fixed", profile.mode)
	}
	if profile.bufferSize != 12*time.Second {
		t.Fatalf("bufferSize = %s, want 12s", profile.bufferSize)
	}
	if profile.fixedBitrateKbps != 1800 {
		t.Fatalf("fixedBitrateKbps = %d, want 1800", profile.fixedBitrateKbps)
	}
	if profile.bandwidthEstimation != "instant" {
		t.Fatalf("bandwidthEstimation = %q, want instant", profile.bandwidthEstimation)
	}
}

func TestParseStreamingProfileWithoutStreamingSettingsIsInactive(t *testing.T) {
	profile := parseStreamingProfile(map[string]any{})

	if profile.active() {
		t.Fatal("profile.active() = true, want inactive when no streaming RTS values are configured")
	}
}

func TestStreamingProfileResponseDelayUsesFixedBitrateAndBufferForMedia(t *testing.T) {
	profile := streamingProfile{
		mode:             "fixed",
		bufferSize:       2 * time.Second,
		fixedBitrateKbps: 8,
	}
	resp := &stdhttp.Response{Header: stdhttp.Header{"Content-Type": []string{"video/mp4"}}}

	delay := profile.responseDelay(resp, nil, "https://media.example.test/clip.mp4", 1000, 100*time.Millisecond)

	if delay != 2900*time.Millisecond {
		t.Fatalf("delay = %s, want 2.9s", delay)
	}
}

func TestStreamingProfileResponseDelaySkipsNonMediaResponses(t *testing.T) {
	profile := streamingProfile{
		mode:             "fixed",
		bufferSize:       time.Second,
		fixedBitrateKbps: 8,
	}
	resp := &stdhttp.Response{Header: stdhttp.Header{"Content-Type": []string{"application/json"}}}

	if delay := profile.responseDelay(resp, nil, "https://api.example.test/orders", 1000, 0); delay != 0 {
		t.Fatalf("delay = %s, want none for non-media response", delay)
	}
}

func TestStreamingProfileAdaptiveRollingEstimation(t *testing.T) {
	profile := streamingProfile{
		mode:                "adaptive",
		bandwidthEstimation: "rolling",
	}
	resp := &stdhttp.Response{Header: stdhttp.Header{"Content-Type": []string{"audio/aac"}}}

	firstDelay := profile.responseDelay(resp, nil, "https://media.example.test/theme.aac", 10000, 100*time.Millisecond)
	secondDelay := profile.responseDelay(resp, nil, "https://media.example.test/theme.aac", 10000, 10*time.Millisecond)

	if firstDelay != 25*time.Millisecond {
		t.Fatalf("first delay = %s, want 25ms", firstDelay)
	}
	if secondDelay != 24*time.Millisecond {
		t.Fatalf("second delay = %s, want rolling estimator delay of 24ms", secondDelay)
	}
}

func TestHTTPExecuteAppliesStreamingPacingToMediaResponses(t *testing.T) {
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1000))
	}))
	defer server.Close()

	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{
		"base_url":                         server.URL,
		"web_streaming_mode":               "fixed",
		"web_streaming_fixed_bitrate_kbps": float64(8),
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer h.Teardown(context.Background())

	var slept time.Duration
	h.streaming.sleep = func(delay time.Duration) {
		slept += delay
	}

	result, err := h.Execute(context.Background(), &protocol.Action{Name: "media", Method: "GET", Target: "/clip.mp4"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("result.Success = false, error=%q", result.ErrorMessage)
	}
	if slept < 900*time.Millisecond {
		t.Fatalf("streaming sleep = %s, want fixed bitrate pacing near 1s", slept)
	}
}
