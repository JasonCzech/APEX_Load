package javarunner

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

func TestValidateVMParametersAllowsExactSafeTokens(t *testing.T) {
	parameters := []string{
		"-Xmx512m",
		"-Dapp.env=perf",
		"-Dlabel=hello world",
		"-XX:+UseG1GC",
		"-Xlog:gc",
		"-Xlog:gc*=info",
		"-Xlog:disable",
		"--enable-preview",
	}
	if issues := ValidateVMParameters(parameters); len(issues) != 0 {
		t.Fatalf("ValidateVMParameters() = %#v, want no issues", issues)
	}
}

func TestValidateVMParametersRejectsUnsafeStructures(t *testing.T) {
	const entropy = "q7W9pL2xN8vC4mR6tY1kH5sF3dJ0aB9uE2zG"
	tests := []struct {
		name     string
		value    string
		category string
	}{
		{name: "empty", value: "", category: VMParameterEmpty},
		{name: "control character", value: "-Dkey=value\nnext", category: VMParameterControlCharacter},
		{name: "must start with dash", value: "main.Class", category: VMParameterMustStartWithDash},
		{name: "argument file", value: "@options.txt", category: VMParameterArgumentFile},
		{name: "XX flags file", value: "-XX:Flags=/tmp/options", category: VMParameterArgumentFile},
		{name: "XX VM options file", value: "-XX:VMOptionsFile=/tmp/options", category: VMParameterArgumentFile},
		{name: "OpenJ9 options file", value: "-Xoptionsfile=/tmp/options", category: VMParameterArgumentFile},
		{name: "jar entrypoint", value: "-jar", category: VMParameterEntrypointOverride},
		{name: "source entrypoint", value: "--source=21", category: VMParameterEntrypointOverride},
		{name: "legacy alternate JRE selection", value: "-VeRsIoN:1.8+", category: VMParameterEntrypointOverride},
		{name: "short classpath", value: "-cp", category: VMParameterClasspathOverride},
		{name: "long classpath", value: "--class-path=/tmp/classes", category: VMParameterClasspathOverride},
		{name: "classpath property", value: "-Djava.class.path=/tmp/classes", category: VMParameterClasspathOverride},
		{name: "legacy extension directory", value: "-Djava.ext.dirs=/tmp/extensions", category: VMParameterCodeLoadingOverride},
		{name: "legacy endorsed directory", value: "-Djava.endorsed.dirs=/tmp/endorsed", category: VMParameterCodeLoadingOverride},
		{name: "native library path", value: "-Djava.library.path=/tmp/native", category: VMParameterCodeLoadingOverride},
		{name: "boot native library path", value: "-Dsun.boot.library.path=/tmp/native", category: VMParameterCodeLoadingOverride},
		{name: "legacy boot class path property", value: "-Dsun.boot.class.path=/tmp/classes", category: VMParameterCodeLoadingOverride},
		{name: "module path property", value: "-Djdk.module.path=/tmp/modules", category: VMParameterCodeLoadingOverride},
		{name: "JVMCI native library path", value: "-XX:JVMCILibPath=/tmp/native", category: VMParameterCodeLoadingOverride},
		{name: "legacy AOT native library", value: "-XX:AOTLibrary=/tmp/native.so", category: VMParameterCodeLoadingOverride},
		{name: "alternate JVM library", value: "-XXaltjvm=/tmp/alternate", category: VMParameterCodeLoadingOverride},
		{name: "wrapper alternate JVM library", value: "-J-XXaltjvm=/tmp/alternate", category: VMParameterCodeLoadingOverride},
		{name: "OpenJ9 JIT server enabled", value: "-XX:+UseJITServer", category: VMParameterCodeLoadingOverride},
		{name: "OpenJ9 JIT server required", value: "-XX:+RequireJITServer", category: VMParameterCodeLoadingOverride},
		{name: "OpenJ9 JIT server address", value: "-XX:JITServerAddress=compiler.example.test", category: VMParameterCodeLoadingOverride},
		{name: "OpenJ9 JIT server root certificate", value: "-XX:JITServerRootCerts=/tmp/roots.pem", category: VMParameterCodeLoadingOverride},
		{name: "OpenJ9 shared classes cache", value: "-Xshareclasses:cacheDir=/tmp/cache,restoreFromSnapshot", category: VMParameterCodeLoadingOverride},
		{name: "OpenJ9 shared classes cache destroy", value: "-Xshareclasses:destroyAll", category: VMParameterCodeLoadingOverride},
		{name: "module main", value: "--module=app/main.Main", category: VMParameterModuleOverride},
		{name: "module path", value: "-p=/tmp/modules", category: VMParameterModuleOverride},
		{name: "add opens split", value: "--add-opens", category: VMParameterModuleAccessOverride},
		{name: "add opens inline", value: "--add-opens=java.base/java.lang=ALL-UNNAMED", category: VMParameterModuleAccessOverride},
		{name: "add exports", value: "--add-exports=java.base/jdk.internal.misc=ALL-UNNAMED", category: VMParameterModuleAccessOverride},
		{name: "add reads", value: "--add-reads=app=ALL-UNNAMED", category: VMParameterModuleAccessOverride},
		{name: "enable native access", value: "--enable-native-access=ALL-UNNAMED", category: VMParameterModuleAccessOverride},
		{name: "disable all bytecode verification", value: "-XvErIfY:NoNe", category: VMParameterVerificationOverride},
		{name: "legacy disable verification", value: "-NoVeRiFy", category: VMParameterVerificationOverride},
		{name: "disable local bytecode verification", value: "-XX:-ByTeCoDeVeRiFiCaTiOnLoCaL", category: VMParameterVerificationOverride},
		{name: "disable remote bytecode verification", value: "-XX:-BytecodeVerificationRemote", category: VMParameterVerificationOverride},
		{name: "self attach", value: "-Djdk.attach.allowAttachSelf=true", category: VMParameterSelfAttach},
		{name: "caller enables attach", value: "-XX:-DisableAttachMechanism", category: VMParameterAttachPolicyOverride},
		{name: "caller duplicates attach disable", value: "-XX:+DisableAttachMechanism", category: VMParameterAttachPolicyOverride},
		{name: "caller enables OpenJ9 attach", value: "-Dcom.ibm.tools.attach.enable=yes", category: VMParameterAttachPolicyOverride},
		{name: "caller duplicates OpenJ9 attach disable", value: "-Dcom.ibm.tools.attach.enable=no", category: VMParameterAttachPolicyOverride},
		{name: "caller ignores unknown VM options", value: "-XX:+IgnoreUnrecognizedVMOptions", category: VMParameterCompatibilityOverride},
		{name: "caller rejects unknown VM options", value: "-XX:-IgnoreUnrecognizedVMOptions", category: VMParameterCompatibilityOverride},
		{name: "caller ignores unknown OpenJ9 colon options", value: "-XX:+IgnoreUnrecognizedXXColonOptions", category: VMParameterCompatibilityOverride},
		{name: "caller rejects unknown OpenJ9 colon options", value: "-XX:-IgnoreUnrecognizedXXColonOptions", category: VMParameterCompatibilityOverride},
		{name: "remote management listener", value: "-Dcom.sun.management.jmxremote.port=9010", category: VMParameterManagementAgent},
		{name: "management authentication disable", value: "-Dcom.sun.management.jmxremote.authenticate=false", category: VMParameterManagementAgent},
		{name: "management config file", value: "-Dcom.sun.management.config.file=/tmp/management.properties", category: VMParameterManagementAgent},
		{name: "exact management property", value: "-DcOm.SuN.MaNaGeMeNt", category: VMParameterManagementAgent},
		{name: "exact management property assignment", value: "-Dcom.sun.management=true", category: VMParameterManagementAgent},
		{name: "management server enabled", value: "-XX:+ManagementServer", category: VMParameterManagementAgent},
		{name: "management server disabled by caller", value: "-XX:-ManagementServer", category: VMParameterManagementAgent},
		{name: "heap dump path", value: "-XX:HeapDumpPath=/etc/cron.d/x", category: VMParameterDiagnosticFilePath},
		{name: "heap dump on OOM enabled", value: "-XX:+HeapDumpOnOutOfMemoryError", category: VMParameterDiagnosticFilePath},
		{name: "heap dump on OOM disabled by caller", value: "-XX:-HeapDumpOnOutOfMemoryError", category: VMParameterDiagnosticFilePath},
		{name: "heap dump before full GC enabled", value: "-XX:+HeapDumpBeforeFullGC", category: VMParameterDiagnosticFilePath},
		{name: "heap dump before full GC disabled by caller", value: "-XX:-HeapDumpBeforeFullGC", category: VMParameterDiagnosticFilePath},
		{name: "heap dump after full GC enabled", value: "-XX:+HeapDumpAfterFullGC", category: VMParameterDiagnosticFilePath},
		{name: "heap dump after full GC disabled by caller", value: "-XX:-HeapDumpAfterFullGC", category: VMParameterDiagnosticFilePath},
		{name: "crash on OOM enabled", value: "-XX:+CrashOnOutOfMemoryError", category: VMParameterDiagnosticFilePath},
		{name: "crash on OOM disabled by caller", value: "-XX:-CrashOnOutOfMemoryError", category: VMParameterDiagnosticFilePath},
		{name: "error file", value: "-XX:ErrorFile=/tmp/hs_err.log", category: VMParameterDiagnosticFilePath},
		{name: "replay data file", value: "-XX:ReplayDataFile=/tmp/replay", category: VMParameterDiagnosticFilePath},
		{name: "shared archive file", value: "-XX:SharedArchiveFile=/tmp/app.jsa", category: VMParameterDiagnosticFilePath},
		{name: "shared class list file", value: "-XX:SharedClassListFile=/tmp/classes.lst", category: VMParameterDiagnosticFilePath},
		{name: "shared archive config file", value: "-XX:SharedArchiveConfigFile=/tmp/archive.cfg", category: VMParameterDiagnosticFilePath},
		{name: "extra shared class list file", value: "-XX:ExtraSharedClassListFile=/tmp/extra.lst", category: VMParameterDiagnosticFilePath},
		{name: "perf data save path", value: "-XX:PerfDataSaveFile=/tmp/perf.data", category: VMParameterDiagnosticFilePath},
		{name: "pause at startup file", value: "-XX:PauseAtStartupFile=/tmp/pause", category: VMParameterDiagnosticFilePath},
		{name: "archive classes at exit", value: "-XX:ArchiveClassesAtExit=/etc/target", category: VMParameterDiagnosticFilePath},
		{name: "dump loaded class list", value: "-XX:DumpLoadedClassList=/tmp/classes.lst", category: VMParameterDiagnosticFilePath},
		{name: "xlog to file", value: "-Xlog:gc:file=/opt/apexload/harness/Harness.jar", category: VMParameterDiagnosticFilePath},
		{name: "xlog to file bare", value: "-Xlog:file=/tmp/x", category: VMParameterDiagnosticFilePath},
		{name: "legacy GC log file", value: "-Xloggc:/tmp/gc.log", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 verbose GC default log", value: "-Xverbosegclog", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 verbose GC selected log", value: "-Xverbosegclog:/tmp/gc.log,10,2", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 legacy Xlog GC file", value: "-Xlog:gc:/tmp/gc.log", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 JIT verbose log", value: "-Xjit:vlog=/tmp/jit.log", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 JIT limit file", value: "-Xjit:loadLimitFile=/tmp/limits", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 AOT limit file", value: "-Xaot:limitFile=/tmp/limits", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 JIT perf tool", value: "-Xjit:perfTool", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 VM perf tool", value: "-XX:+PerfTool", category: VMParameterDiagnosticFilePath},
		{name: "flight recording file", value: "-XX:StartFlightRecording=filename=/tmp/run.jfr", category: VMParameterDiagnosticFilePath},
		{name: "flight recorder repository", value: "-XX:FlightRecorderOptions=repository=/tmp/jfr", category: VMParameterDiagnosticFilePath},
		{name: "diagnostic log file", value: "-XX:LogFile=/tmp/hotspot.log", category: VMParameterDiagnosticFilePath},
		{name: "file-backed heap path", value: "-XX:AllocateHeapAt=/tmp/heap", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 heap dump", value: "-Xdump:heap:events=vmstop,file=/tmp/heap.dmp", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 tool dump hook", value: "-Xdump:tool:events=vmstart,exec=/tmp/run", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 dump disable override", value: "-Xdump:none", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 trace output", value: "-Xtrace:output=/tmp/trace", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 trace properties", value: "-Xtrace:properties=/tmp/trace.properties", category: VMParameterDiagnosticFilePath},
		{name: "OpenJ9 trace dump trigger", value: "-Xtrace:trigger=method{java/lang/String.*,javadump}", category: VMParameterDiagnosticFilePath},
		{name: "HotSpot shared archive dump", value: "-Xshare:dump", category: VMParameterTerminalOption},
		{name: "unified logging help", value: "-Xlog:help", category: VMParameterTerminalOption},
		{name: "initial VM flags", value: "-XX:+PrInTfLaGsInItIaL", category: VMParameterTerminalOption},
		{name: "VM flags with comments", value: "-XX:+PrInTfLaGsWiThCoMmEnTs", category: VMParameterTerminalOption},
		{name: "legacy VM flags", value: "-XpRiNtFlAgS", category: VMParameterTerminalOption},
		{name: "dump shared spaces enabled", value: "-XX:+DumpSharedSpaces", category: VMParameterTerminalOption},
		{name: "dump shared spaces disabled by caller", value: "-XX:-DumpSharedSpaces", category: VMParameterTerminalOption},
		{name: "print shared archive and exit", value: "-XX:+PrintSharedArchiveAndExit", category: VMParameterTerminalOption},
		{name: "perf data save enabled", value: "-XX:+PerfDataSaveToFile", category: VMParameterDiagnosticFilePath},
		{name: "perf data save disabled by caller", value: "-XX:-PerfDataSaveToFile", category: VMParameterDiagnosticFilePath},
		{name: "pause at startup enabled", value: "-XX:+PauseAtStartup", category: VMParameterTerminalOption},
		{name: "pause at startup disabled by caller", value: "-XX:-PauseAtStartup", category: VMParameterTerminalOption},
		{name: "pause at exit enabled", value: "-XX:+PauseAtExit", category: VMParameterTerminalOption},
		{name: "AOT cache input", value: "-XX:AOTCache=/tmp/cache.aot", category: VMParameterDiagnosticFilePath},
		{name: "AOT configuration input", value: "-XX:AOTConfiguration=/tmp/config.aotconf", category: VMParameterDiagnosticFilePath},
		{name: "AOT cache output", value: "-XX:AOTCacheOutput=/tmp/cache.aot", category: VMParameterDiagnosticFilePath},
		{name: "AOT workflow mode", value: "-XX:AOTMode=create", category: VMParameterDiagnosticFilePath},
		{name: "java agent", value: "-javaagent:/tmp/agent.jar", category: VMParameterAgentHook},
		{name: "native agent", value: "-agentpath:/tmp/agent.so", category: VMParameterAgentHook},
		{name: "legacy native agent", value: "-Xrunjdwp:transport=dt_socket", category: VMParameterAgentHook},
		{name: "legacy profiler default output", value: "-PrOf", category: VMParameterAgentHook},
		{name: "legacy profiler explicit output", value: "-prof:/tmp/java.prof", category: VMParameterAgentHook},
		{name: "boot classpath", value: "-Xbootclasspath/a:/tmp/classes", category: VMParameterBootclasspath},
		{name: "custom loader", value: "-Djava.system.class.loader=custom.Loader", category: VMParameterCustomSystemLoader},
		{name: "version terminates", value: "--version", category: VMParameterTerminalOption},
		{name: "full version terminates", value: "-FuLlVeRsIoN", category: VMParameterTerminalOption},
		{name: "option terminator", value: "--", category: VMParameterTerminalOption},
		{name: "lone dash", value: "-", category: VMParameterTerminalOption},
		{name: "dry run terminates", value: "--dry-run", category: VMParameterTerminalOption},
		{name: "list modules terminates", value: "--list-modules", category: VMParameterTerminalOption},
		{name: "error command", value: "-XX:OnError=/bin/sh", category: VMParameterErrorCommandHook},
		{name: "oom command", value: "-XX:OnOutOfMemoryError=/bin/sh", category: VMParameterErrorCommandHook},
		{name: "reserved property", value: "-Dapexload.secret=value", category: VMParameterReservedProperty},
		{name: "template", value: "-Dapp.env={{env.PERF_ENV}}", category: VMParameterTemplateForbidden},
		{name: "password property", value: "-Ddatabase.password=value", category: VMParameterSensitiveProperty},
		{name: "token property", value: "-DserviceAuthToken=value", category: VMParameterSensitiveProperty},
		{name: "secret property", value: "-Dclient-secret=value", category: VMParameterSensitiveProperty},
		{name: "credential property", value: "-Dcredential_file=value", category: VMParameterSensitiveProperty},
		{name: "api key property alias", value: "-Dapi.key=short", category: VMParameterSensitiveProperty},
		{name: "private key property alias", value: "-Dprivate_key=short", category: VMParameterSensitiveProperty},
		{name: "camel case API key property", value: "-DserviceApiKey=short", category: VMParameterSensitiveProperty},
		{name: "camel case access key property", value: "-DcloudAccessKey=short", category: VMParameterSensitiveProperty},
		{name: "camel case private key property", value: "-DtlsPrivateKey=short", category: VMParameterSensitiveProperty},
		{name: "camel case passphrase property", value: "-DdbPassphrase=short", category: VMParameterSensitiveProperty},
		{name: "nested sensitive assignment", value: "-Dlabel=api_key=short-secret", category: VMParameterSensitiveProperty},
		{name: "nested basic authorization", value: "-Dlabel=Basic dXNlcjpwYXNz", category: VMParameterSensitiveProperty},
		{name: "nested bearer authorization", value: "-Dlabel=Bearer short-token", category: VMParameterSensitiveProperty},
		{name: "neutral high entropy property", value: "-Dlabel=" + entropy, category: VMParameterSensitiveProperty},
		{name: "arbitrary high entropy option", value: "-Xopaque:" + entropy, category: VMParameterSensitiveProperty},
		{name: "URL userinfo", value: "-Dendpoint=https://user:short@example.test/path", category: VMParameterSensitiveProperty},
		{name: "URL sensitive query", value: "-Dendpoint=https://example.test/path?api_key=short", category: VMParameterSensitiveProperty},
		{name: "URL sensitive fragment", value: "-Dendpoint=https://example.test/path#token=short", category: VMParameterSensitiveProperty},
		{name: "literal disclosure sentinel", value: "-Dlabel=" + credential.Redacted, category: VMParameterSensitiveProperty},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := ValidateVMParameters([]string{"-Xms64m", test.value})
			if len(issues) != 1 {
				t.Fatalf("ValidateVMParameters() = %#v, want one issue", issues)
			}
			if issues[0].Index != 1 || issues[0].Category != test.category {
				t.Fatalf("issue = %#v, want index 1 category %q", issues[0], test.category)
			}
		})
	}
}

func TestValidateVMParametersPreservesSafeURLAndStructuredValues(t *testing.T) {
	parameters := []string{
		"-Dendpoint=https://example.test/q7W9pL2xN8vC4mR6tY1kH5sF3dJ0aB9uE2zG?view=full#section",
		`-Dconfig={"label": "hello"}`,
		"-Xlog:gc:stdout",
		"-Xlog:gc:stderr",
	}
	if issues := ValidateVMParameters(parameters); len(issues) != 0 {
		t.Fatalf("safe URL/structured JVM parameters rejected: %#v", issues)
	}
}

func TestValidateVMParametersBounds(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		parameters := make([]string, MaxVMParameterCount+1)
		for index := range parameters {
			parameters[index] = "-Xmx64m"
		}
		assertCollectionIssue(t, ValidateVMParameters(parameters), VMParameterTooMany)
	})

	t.Run("token bytes", func(t *testing.T) {
		parameter := "-" + strings.Repeat("x", MaxVMParameterBytes)
		issues := ValidateVMParameters([]string{parameter})
		if len(issues) != 1 || issues[0].Index != 0 || issues[0].Category != VMParameterTooLong {
			t.Fatalf("issues = %#v, want one too-long token issue", issues)
		}
	})

	t.Run("total bytes", func(t *testing.T) {
		parameters := make([]string, 17)
		for index := range parameters {
			parameters[index] = "-" + strings.Repeat("x", MaxVMParameterBytes-1)
		}
		assertCollectionIssue(t, ValidateVMParameters(parameters), VMParameterTotalTooLarge)
	})
}

func TestValidationIssueDoesNotExposeTokenValue(t *testing.T) {
	const secret = "sensitive-value"
	issues := ValidateVMParameters([]string{"-Dapexload.secret=" + secret})
	if len(issues) != 1 {
		t.Fatalf("issues = %#v, want one issue", issues)
	}
	if strings.Contains(issues[0].Error(), secret) {
		t.Fatalf("issue error %q exposed parameter value", issues[0].Error())
	}
}

func TestSensitiveVMParameterValidationDoesNotExposeValue(t *testing.T) {
	const secret = "top-secret-value"
	issues := ValidateVMParameters([]string{"-Dservice.token=" + secret})
	if len(issues) != 1 || issues[0].Category != VMParameterSensitiveProperty {
		t.Fatalf("issues = %#v, want one sensitive-property issue", issues)
	}
	if strings.Contains(issues[0].Error(), secret) {
		t.Fatalf("issue error %q exposed parameter value", issues[0].Error())
	}
}

func assertCollectionIssue(t *testing.T, issues []ValidationIssue, category string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Index == -1 && issue.Category == category {
			return
		}
	}
	t.Fatalf("issues = %#v, want collection issue %q", issues, category)
}
