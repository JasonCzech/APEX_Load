package dsl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveBodyRefs replaces body_file references with inline body content.
// Paths are resolved relative to baseDir and must remain confined under it.
func ResolveBodyRefs(script *Script, baseDir string) error {
	if script == nil {
		return nil
	}
	if err := resolveBodyRefsInActions(script.Init, baseDir, "init"); err != nil {
		return err
	}
	if err := resolveBodyRefsInActions(script.Actions, baseDir, "actions"); err != nil {
		return err
	}
	if err := resolveBodyRefsInActions(script.Teardown, baseDir, "teardown"); err != nil {
		return err
	}
	return nil
}

func resolveBodyRefsInActions(actions []Action, baseDir string, section string) error {
	for i := range actions {
		label := fmt.Sprintf("%s[%d]", section, i)
		if err := resolveBodyRefInAction(&actions[i], baseDir, label); err != nil {
			return err
		}
	}
	return nil
}

func resolveBodyRefInAction(action *Action, baseDir string, label string) error {
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
		path, err := resolveConfinedBodyRefPath(baseDir, bodyFile)
		if err != nil {
			return fmt.Errorf("%s: invalid body_file %q: %w", label, action.BodyFile, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%s: read body_file %q: %w", label, action.BodyFile, err)
		}
		action.Body = string(data)
		action.BodyFile = ""
	}
	if action.Control != nil {
		if err := resolveBodyRefsInActions(action.Control.Steps, baseDir, label+".control.steps"); err != nil {
			return err
		}
		if err := resolveBodyRefsInActions(action.Control.Else, baseDir, label+".control.else"); err != nil {
			return err
		}
	}
	if action.Type == "http_sequence" {
		if err := resolveBodyRefsInSequenceChildren(action.Params, baseDir, label); err != nil {
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
func resolveBodyRefsInSequenceChildren(params map[string]any, baseDir string, label string) error {
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
			child, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if err := resolveBodyRefInSequenceChild(child, baseDir, fmt.Sprintf("%s.params.requests[%d]", label, i)); err != nil {
				return err
			}
		}
	case []map[string]any:
		for i := range typed {
			if err := resolveBodyRefInSequenceChild(typed[i], baseDir, fmt.Sprintf("%s.params.requests[%d]", label, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveBodyRefInSequenceChild reads the child's body_file (relative to and
// confined under baseDir), writes it to the child's body, and removes body_file.
// It mirrors resolveBodyRefInAction's behavior for the untyped child map form.
func resolveBodyRefInSequenceChild(child map[string]any, baseDir string, label string) error {
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
	path, err := resolveConfinedBodyRefPath(baseDir, bodyFile)
	if err != nil {
		return fmt.Errorf("%s: invalid body_file %q: %w", label, bodyFile, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: read body_file %q: %w", label, bodyFile, err)
	}
	child["body"] = string(data)
	delete(child, "body_file")
	return nil
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
		baseDir = "."
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
