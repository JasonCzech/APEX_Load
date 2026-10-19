package correlate

import (
	"fmt"
	"sort"
	"strings"
)

// StaticEvidenceOmission counts the static-asset response bodies that
// FitStaticAssetEvidence left out of correlation analysis.
type StaticEvidenceOmission struct {
	Responses int
	Bytes     int64
}

// Note states the omission for an import review, or "" when nothing was left
// out.
func (o StaticEvidenceOmission) Note() string {
	if o.Responses == 0 {
		return ""
	}
	return fmt.Sprintf(
		"correlation analysis skipped the bodies of %d static asset response(s) (%d MiB of JavaScript, stylesheets, fonts and media) to stay within its %d MiB evidence budget; values that appear only inside those files are not correlated",
		o.Responses, (o.Bytes+(1<<20)-1)>>20, maxCorrelationAnalysisEvidenceBytes>>20,
	)
}

// FitStaticAssetEvidence leaves static-asset response bodies out of the
// evidence until it fits the per-response and aggregate budgets that
// detection enforces. A large browser capture is mostly framework
// JavaScript (a Salesforce Lightning session records 100+ MiB of Aura
// component definitions), and counting it made detection refuse the whole
// capture. Static bodies are dropped largest first and only as far as needed,
// so a capture that already fits is analyzed unchanged. Requests, response
// headers and every other body are kept, so values sent to or set by those
// assets still correlate. The input slice is never modified.
func FitStaticAssetEvidence(observations []Observation) ([]Observation, StaticEvidenceOmission) {
	var total int64
	var static []int
	for index, observation := range observations {
		total += correlationObservationBytes(observation)
		if observation.ResponseBody != "" && staticAssetResponse(observation.ResponseHeaders) {
			static = append(static, index)
		}
	}
	omit := map[int]bool{}
	var omission StaticEvidenceOmission
	drop := func(index int) {
		size := int64(len(observations[index].ResponseBody))
		omit[index] = true
		omission.Responses++
		omission.Bytes += size
		total -= size
	}
	for _, index := range static {
		if correlationObservationBytes(observations[index]) > maxCorrelationEvidenceBytes {
			drop(index)
		}
	}
	if total > maxCorrelationAnalysisEvidenceBytes {
		sort.SliceStable(static, func(i, j int) bool {
			return len(observations[static[i]].ResponseBody) > len(observations[static[j]].ResponseBody)
		})
		for _, index := range static {
			if total <= maxCorrelationAnalysisEvidenceBytes {
				break
			}
			if !omit[index] {
				drop(index)
			}
		}
	}
	if len(omit) == 0 {
		return observations, omission
	}
	fitted := make([]Observation, len(observations))
	copy(fitted, observations)
	for index := range omit {
		fitted[index].ResponseBody = ""
	}
	return fitted, omission
}

// staticAssetResponse reports whether a response's Content-Type names a
// static asset — script, stylesheet, font, image, audio, video or wasm —
// rather than a document or data the application produced for this session.
// A response without a Content-Type is never treated as static.
func staticAssetResponse(headers map[string]string) bool {
	var contentType string
	for name, value := range headers {
		if strings.EqualFold(name, "content-type") {
			contentType = value
			break
		}
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch {
	case mediaType == "":
		return false
	case strings.Contains(mediaType, "javascript"), strings.Contains(mediaType, "ecmascript"):
		return true
	case mediaType == "text/css", mediaType == "application/wasm":
		return true
	case strings.HasPrefix(mediaType, "font/"), strings.HasPrefix(mediaType, "image/"),
		strings.HasPrefix(mediaType, "audio/"), strings.HasPrefix(mediaType, "video/"):
		return true
	case strings.Contains(mediaType, "font"):
		// application/font-woff, application/x-font-ttf, application/vnd.ms-fontobject
		return true
	default:
		return false
	}
}
