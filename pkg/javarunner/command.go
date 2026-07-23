package javarunner

import (
	"fmt"
	"path/filepath"
	"strings"
)

const DefaultHarnessMain = "Harness"

// CommandSpec describes the operator- and script-owned portions of a direct
// Java harness launch. VMParameters are already exact argv tokens; no quoting
// or shell expansion is performed.
type CommandSpec struct {
	VMParameters         []string
	HarnessJar           string
	ScriptClasspath      []string
	SharedClasspath      []string
	DedicatedClassLoader bool
	HarnessMain          string
	HarnessArgs          []string
}

// CommandPlan contains JVM argv and the user classpath that must be conveyed in
// the harness request when DedicatedClassLoader is enabled. UserClasspath is
// intentionally not joined into an argv string in dedicated mode.
type CommandPlan struct {
	Args            []string
	SystemClasspath []string
	UserClasspath   []string
}

// BuildCommand constructs a direct Java invocation. Script classpath entries
// always precede operator-shared entries. VM parameters always precede launcher
// classpath/main arguments. Modern runtimes never receive an Xbootclasspath
// flag; callers represent that compatibility fallback as execution evidence.
func BuildCommand(spec CommandSpec) (CommandPlan, error) {
	if issues := ValidateVMParameters(spec.VMParameters); len(issues) > 0 {
		return CommandPlan{}, issues[0]
	}

	harnessJar := strings.TrimSpace(spec.HarnessJar)
	userClasspath := append(cleanClasspath(spec.ScriptClasspath), cleanClasspath(spec.SharedClasspath)...)
	if issues := ValidateClasspath(userClasspath); len(issues) > 0 {
		return CommandPlan{}, issues[0]
	}
	harnessMain := strings.TrimSpace(spec.HarnessMain)
	if harnessMain == "" {
		harnessMain = DefaultHarnessMain
	}
	// The main class is a terminal positional token. Reject a leading dash so a
	// future caller cannot let it be reparsed as a JVM option after -cp,
	// re-opening option injection past the validated VM parameters.
	if strings.HasPrefix(harnessMain, "-") {
		return CommandPlan{}, fmt.Errorf("harness main class must not begin with '-'")
	}

	plan := CommandPlan{
		Args: append([]string(nil), spec.VMParameters...),
	}
	if spec.DedicatedClassLoader {
		if harnessJar == "" {
			return CommandPlan{}, fmt.Errorf("dedicated Java class loader requires an operator harness jar")
		}
		if len(userClasspath) == 0 {
			return CommandPlan{}, fmt.Errorf("dedicated Java class loader requires a user classpath")
		}
		plan.SystemClasspath = []string{harnessJar}
		plan.UserClasspath = append([]string(nil), userClasspath...)
		plan.Args = append(plan.Args,
			"-cp", strings.Join(plan.SystemClasspath, string(filepath.ListSeparator)), harnessMain,
		)
	} else {
		switch {
		case harnessJar != "" && len(userClasspath) > 0:
			plan.SystemClasspath = append([]string{harnessJar}, userClasspath...)
			plan.Args = append(plan.Args,
				"-cp", strings.Join(plan.SystemClasspath, string(filepath.ListSeparator)), harnessMain,
			)
		case harnessJar != "":
			plan.SystemClasspath = []string{harnessJar}
			plan.Args = append(plan.Args, "-jar", harnessJar)
		case len(userClasspath) > 0:
			plan.SystemClasspath = append([]string(nil), userClasspath...)
			plan.Args = append(plan.Args,
				"-cp", strings.Join(plan.SystemClasspath, string(filepath.ListSeparator)), harnessMain,
			)
		default:
			plan.Args = append(plan.Args, harnessMain)
		}
	}
	plan.Args = append(plan.Args, spec.HarnessArgs...)
	return plan, nil
}

func cleanClasspath(entries []string) []string {
	cleaned := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry != "" {
			cleaned = append(cleaned, entry)
		}
	}
	return cleaned
}
