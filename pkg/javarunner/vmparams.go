// Package javarunner contains shared, policy-enforcing primitives for the
// governed Java Vuser runtime. It deliberately has no dependency on the DSL or
// protocol packages so validation can be reused at authoring and execution
// boundaries without creating import cycles.
package javarunner

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	// MaxVMParameterCount is the maximum number of exact JVM argument tokens a
	// script may supply.
	MaxVMParameterCount = 128
	// MaxVMParameterBytes is the maximum UTF-8 byte length of one JVM argument
	// token.
	MaxVMParameterBytes = 4096
	// MaxVMParametersTotalBytes is the maximum combined UTF-8 byte length of all
	// JVM argument tokens.
	MaxVMParametersTotalBytes = 64 << 10
)

// Stable validation categories. Callers may safely expose these values in
// diagnostics; they never contain the rejected parameter value.
const (
	VMParameterTooMany              = "too_many_parameters"
	VMParameterTotalTooLarge        = "total_size_exceeded"
	VMParameterEmpty                = "empty"
	VMParameterTooLong              = "too_long"
	VMParameterControlCharacter     = "control_character"
	VMParameterMustStartWithDash    = "must_start_with_dash"
	VMParameterArgumentFile         = "argument_file"
	VMParameterEntrypointOverride   = "entrypoint_override"
	VMParameterClasspathOverride    = "classpath_override"
	VMParameterModuleOverride       = "module_override"
	VMParameterModuleAccessOverride = "module_access_override"
	VMParameterSelfAttach           = "self_attach"
	VMParameterDiagnosticFilePath   = "diagnostic_file_path"
	VMParameterAgentHook            = "agent_hook"
	VMParameterBootclasspath        = "bootclasspath_override"
	VMParameterCustomSystemLoader   = "custom_system_loader"
	VMParameterTerminalOption       = "terminal_option"
	VMParameterErrorCommandHook     = "error_command_hook"
	VMParameterReservedProperty     = "reserved_property"
	VMParameterTemplateForbidden    = "template_forbidden"
	VMParameterSensitiveProperty    = "sensitive_property"
)

// ValidationIssue identifies one invalid parameter by index and stable
// category. Index is -1 for collection-wide bounds. The rejected token is
// intentionally omitted so template- or secret-resolved values are not leaked
// through logs or API errors.
type ValidationIssue struct {
	Index    int    `json:"index"`
	Category string `json:"category"`
}

func (i ValidationIssue) Error() string {
	if i.Index < 0 {
		return fmt.Sprintf("Java VM parameters are invalid (%s)", i.Category)
	}
	return fmt.Sprintf("Java VM parameter token %d is invalid (%s)", i.Index, i.Category)
}

// ValidateVMParameters validates exact JVM argv tokens using a structural
// denylist. It never interprets quoting and never returns parameter values.
// One issue is returned per invalid token, plus any collection-wide issues.
func ValidateVMParameters(parameters []string) []ValidationIssue {
	issues := make([]ValidationIssue, 0)
	if len(parameters) > MaxVMParameterCount {
		issues = append(issues, ValidationIssue{Index: -1, Category: VMParameterTooMany})
	}

	total := 0
	for index, parameter := range parameters {
		total += len(parameter)
		if category := validateVMParameter(parameter); category != "" {
			issues = append(issues, ValidationIssue{Index: index, Category: category})
		}
	}
	if total > MaxVMParametersTotalBytes {
		issues = append(issues, ValidationIssue{Index: -1, Category: VMParameterTotalTooLarge})
	}
	return issues
}

func validateVMParameter(parameter string) string {
	if parameter == "" {
		return VMParameterEmpty
	}
	if len(parameter) > MaxVMParameterBytes {
		return VMParameterTooLong
	}
	if strings.IndexFunc(parameter, unicode.IsControl) >= 0 {
		return VMParameterControlCharacter
	}
	// JVM tokens are passed to a separately governed process and intentionally
	// bypass ordinary action-parameter resolution. Reject template syntax at
	// every validation boundary so a future resolver change cannot turn a
	// reviewed token into a secret-bearing argv value.
	if strings.Contains(parameter, "{{") || strings.Contains(parameter, "}}") {
		return VMParameterTemplateForbidden
	}

	lower := strings.ToLower(parameter)
	if strings.HasPrefix(lower, "@") {
		return VMParameterArgumentFile
	}
	if !strings.HasPrefix(parameter, "-") {
		return VMParameterMustStartWithDash
	}
	if parameter == "-" || parameter == "--" {
		return VMParameterTerminalOption
	}

	if matchesOption(lower, "-xx:flags") {
		return VMParameterArgumentFile
	}
	if matchesOption(lower, "-jar") || matchesOption(lower, "--source") {
		return VMParameterEntrypointOverride
	}
	if matchesAnyOption(lower, "-cp", "-classpath", "--class-path") {
		return VMParameterClasspathOverride
	}
	if matchesProperty(lower, "-djava.class.path") {
		return VMParameterClasspathOverride
	}
	if matchesAnyOption(lower,
		"-m", "--module", "-p", "--module-path", "--upgrade-module-path",
		"--patch-module", "--limit-modules", "--add-modules",
	) {
		return VMParameterModuleOverride
	}
	// Options that silently widen the JVM trust boundary a reviewer of the
	// Actions source would not expect from a "runtime setting": deep reflective
	// access into JDK internals and restricted native access.
	if matchesAnyOption(lower, "--add-opens", "--add-exports", "--add-reads", "--enable-native-access") {
		return VMParameterModuleAccessOverride
	}
	if strings.HasPrefix(lower, "-javaagent") ||
		strings.HasPrefix(lower, "-agentlib") ||
		strings.HasPrefix(lower, "-agentpath") ||
		strings.HasPrefix(lower, "-xrun") {
		return VMParameterAgentHook
	}
	if strings.HasPrefix(lower, "-xbootclasspath") {
		return VMParameterBootclasspath
	}
	if matchesProperty(lower, "-djava.system.class.loader") {
		return VMParameterCustomSystemLoader
	}
	// Re-enabling self-attach lets already-running in-JVM code load an
	// instrumentation agent into its own process and redefine arbitrary classes.
	if matchesProperty(lower, "-djdk.attach.allowattachself") {
		return VMParameterSelfAttach
	}
	// -XX options that write JVM-authored files (crash logs, heap dumps,
	// replay/CDS archives, loaded-class lists) to a caller-chosen path: a
	// file-clobber and heap-secret-exfil-to-readable-path primitive triggered by
	// the JVM itself. Both the read-side (-XX:SharedArchiveFile) and write-side
	// (-XX:ArchiveClassesAtExit) CDS forms are covered.
	if matchesAnyOption(lower,
		"-xx:heapdumppath", "-xx:errorfile", "-xx:replaydatafile", "-xx:sharedarchivefile",
		"-xx:archiveclassesatexit", "-xx:dumploadedclasslist",
	) {
		return VMParameterDiagnosticFilePath
	}
	// Unified logging can write to a caller-chosen path via ...:file=<path>.
	// Log-to-stdout/stderr forms (no file= output) stay allowed so the runner's
	// bounded output capture still surfaces GC/class logging.
	if strings.HasPrefix(lower, "-xlog") && strings.Contains(lower, "file=") {
		return VMParameterDiagnosticFilePath
	}
	if matchesAnyOption(lower,
		"-h", "-help", "--help", "-?", "-x", "--help-extra",
		"-version", "--version", "-showversion", "--show-version", "--dry-run",
		"--list-modules", "-d", "--describe-module", "--validate-modules", "-xinternalversion",
	) {
		return VMParameterTerminalOption
	}
	if matchesOption(lower, "-xx:onerror") || matchesOption(lower, "-xx:onoutofmemoryerror") {
		return VMParameterErrorCommandHook
	}
	if strings.HasPrefix(lower, "-dapexload.") {
		return VMParameterReservedProperty
	}
	if isSensitiveSystemProperty(lower) {
		return VMParameterSensitiveProperty
	}
	return ""
}

func isSensitiveSystemProperty(parameter string) bool {
	if !strings.HasPrefix(parameter, "-d") {
		return false
	}
	equals := strings.IndexByte(parameter, '=')
	if equals <= len("-d") {
		return false
	}
	propertyName := parameter[len("-d"):equals]
	for _, fragment := range []string{"password", "token", "secret", "credential"} {
		if strings.Contains(propertyName, fragment) {
			return true
		}
	}
	return false
}

func matchesAnyOption(parameter string, options ...string) bool {
	for _, option := range options {
		if matchesOption(parameter, option) {
			return true
		}
	}
	return false
}

func matchesOption(parameter, option string) bool {
	return parameter == option || strings.HasPrefix(parameter, option+"=")
}

func matchesProperty(parameter, property string) bool {
	return parameter == property || strings.HasPrefix(parameter, property+"=")
}
