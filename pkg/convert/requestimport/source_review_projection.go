package requestimport

import (
	"path"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/credential"
)

// RedactSourceReviewFilesByBaseName removes the contents of selected source
// artifacts from a public source review. Adapters remain responsible for
// source-specific parsing that identifies which files carried protected data.
func RedactSourceReviewFilesByBaseName(
	review *sourceview.Review,
	fileNames []string,
) {
	if review == nil || len(fileNames) == 0 {
		return
	}
	protected := make(map[string]struct{}, len(fileNames))
	for _, fileName := range fileNames {
		if base := sourceReviewBaseName(fileName); base != "" {
			protected[base] = struct{}{}
		}
	}
	for index := range review.SourceFiles {
		source := &review.SourceFiles[index]
		if _, redact := protected[sourceReviewBaseName(source.Path)]; !redact {
			continue
		}
		source.Content = credential.Redacted
		source.Redacted = true
	}
}

func sourceReviewBaseName(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	if value == "" {
		return ""
	}
	return strings.ToLower(path.Base(value))
}
