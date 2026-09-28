package javarunner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DefaultHarnessMain = "Harness"
const enforcedDisableAttachMechanism = "-XX:+DisableAttachMechanism"
const enforcedDisableOpenJ9Attach = "-Dcom.ibm.tools.attach.enable=no"
const enforcedDisableHeapDumpOnOOM = "-XX:-HeapDumpOnOutOfMemoryError"
const enforcedDisableCrashCoreDump = "-XX:-CreateCoredumpOnCrash"
const enforcedDisableOpenJ9Dumps = "-Xdump:none"

// CommandSpec describes the operator- and script-owned portions of a direct
// Java harness launch. VMParameters are already exact argv tokens; no quoting
// or shell expansion is performed.
type CommandSpec struct {
	OperatorCDSManifest string
	ResolvedJDK         *ResolvedJDK
	VMParameters        []string
	// JavaMajor is the already-probed launcher major. Zero means unknown and
	// keeps the modern fail-closed defaults. The explicit legacy Java 8 lane
	// omits flags introduced after Java 8 so its JVM can still start.
	JavaMajor int
	// VMFamily is the already-probed VM implementation. Java 8 requires an
	// identified family because HotSpot and OpenJ9 use incompatible crash-dump
	// suppression switches.
	VMFamily             JavaVMFamily
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
	// BuildCommand is an execution boundary. Ordinary authored values have
	// already passed strict validation before persistence; trusted imports may
	// intentionally retain exact credential-bearing tokens and surface their
	// process-table exposure as durable review evidence.
	if issues := ValidateVMParametersForExecution(spec.VMParameters); len(issues) > 0 {
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
	if spec.OperatorCDSManifest != "" {
		archive, err := validateOperatorCDS(spec, userClasspath)
		if err != nil {
			return CommandPlan{}, err
		}
		// Fail closed on archive/JDK incompatibility instead of reporting an
		// AppCDS measurement while silently running without the archive.
		plan.Args = append(plan.Args, "-Xshare:on", "-XX:SharedArchiveFile="+archive)
	}
	// Every VUser JVM in a sidecar shares an OS identity and working directory.
	// Enforce attach isolation and dump suppression after all caller-owned VM
	// tokens so last-option precedence cannot reopen a sibling-memory or
	// crash-artifact disclosure path. OpenJ9 has its own all-dump switch.
	// CreateCoredumpOnCrash was introduced in HotSpot 9, so HotSpot 8 instead
	// directs its fatal-error log to the platform null device. An unidentified
	// Java 8 VM cannot be launched safely because those switches are mutually
	// incompatible.
	plan.Args = append(
		plan.Args,
		enforcedDisableAttachMechanism,
		enforcedDisableOpenJ9Attach,
		enforcedDisableHeapDumpOnOOM,
	)
	switch {
	case spec.VMFamily == JavaVMOpenJ9:
		plan.Args = append(plan.Args, enforcedDisableOpenJ9Dumps)
	case spec.JavaMajor == 8 && spec.VMFamily == JavaVMHotSpot:
		plan.Args = append(plan.Args, "-XX:ErrorFile="+os.DevNull)
	case spec.JavaMajor == 8:
		return CommandPlan{}, fmt.Errorf("legacy Java 8 VM family is not recognized")
	default:
		plan.Args = append(plan.Args, enforcedDisableCrashCoreDump)
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
