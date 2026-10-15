package archiveingest

import (
	"path"
	"strings"
)

// Reasons an uploaded file is left out of an import. Folder and archive
// uploads routinely carry operating-system and version-control files; they are
// never collection content, so counting them makes a review warn about
// content that does not exist.
const (
	SkipReasonSystemFile     = "operating_system_file"
	SkipReasonVersionControl = "version_control_file"
	SkipReasonIgnoreList     = "ignore_list"
)

// JunkReason reports why logicalPath is operating-system or version-control
// bookkeeping rather than content, or "" when it is content.
func JunkReason(logicalPath string) string {
	segments := strings.Split(LogicalPath(logicalPath), "/")
	for _, directory := range segments[:len(segments)-1] {
		switch strings.ToLower(directory) {
		case "__macosx":
			return SkipReasonSystemFile
		case ".git", ".svn", ".hg":
			return SkipReasonVersionControl
		}
	}
	base := segments[len(segments)-1]
	switch strings.ToLower(base) {
	case ".ds_store", "thumbs.db", "ehthumbs.db", "desktop.ini", "icon\r":
		return SkipReasonSystemFile
	case ".gitignore", ".gitattributes", ".gitkeep", ".gitmodules":
		return SkipReasonVersionControl
	}
	if strings.HasPrefix(base, "._") {
		// macOS AppleDouble resource forks shadow every file copied to a
		// non-Apple filesystem.
		return SkipReasonSystemFile
	}
	return ""
}

// IgnoreRules matches paths against .gitignore-style patterns. Blank lines and
// # comments are skipped; a leading / anchors a pattern to the root, as does a
// slash inside it; a trailing / matches directories only; *, ? and [...] glob
// within one path segment, and a leading **/ matches at any depth. Negation (!)
// and other ** forms are not supported and are skipped, which can only keep
// more files.
type IgnoreRules struct {
	patterns []ignorePattern
}

type ignorePattern struct {
	glob     string
	anchored bool
	dirOnly  bool
}

// ParseIgnoreRules builds rules from pattern lines, such as the lines of a
// .gitignore file or the entries of an import tool's ignore list.
func ParseIgnoreRules(lines []string) IgnoreRules {
	var rules IgnoreRules
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		line = strings.TrimSpace(line)
		pattern := ignorePattern{}
		if strings.HasSuffix(line, "/") {
			pattern.dirOnly = true
			line = strings.TrimRight(line, "/")
		}
		line = strings.TrimPrefix(line, "**/")
		if strings.Contains(line, "**") || line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") || strings.Contains(line, "/") {
			pattern.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		if _, err := path.Match(line, ""); err != nil {
			continue
		}
		pattern.glob = line
		rules.patterns = append(rules.patterns, pattern)
	}
	return rules
}

// Empty reports whether the rules match nothing.
func (rules IgnoreRules) Empty() bool { return len(rules.patterns) == 0 }

// Match reports whether relativePath, relative to the rules' root, is ignored
// itself or lies inside an ignored directory.
func (rules IgnoreRules) Match(relativePath string) bool {
	relativePath = strings.Trim(LogicalPath(relativePath), "/")
	if relativePath == "" || relativePath == "." {
		return false
	}
	segments := strings.Split(relativePath, "/")
	for _, pattern := range rules.patterns {
		for index := range segments {
			isDirectory := index < len(segments)-1
			if pattern.dirOnly && !isDirectory {
				continue
			}
			candidate := segments[index]
			if pattern.anchored {
				candidate = strings.Join(segments[:index+1], "/")
			}
			if matched, _ := path.Match(pattern.glob, candidate); matched {
				return true
			}
		}
	}
	return false
}
