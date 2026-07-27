// Package javarunner contains shared, policy-enforcing primitives for the
// governed Java Vuser runtime. It deliberately has no dependency on the DSL or
// protocol packages so validation can be reused at authoring and execution
// boundaries without creating import cycles.
package javarunner

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/scrub"
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
	VMParameterTooMany               = "too_many_parameters"
	VMParameterTotalTooLarge         = "total_size_exceeded"
	VMParameterEmpty                 = "empty"
	VMParameterTooLong               = "too_long"
	VMParameterControlCharacter      = "control_character"
	VMParameterMustStartWithDash     = "must_start_with_dash"
	VMParameterArgumentFile          = "argument_file"
	VMParameterEntrypointOverride    = "entrypoint_override"
	VMParameterClasspathOverride     = "classpath_override"
	VMParameterCodeLoadingOverride   = "code_loading_override"
	VMParameterModuleOverride        = "module_override"
	VMParameterModuleAccessOverride  = "module_access_override"
	VMParameterVerificationOverride  = "verification_policy_override"
	VMParameterCompatibilityOverride = "vm_option_compatibility_override"
	VMParameterManagementAgent       = "management_agent"
	VMParameterAttachPolicyOverride  = "attach_policy_override"
	VMParameterSelfAttach            = "self_attach"
	VMParameterDiagnosticFilePath    = "diagnostic_file_path"
	VMParameterAgentHook             = "agent_hook"
	VMParameterBootclasspath         = "bootclasspath_override"
	VMParameterCustomSystemLoader    = "custom_system_loader"
	VMParameterTerminalOption        = "terminal_option"
	VMParameterErrorCommandHook      = "error_command_hook"
	VMParameterReservedProperty      = "reserved_property"
	VMParameterTemplateForbidden     = "template_forbidden"
	VMParameterSensitiveProperty     = "sensitive_property"
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

	if matchesAnyOption(lower, "-xx:flags", "-xx:vmoptionsfile") {
		return VMParameterArgumentFile
	}
	if matchesOption(lower, "-xoptionsfile") {
		return VMParameterArgumentFile
	}
	if matchesOption(lower, "-jar") || matchesOption(lower, "--source") {
		return VMParameterEntrypointOverride
	}
	if strings.HasPrefix(lower, "-version:") {
		return VMParameterEntrypointOverride
	}
	if matchesAnyOption(lower, "-cp", "-classpath", "--class-path") {
		return VMParameterClasspathOverride
	}
	if matchesProperty(lower, "-djava.class.path") {
		return VMParameterClasspathOverride
	}
	// Legacy Java 8 extension/endorsed directories and native-library search
	// properties bypass the governed classpath/asset resolver. The latter can
	// load arbitrary native code when reviewed Java source calls loadLibrary,
	// so these paths are code-loading surfaces rather than ordinary -D values.
	for _, property := range []string{
		"-djava.ext.dirs",
		"-djava.endorsed.dirs",
		"-djava.library.path",
		"-dsun.boot.library.path",
		"-dsun.boot.class.path",
		"-djdk.module.path",
	} {
		if matchesProperty(lower, property) {
			return VMParameterCodeLoadingOverride
		}
	}
	if matchesAnyOption(lower, "-xx:jvmcilibpath", "-xx:aotlibrary") {
		return VMParameterCodeLoadingOverride
	}
	if matchesAnyOption(lower, "-xxaltjvm", "-j-xxaltjvm") {
		return VMParameterCodeLoadingOverride
	}
	// OpenJ9 JITServer accepts compiled native methods from a remote endpoint.
	// Treat the complete client option family as external code loading rather
	// than allowing address, trust, or fallback combinations piecemeal.
	if strings.HasPrefix(lower, "-xx:jitserver") ||
		lower == "-xx:+usejitserver" || lower == "-xx:-usejitserver" ||
		lower == "-xx:+requirejitserver" || lower == "-xx:-requirejitserver" {
		return VMParameterCodeLoadingOverride
	}
	// OpenJ9 shared-class caches can restore cached/AOT code and expose
	// cache-directory plus destructive maintenance subcommands. The grammar is
	// comma-composable, so govern the full family.
	if strings.HasPrefix(lower, "-xshareclasses") {
		return VMParameterCodeLoadingOverride
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
	if lower == "-xverify:none" ||
		lower == "-noverify" ||
		lower == "-xx:-bytecodeverificationlocal" ||
		lower == "-xx:-bytecodeverificationremote" {
		return VMParameterVerificationOverride
	}
	if strings.HasPrefix(lower, "-javaagent") ||
		strings.HasPrefix(lower, "-agentlib") ||
		strings.HasPrefix(lower, "-agentpath") ||
		strings.HasPrefix(lower, "-xrun") ||
		strings.HasPrefix(lower, "-prof") {
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
	if lower == "-xx:+disableattachmechanism" ||
		lower == "-xx:-disableattachmechanism" ||
		matchesProperty(lower, "-dcom.ibm.tools.attach.enable") {
		return VMParameterAttachPolicyOverride
	}
	switch lower {
	case "-xx:+ignoreunrecognizedvmoptions", "-xx:-ignoreunrecognizedvmoptions",
		"-xx:+ignoreunrecognizedxxcolonoptions", "-xx:-ignoreunrecognizedxxcolonoptions":
		return VMParameterCompatibilityOverride
	}
	// These properties can launch or reconfigure the in-process JMX management
	// agent. A small combination of flags can expose an unauthenticated remote
	// control listener, so the whole management namespace is governed.
	if strings.HasPrefix(lower, "-dcom.sun.management.") ||
		matchesProperty(lower, "-dcom.sun.management") ||
		lower == "-xx:+managementserver" ||
		lower == "-xx:-managementserver" {
		return VMParameterManagementAgent
	}
	// -XX options that write JVM-authored files (crash logs, heap dumps,
	// replay/CDS archives, loaded-class lists) to a caller-chosen path: a
	// file-clobber and heap-secret-exfil-to-readable-path primitive triggered by
	// the JVM itself. Both the read-side (-XX:SharedArchiveFile) and write-side
	// (-XX:ArchiveClassesAtExit) CDS forms are covered.
	if matchesAnyOption(lower,
		"-xx:heapdumppath", "-xx:errorfile", "-xx:replaydatafile", "-xx:sharedarchivefile",
		"-xx:archiveclassesatexit", "-xx:dumploadedclasslist", "-xx:logfile",
		"-xx:startflightrecording", "-xx:flightrecorderoptions", "-xx:allocateheapat",
		"-xx:aotcache", "-xx:aotconfiguration", "-xx:aotcacheoutput", "-xx:aotmode",
		"-xx:sharedclasslistfile", "-xx:sharedarchiveconfigfile", "-xx:extrasharedclasslistfile",
		"-xx:perfdatasavefile", "-xx:pauseatstartupfile",
	) {
		return VMParameterDiagnosticFilePath
	}
	// Heap-dump trigger flags write an HPROF file to the JVM working directory
	// even when HeapDumpPath is absent. Sidecar VUsers share an OS identity and
	// working directory, so caller control of either the enabling or disabling
	// form would create an unreviewed cross-script heap disclosure surface.
	switch lower {
	case "-xx:+heapdumponoutofmemoryerror", "-xx:-heapdumponoutofmemoryerror",
		"-xx:+heapdumpbeforefullgc", "-xx:-heapdumpbeforefullgc",
		"-xx:+heapdumpafterfullgc", "-xx:-heapdumpafterfullgc",
		"-xx:+crashonoutofmemoryerror", "-xx:-crashonoutofmemoryerror",
		"-xx:+perfdatasavetofile", "-xx:-perfdatasavetofile":
		return VMParameterDiagnosticFilePath
	}
	// Unified logging can write to a caller-chosen path via ...:file=<path>.
	// Log-to-stdout/stderr forms (no file= output) stay allowed so the runner's
	// bounded output capture still surfaces GC/class logging.
	if strings.HasPrefix(lower, "-xlog") && strings.Contains(lower, "file=") {
		return VMParameterDiagnosticFilePath
	}
	if strings.HasPrefix(lower, "-xloggc:") {
		return VMParameterDiagnosticFilePath
	}
	if strings.HasPrefix(lower, "-xverbosegclog") ||
		strings.HasPrefix(lower, "-xjit") ||
		strings.HasPrefix(lower, "-xaot") ||
		lower == "-xx:+perftool" ||
		lower == "-xx:-perftool" {
		return VMParameterDiagnosticFilePath
	}
	// OpenJ9's legacy -Xlog:gc:<filename> form does not use `file=`. Preserve
	// only the explicit process-captured stdout/stderr destinations.
	if strings.HasPrefix(lower, "-xlog:gc:") {
		output := strings.TrimSpace(strings.SplitN(lower[len("-xlog:gc:"):], ":", 2)[0])
		if output != "" && output != "stdout" && output != "stderr" {
			return VMParameterDiagnosticFilePath
		}
	}
	// OpenJ9's -Xdump family can write heap/system artifacts and its tool form
	// can execute an external command on JVM lifecycle events. Caller-supplied
	// dump customization is never safe in the shared sidecar boundary.
	if strings.HasPrefix(lower, "-xdump") {
		return VMParameterDiagnosticFilePath
	}
	if strings.HasPrefix(lower, "-xtrace") {
		return VMParameterDiagnosticFilePath
	}
	if lower == "-xshare:dump" ||
		lower == "-xlog:help" ||
		strings.HasPrefix(lower, "-xlog:help:") ||
		lower == "-xx:+printflagsinitial" ||
		lower == "-xx:+printflagswithcomments" ||
		lower == "-xprintflags" {
		return VMParameterTerminalOption
	}
	switch lower {
	case "-xx:+dumpsharedspaces", "-xx:-dumpsharedspaces",
		"-xx:+printsharedarchiveandexit", "-xx:-printsharedarchiveandexit",
		"-xx:+pauseatstartup", "-xx:-pauseatstartup",
		"-xx:+pauseatexit", "-xx:-pauseatexit":
		return VMParameterTerminalOption
	}
	if matchesAnyOption(lower,
		"-h", "-help", "--help", "-?", "-x", "--help-extra",
		"-version", "--version", "-showversion", "--show-version", "--dry-run",
		"-fullversion",
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
	if ContainsSensitiveVMParameterValue(parameter) {
		return VMParameterSensitiveProperty
	}
	return ""
}

func isSensitiveSystemProperty(parameter string) bool {
	if len(parameter) < len("-D") || !strings.EqualFold(parameter[:len("-D")], "-D") {
		return false
	}
	equals := strings.IndexByte(parameter, '=')
	if equals <= len("-d") {
		return false
	}
	propertyName := parameter[len("-d"):equals]
	if credential.SensitiveName(propertyName) {
		return true
	}
	lowerName := strings.ToLower(propertyName)
	for _, fragment := range []string{"password", "token", "secret", "credential"} {
		if strings.Contains(lowerName, fragment) {
			return true
		}
	}
	normalizedName := credential.NormalizeName(propertyName)
	for _, suffix := range []string{
		"apikey", "accesskey", "privatekey", "secretkey", "passphrase",
		"connectionstring", "signature", "sessionid", "sessiontoken",
	} {
		if strings.HasSuffix(normalizedName, suffix) {
			return true
		}
	}
	return false
}

// ContainsSensitiveVMParameterValue applies the same value classifier used at
// disclosure boundaries to every process-observable JVM argument. A neutral
// property name does not make a high-entropy token or credential-bearing URL
// safe to expose through the OS process table. URL-aware scrubbing preserves
// ordinary hosts, paths, namespaces, and query values, so those remain valid
// arguments while userinfo and governed query/fragment values fail closed.
func ContainsSensitiveVMParameterValue(parameter string) bool {
	if isSensitiveSystemProperty(parameter) ||
		strings.Contains(parameter, credential.Redacted) ||
		containsCredentialAuthorizationValue(parameter) {
		return true
	}
	projected := scrub.Value(parameter, scrub.Policy{})
	if strings.Contains(projected, credential.Redacted) {
		return true
	}
	// Scrub the property value independently. In the complete token, an outer
	// neutral assignment such as "-Dlabel=" otherwise hides an inner
	// "api_key=..." from start-of-value credential boundaries.
	if value, ok := javaSystemPropertyValue(parameter); ok {
		projected = scrub.Value(value, scrub.Policy{})
		return strings.Contains(projected, credential.Redacted) ||
			containsCredentialAuthorizationValue(value)
	}
	return false
}

func javaSystemPropertyValue(parameter string) (string, bool) {
	if len(parameter) < len("-D") || !strings.EqualFold(parameter[:len("-D")], "-D") {
		return "", false
	}
	equals := strings.IndexByte(parameter, '=')
	if equals <= len("-D") {
		return "", false
	}
	return parameter[equals+1:], true
}

func containsCredentialAuthorizationValue(value string) bool {
	lower := strings.ToLower(value)
	for _, scheme := range []string{"basic", "bearer"} {
		cursor := 0
		for cursor < len(value) {
			at := strings.Index(lower[cursor:], scheme)
			if at < 0 {
				break
			}
			at += cursor
			beforeOK := at == 0 || strings.ContainsRune("=,:;([{\"' \t", rune(value[at-1]))
			after := at + len(scheme)
			if !beforeOK || after >= len(value) || (value[after] != ' ' && value[after] != '\t') {
				cursor = at + len(scheme)
				continue
			}
			payload := strings.TrimSpace(value[after:])
			if end := strings.IndexAny(payload, " \t\r\n,;)]}\"'"); end >= 0 {
				payload = payload[:end]
			}
			if payload == "" {
				cursor = after
				continue
			}
			if scheme == "bearer" || decodesBasicCredential(payload) {
				return true
			}
			cursor = after
		}
	}
	return false
}

func decodesBasicCredential(value string) bool {
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		decoded, err := encoding.DecodeString(value)
		if err == nil && strings.Contains(string(decoded), ":") {
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
