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
	return nil
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
