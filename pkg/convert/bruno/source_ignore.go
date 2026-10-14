package bruno

import (
	"encoding/json"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
)

// SkippedFile is an uploaded file the import left out, with the reason:
// operating-system or version-control bookkeeping, or a file the collection's
// own .gitignore or Bruno ignore list excludes.
type SkippedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// maxReportedSkippedFiles bounds the skipped-file list a review carries; an
// ignored node_modules folder alone can hold thousands of files.
const maxReportedSkippedFiles = 100

// sourceIgnoreRules holds the two kinds of ignore list a collection carries.
// Bruno never loads what its own ignore list names, so that list applies to
// every file. A .gitignore only says what is kept out of version control:
// Bruno still loads a gitignored .env, environment, request or certificate,
// so it applies only to other files, such as logs and installed modules.
type sourceIgnoreRules struct {
	bruno archiveingest.IgnoreRules
	git   archiveingest.IgnoreRules
}

// filterIgnoredSourceFiles keeps only collection content. Skipped files never
// reach the manifest, the sanitized bundle or the capability summary, so a
// .DS_Store cannot read as a "file body" the import cannot translate. rules
// are matched relative to root.
func filterIgnoredSourceFiles(files []sourceFile, root string, rules sourceIgnoreRules) ([]sourceFile, []SkippedFile) {
	kept := make([]sourceFile, 0, len(files))
	var skipped []SkippedFile
	for _, file := range files {
		reason := archiveingest.JunkReason(file.Path)
		if reason == "" && (!rules.bruno.Empty() || !rules.git.Empty()) {
			if relative, ok := nativeRelativePath(file.Path, root); ok &&
				(rules.bruno.Match(relative) || (rules.git.Match(relative) && !brunoLoadedFile(file))) {
				reason = archiveingest.SkipReasonIgnoreList
			}
		}
		if reason != "" {
			skipped = append(skipped, SkippedFile{Path: file.Path, Reason: reason})
			continue
		}
		kept = append(kept, file)
	}
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Path < skipped[j].Path })
	return kept, skipped
}

// brunoLoadedFile reports whether Bruno reads file itself when it opens the
// collection, whatever version control ignores.
func brunoLoadedFile(file sourceFile) bool {
	switch classifyFileRole(file.Path, file.Data) {
	case "collection", "folder", "environment", "request", "certificate":
		return true
	default:
		return false
	}
}

// brunoIgnoreRules reads the root .gitignore and the ignore list Bruno keeps
// in opencollection.yml (extensions.bruno.ignore, or a top-level ignore) or
// bruno.json. An unreadable file contributes no rules.
func brunoIgnoreRules(files []sourceFile, root string) sourceIgnoreRules {
	var brunoLines, gitLines []string
	for _, file := range files {
		relative, ok := nativeRelativePath(file.Path, root)
		if !ok {
			continue
		}
		switch strings.ToLower(relative) {
		case ".gitignore":
			gitLines = append(gitLines, strings.Split(string(file.Data), "\n")...)
		case "opencollection.yml", "opencollection.yaml":
			var document struct {
				Ignore     []string `yaml:"ignore"`
				Extensions struct {
					Bruno struct {
						Ignore []string `yaml:"ignore"`
					} `yaml:"bruno"`
				} `yaml:"extensions"`
			}
			if yaml.Unmarshal(file.Data, &document) == nil {
				brunoLines = append(brunoLines, document.Ignore...)
				brunoLines = append(brunoLines, document.Extensions.Bruno.Ignore...)
			}
		case "bruno.json":
			var document struct {
				Ignore []string `json:"ignore"`
			}
			if json.Unmarshal(file.Data, &document) == nil {
				brunoLines = append(brunoLines, document.Ignore...)
			}
		}
	}
	return sourceIgnoreRules{
		bruno: archiveingest.ParseIgnoreRules(brunoLines),
		git:   archiveingest.ParseIgnoreRules(gitLines),
	}
}
