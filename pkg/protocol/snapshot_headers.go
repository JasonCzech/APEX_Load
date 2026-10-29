package protocol

import (
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
)

// FlattenSnapshotHeaders retains repeated header values in wire order, separated
// by newlines (the same representation used by HTTP snapshots). It bounds the
// detached copy before returning it to a protocol result.
func FlattenSnapshotHeaders(headers map[string][]string) (map[string]string, bool) {
	return flattenSnapshotHeaders(headers, false)
}

// FlattenGRPCSnapshotMetadata represents binary metadata as its unpadded
// base64 wire value before applying the same snapshot bounds.
func FlattenGRPCSnapshotMetadata(headers map[string][]string) (map[string]string, bool) {
	return flattenSnapshotHeaders(headers, true)
}

func flattenSnapshotHeaders(headers map[string][]string, grpcMetadata bool) (map[string]string, bool) {
	const maxNames = 256
	const maxValueBytes = 16 << 10
	const maxTotalBytes = 256 << 10
	const marker = "...[truncated]"
	if len(headers) == 0 {
		return nil, false
	}
	keys := boundedtext.SmallestStringKeys(headers, maxNames)
	remaining := maxTotalBytes
	omitted := len(headers) > len(keys)
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if len(key) > 4<<10 || len(key) >= remaining {
			omitted = true
			continue
		}
		name, changed, _ := normalizeSnapshotHeader(key, min(4<<10, remaining-1))
		if changed {
			omitted = true
		}
		if name == "" && key != "" {
			omitted = true
			continue
		}
		if _, exists := out[name]; exists {
			// Raw keys are sorted, so the first normalized name wins even if
			// the first value is empty. Never silently overwrite a collision.
			omitted = true
			continue
		}
		remaining -= len(name)
		limit := min(maxValueBytes, remaining)
		var value strings.Builder
		truncated := false
		for i, part := range headers[key] {
			if i != 0 {
				if value.Len() >= limit {
					truncated = true
					break
				}
				value.WriteByte('\n')
			}
			available := limit - value.Len()
			if grpcMetadata && strings.HasSuffix(strings.ToLower(key), "-bin") {
				encodedLen := base64.RawStdEncoding.EncodedLen(len(part))
				// Never allocate an encoding larger than the remaining bounded field.
				prefix := min(len(part), (available/4+1)*3)
				encoded := base64.RawStdEncoding.EncodeToString([]byte(part[:prefix]))
				if encodedLen > available {
					value.WriteString(encoded[:min(available, len(encoded))])
					truncated = true
					break
				}
				value.WriteString(encoded)
				continue
			}
			normalized, changed, shortened := normalizeSnapshotHeader(part, available)
			value.WriteString(normalized)
			omitted = omitted || changed
			if shortened {
				truncated = true
				break
			}
		}
		text := value.String()
		if truncated {
			omitted = true
		}
		if truncated && limit >= len(marker) {
			text = boundedtext.TruncateUTF8(text, limit-len(marker)) + marker
		}
		remaining -= len(text)
		out[name] = strings.Clone(text)
	}
	return out, omitted
}

// normalizeSnapshotHeader replaces invalid UTF-8 and stops before exceeding
// the byte budget. changed covers replacement and truncation independently of
// whether the normalized output happens to have the same length as the input.
func normalizeSnapshotHeader(value string, limit int) (normalized string, changed, shortened bool) {
	var out strings.Builder
	out.Grow(min(len(value), max(0, limit)))
	for len(value) > 0 {
		r, width := utf8.DecodeRuneInString(value)
		piece := value[:width]
		if r == utf8.RuneError && width == 1 {
			piece = "\uFFFD"
			changed = true
		}
		if len(piece) > limit-out.Len() {
			return out.String(), true, true
		}
		out.WriteString(piece)
		value = value[width:]
	}
	return out.String(), changed, false
}
