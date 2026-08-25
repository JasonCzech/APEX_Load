package dsl

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// MaxBodyRefBytes bounds one local body_file before it is inlined into the
	// script. Larger upload bodies should use governed file data sources.
	MaxBodyRefBytes int64 = 16 << 20
	// MaxBodyRefAggregateBytes prevents a script with many individually valid
	// body_file entries from exhausting memory during ParseFile.
	MaxBodyRefAggregateBytes int64 = 64 << 20
)

// ResolveBodyRefs replaces body_file references with inline body content.
// Paths are resolved relative to baseDir and must remain confined under it.
func ResolveBodyRefs(script *Script, baseDir string) error {
	if script == nil {
		return nil
	}
	remaining := MaxBodyRefAggregateBytes
	if err := resolveBodyRefsInActions(script.Init, baseDir, "init", &remaining); err != nil {
		return err
	}
	if err := resolveBodyRefsInActions(script.Actions, baseDir, "actions", &remaining); err != nil {
		return err
	}
	if err := resolveBodyRefsInActions(script.Teardown, baseDir, "teardown", &remaining); err != nil {
		return err
	}
	return nil
}

func resolveBodyRefsInActions(actions []Action, baseDir string, section string, remaining *int64) error {
	for i := range actions {
		label := fmt.Sprintf("%s[%d]", section, i)
		if err := resolveBodyRefInAction(&actions[i], baseDir, label, remaining); err != nil {
			return err
		}
	}
	return nil
}

func resolveBodyRefInAction(action *Action, baseDir string, label string, remaining *int64) error {
	if action == nil {
		return nil
	}
	if name := strings.TrimSpace(action.Name); name != "" {
		label = fmt.Sprintf("%s %q", label, name)
	}
	bodyFile := strings.TrimSpace(action.BodyFile)
	if bodyFile != "" {
		if action.Body != "" {
			return fmt.Errorf("%s: body and body_file cannot both be set", label)
		}
		data, err := readConfinedBodyRef(baseDir, bodyFile, remaining)
		if err != nil {
			return fmt.Errorf("%s: read body_file %q: %w", label, action.BodyFile, err)
		}
		action.Body = string(data)
		action.BodyFile = ""
	}
	if action.Control != nil {
		for _, branch := range action.Control.ActionBranches() {
			if err := resolveBodyRefsInActions(branch.Actions, baseDir, label+".control."+branch.Path, remaining); err != nil {
				return err
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
		if err := resolveBodyRefsInSequenceChildren(action.Params, baseDir, label, remaining); err != nil {
			return err
		}
	}
	return nil
}

// resolveBodyRefsInSequenceChildren resolves body_file references on
// http_sequence child requests, which live untyped in params["requests"]. The
// engine decodes each child map into a dsl.Action and the HTTP protocol uses
// only the inline body, so an unresolved child body_file would silently POST an
// empty body. Children are resolved using the same confinement guard and
// conflict rules as top-level actions.
func resolveBodyRefsInSequenceChildren(params map[string]any, baseDir string, label string, remaining *int64) error {
	if params == nil {
		return nil
	}
	requests, ok := params["requests"]
	if !ok {
		return nil
	}
	switch typed := requests.(type) {
	case []any:
		for i, value := range typed {
			childLabel := fmt.Sprintf("%s.params.requests[%d]", label, i)
			switch child := value.(type) {
			case map[string]any:
				if err := resolveBodyRefInSequenceChild(child, baseDir, childLabel, remaining); err != nil {
					return err
				}
			case Action:
				childCopy := child
				if err := resolveBodyRefInAction(&childCopy, baseDir, childLabel, remaining); err != nil {
					return err
				}
				typed[i] = childCopy
			case *Action:
				if err := resolveBodyRefInAction(child, baseDir, childLabel, remaining); err != nil {
					return err
				}
			}
		}
	case []map[string]any:
		for i := range typed {
			if err := resolveBodyRefInSequenceChild(typed[i], baseDir, fmt.Sprintf("%s.params.requests[%d]", label, i), remaining); err != nil {
				return err
			}
		}
	case []Action:
		for i := range typed {
			if err := resolveBodyRefInAction(&typed[i], baseDir, fmt.Sprintf("%s.params.requests[%d]", label, i), remaining); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveBodyRefInSequenceChild reads the child's body_file (relative to and
// confined under baseDir), writes it to the child's body, and removes body_file.
// It mirrors resolveBodyRefInAction's behavior for the untyped child map form.
func resolveBodyRefInSequenceChild(child map[string]any, baseDir string, label string, remaining *int64) error {
	if child == nil {
		return nil
	}
	if name := strings.TrimSpace(stringMapValue(child, "name")); name != "" {
		label = fmt.Sprintf("%s %q", label, name)
	}
	bodyFile := strings.TrimSpace(stringMapValue(child, "body_file"))
	if bodyFile == "" {
		return nil
	}
	if strings.TrimSpace(stringMapValue(child, "body")) != "" {
		return fmt.Errorf("%s: body and body_file cannot both be set", label)
	}
	data, err := readConfinedBodyRef(baseDir, bodyFile, remaining)
	if err != nil {
		return fmt.Errorf("%s: read body_file %q: %w", label, bodyFile, err)
	}
	child["body"] = string(data)
	delete(child, "body_file")
	return nil
}

func readConfinedBodyRef(baseDir, ref string, remaining *int64) ([]byte, error) {
	path, err := resolveConfinedBodyRefPath(baseDir, ref)
	if err != nil {
		return nil, err
	}
	absBase, err := filepath.Abs(firstNonEmptyBodyRefBase(baseDir))
	if err != nil {
		return nil, err
	}
	absBase = filepath.Clean(absBase)
	relative, err := filepath.Rel(absBase, path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(absBase)
	if err != nil {
		return nil, fmt.Errorf("open script directory: %w", err)
	}
	defer root.Close()
	info, err := root.Stat(relative)
	if err != nil {
		return nil, fmt.Errorf("open within script directory: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("body_file must be a regular file")
	}
	if info.Size() > MaxBodyRefBytes {
		return nil, fmt.Errorf("body_file exceeds %d byte limit", MaxBodyRefBytes)
	}
	if remaining == nil || *remaining < 0 {
		return nil, fmt.Errorf("body_file aggregate budget is invalid")
	}
	if info.Size() > *remaining {
		return nil, fmt.Errorf("body_file content exceeds %d byte aggregate limit", MaxBodyRefAggregateBytes)
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, fmt.Errorf("open within script directory: %w", err)
	}
	defer file.Close()
	limit := MaxBodyRefBytes
	if *remaining < limit {
		limit = *remaining
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxBodyRefBytes {
		return nil, fmt.Errorf("body_file exceeds %d byte limit", MaxBodyRefBytes)
	}
	if int64(len(data)) > *remaining {
		return nil, fmt.Errorf("body_file content exceeds %d byte aggregate limit", MaxBodyRefAggregateBytes)
	}
	*remaining -= int64(len(data))
	return data, nil
}

func firstNonEmptyBodyRefBase(baseDir string) string {
	if strings.TrimSpace(baseDir) == "" {
		return "."
	}
	return baseDir
}

// stringMapValue returns the string at key, or "" if absent or not a string.
func stringMapValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if value, ok := m[key].(string); ok {
		return value
	}
	return ""
}

func resolveConfinedBodyRefPath(baseDir string, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.TrimSpace(baseDir) == "" {
		baseDir = firstNonEmptyBodyRefBase(baseDir)
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absBase = filepath.Clean(absBase)

	candidate := ref
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(absBase, candidate)
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	absCandidate = filepath.Clean(absCandidate)

	rel, err := filepath.Rel(absBase, absCandidate)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path escapes script directory")
	}
	return absCandidate, nil
}
