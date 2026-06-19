package http

import (
	"context"
	"math"
	stdhttp "net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type streamingProfile struct {
	enabled                 bool
	mode                    string
	bufferSize              time.Duration
	fixedBitrateKbps        int
	bandwidthEstimation     string
	adaptiveBandwidthKbps   int
	sleep                   func(time.Duration)
	rollingBitrateKbps      float64
	rollingBitrateKbpsMutex sync.Mutex
}

func parseStreamingProfile(config map[string]any) *streamingProfile {
	mode, _ := config["web_streaming_mode"].(string)
	bandwidthEstimation, _ := config["web_streaming_bandwidth_estimation"].(string)
	bufferSizeSeconds := protocol.ConfigInt(config, "web_streaming_buffer_size_seconds")
	fixedBitrateKbps := protocol.ConfigInt(config, "web_streaming_fixed_bitrate_kbps")
	_, networkBandwidthKbps := resolveNetworkProfile(
		stringConfig(config, "web_network_simulation_mode"),
		stringConfig(config, "web_network_simulation_preset"),
		protocol.ConfigInt(config, "web_network_simulation_latency_ms"),
		protocol.ConfigInt(config, "web_network_simulation_bandwidth_kbps"),
	)

	profile := streamingProfile{
		enabled:               strings.TrimSpace(mode) != "" || bufferSizeSeconds > 0 || fixedBitrateKbps > 0 || strings.TrimSpace(bandwidthEstimation) != "",
		mode:                  strings.ToLower(strings.TrimSpace(mode)),
		bufferSize:            time.Duration(bufferSizeSeconds) * time.Second,
		fixedBitrateKbps:      fixedBitrateKbps,
		bandwidthEstimation:   strings.ToLower(strings.TrimSpace(bandwidthEstimation)),
		adaptiveBandwidthKbps: networkBandwidthKbps,
	}
	if profile.mode == "" && profile.active() {
		profile.mode = "adaptive"
	}
	if profile.bandwidthEstimation == "" {
		profile.bandwidthEstimation = "rolling"
	}
	return &profile
}

func stringConfig(config map[string]any, key string) string {
	value, _ := config[key].(string)
	return value
}

func (p *streamingProfile) active() bool {
	if p == nil {
		return false
	}
	return p.enabled || strings.TrimSpace(p.mode) != "" || p.bufferSize > 0 || p.fixedBitrateKbps > 0
}

func (p *streamingProfile) responseDelay(resp *stdhttp.Response, action *protocol.Action, targetURL string, responseBytes int64, bodyReadDuration time.Duration) time.Duration {
	if p == nil || !p.active() || responseBytes <= 0 || !isStreamingMediaResponse(resp, action, targetURL) {
		return 0
	}

	desiredDuration := p.mediaTransferDuration(responseBytes, bodyReadDuration)
	if p.bufferSize > 0 {
		desiredDuration += p.bufferSize
	}
	if desiredDuration <= bodyReadDuration {
		return 0
	}
	return roundDurationToMillis(desiredDuration - bodyReadDuration)
}

func (p *streamingProfile) mediaTransferDuration(responseBytes int64, bodyReadDuration time.Duration) time.Duration {
	bitrateKbps := p.effectiveBitrateKbps(responseBytes, bodyReadDuration)
	if bitrateKbps <= 0 {
		return 0
	}
	seconds := float64(responseBytes*8) / float64(bitrateKbps*1000)
	return time.Duration(seconds * float64(time.Second))
}

func (p *streamingProfile) effectiveBitrateKbps(responseBytes int64, bodyReadDuration time.Duration) int {
	switch strings.ToLower(strings.TrimSpace(p.mode)) {
	case "fixed":
		return p.fixedBitrateKbps
	case "adaptive", "":
		if p.adaptiveBandwidthKbps > 0 {
			return int(math.Max(1, math.Round(float64(p.adaptiveBandwidthKbps)*0.8)))
		}
		observed := observedBitrateKbps(responseBytes, bodyReadDuration)
		if observed <= 0 {
			return 0
		}
		if p.bandwidthEstimation == "instant" {
			return int(math.Max(1, math.Round(observed*0.8)))
		}
		return int(math.Max(1, math.Round(p.rollingBitrateEstimate(observed)*0.8)))
	default:
		return 0
	}
}

func observedBitrateKbps(responseBytes int64, elapsed time.Duration) float64 {
	if responseBytes <= 0 || elapsed <= 0 {
		return 0
	}
	return (float64(responseBytes) * 8) / (elapsed.Seconds() * 1000)
}

func (p *streamingProfile) rollingBitrateEstimate(observedKbps float64) float64 {
	p.rollingBitrateKbpsMutex.Lock()
	defer p.rollingBitrateKbpsMutex.Unlock()

	if p.rollingBitrateKbps <= 0 {
		p.rollingBitrateKbps = observedKbps
	} else {
		p.rollingBitrateKbps = p.rollingBitrateKbps*0.7 + observedKbps*0.3
	}
	return p.rollingBitrateKbps
}

func (p *streamingProfile) wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if p != nil && p.sleep != nil {
		p.sleep(delay)
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isStreamingMediaResponse(resp *stdhttp.Response, action *protocol.Action, targetURL string) bool {
	if actionHasMediaResourceType(action) {
		return true
	}
	if resp != nil {
		if contentTypeLooksStreaming(resp.Header.Get("Content-Type")) {
			return true
		}
	}
	return urlLooksStreaming(targetURL)
}

func actionHasMediaResourceType(action *protocol.Action) bool {
	if action == nil || len(action.Metadata) == 0 {
		return false
	}
	for _, key := range []string{"resource_type", "resourceType", "initiator_type", "initiatorType"} {
		value, _ := action.Metadata[key].(string)
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "media", "video", "audio":
			return true
		}
	}
	return false
}

func contentTypeLooksStreaming(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if strings.HasPrefix(contentType, "video/") || strings.HasPrefix(contentType, "audio/") {
		return true
	}
	switch contentType {
	case "application/dash+xml",
		"application/mp4",
		"application/mpegurl",
		"application/vnd.apple.mpegurl",
		"application/x-mpegurl":
		return true
	default:
		return false
	}
}

func urlLooksStreaming(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	extension := strings.ToLower(path.Ext(parsed.Path))
	switch extension {
	case ".aac", ".avi", ".dash", ".flac", ".m3u8", ".m4a", ".m4s", ".m4v", ".mkv", ".mov", ".mp3", ".mp4", ".mpeg", ".mpd", ".ogg", ".ts", ".wav", ".webm":
		return true
	default:
		return false
	}
}
