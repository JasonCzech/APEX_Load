package javarunner

import (
	"strings"
	"testing"
)

func TestValidateVMParametersAllowsExactSafeTokens(t *testing.T) {
	parameters := []string{
		"-Xmx512m",
		"-Dapp.env=perf",
		"-Dlabel=hello world",
		"-XX:+UseG1GC",
		"-XX:+HeapDumpOnOutOfMemoryError",
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
		{name: "jar entrypoint", value: "-jar", category: VMParameterEntrypointOverride},
		{name: "source entrypoint", value: "--source=21", category: VMParameterEntrypointOverride},
		{name: "short classpath", value: "-cp", category: VMParameterClasspathOverride},
		{name: "long classpath", value: "--class-path=/tmp/classes", category: VMParameterClasspathOverride},
		{name: "classpath property", value: "-Djava.class.path=/tmp/classes", category: VMParameterClasspathOverride},
		{name: "module main", value: "--module=app/main.Main", category: VMParameterModuleOverride},
		{name: "module path", value: "-p=/tmp/modules", category: VMParameterModuleOverride},
		{name: "add opens split", value: "--add-opens", category: VMParameterModuleAccessOverride},
		{name: "add opens inline", value: "--add-opens=java.base/java.lang=ALL-UNNAMED", category: VMParameterModuleAccessOverride},
		{name: "add exports", value: "--add-exports=java.base/jdk.internal.misc=ALL-UNNAMED", category: VMParameterModuleAccessOverride},
		{name: "add reads", value: "--add-reads=app=ALL-UNNAMED", category: VMParameterModuleAccessOverride},
		{name: "enable native access", value: "--enable-native-access=ALL-UNNAMED", category: VMParameterModuleAccessOverride},
		{name: "self attach", value: "-Djdk.attach.allowAttachSelf=true", category: VMParameterSelfAttach},
		{name: "heap dump path", value: "-XX:HeapDumpPath=/etc/cron.d/x", category: VMParameterDiagnosticFilePath},
		{name: "error file", value: "-XX:ErrorFile=/tmp/hs_err.log", category: VMParameterDiagnosticFilePath},
		{name: "replay data file", value: "-XX:ReplayDataFile=/tmp/replay", category: VMParameterDiagnosticFilePath},
		{name: "shared archive file", value: "-XX:SharedArchiveFile=/tmp/app.jsa", category: VMParameterDiagnosticFilePath},
		{name: "archive classes at exit", value: "-XX:ArchiveClassesAtExit=/etc/target", category: VMParameterDiagnosticFilePath},
		{name: "dump loaded class list", value: "-XX:DumpLoadedClassList=/tmp/classes.lst", category: VMParameterDiagnosticFilePath},
		{name: "xlog to file", value: "-Xlog:gc:file=/opt/apexload/harness/Harness.jar", category: VMParameterDiagnosticFilePath},
		{name: "xlog to file bare", value: "-Xlog:file=/tmp/x", category: VMParameterDiagnosticFilePath},
		{name: "java agent", value: "-javaagent:/tmp/agent.jar", category: VMParameterAgentHook},
		{name: "native agent", value: "-agentpath:/tmp/agent.so", category: VMParameterAgentHook},
		{name: "legacy native agent", value: "-Xrunjdwp:transport=dt_socket", category: VMParameterAgentHook},
		{name: "boot classpath", value: "-Xbootclasspath/a:/tmp/classes", category: VMParameterBootclasspath},
		{name: "custom loader", value: "-Djava.system.class.loader=custom.Loader", category: VMParameterCustomSystemLoader},
		{name: "version terminates", value: "--version", category: VMParameterTerminalOption},
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
