package java_vuser

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

// localCompileSlot admits one local compile at a time; javac is memory-heavy.
var localCompileSlot = make(chan struct{}, 1)

// CompileSource compiles a Java Vuser's source on the operator's Java runner:
// the loopback sidecar in sidecar mode, or a contained javac child of this
// process in local mode, mirroring how each mode runs Java VUsers. A compile
// that ran and failed is a response with ErrorCode and javac's diagnostics. A
// refused compile is a *javasidecar.RemoteError with a stable code.
func CompileSource(ctx context.Context, operator config.JavaVuserOperatorConfig, request javasidecar.CompileRequest) (javasidecar.CompileResponse, error) {
	if !operator.Enabled {
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusServiceUnavailable, "runner_disabled")
	}
	if operator.RunnerMode == config.JavaVuserRunnerModeSidecar {
		client, err := javasidecar.NewClientWithToken(operator.RunnerURL, operator.AuthToken, nil)
		if err != nil {
			return javasidecar.CompileResponse{}, compileRefusal(http.StatusServiceUnavailable, "runner_unavailable")
		}
		return client.Compile(ctx, request)
	}
	return compileLocally(ctx, operator, request)
}

func compileRefusal(status int, code string) error {
	return &javasidecar.RemoteError{StatusCode: status, Code: code}
}

// compileLocally follows the sidecar's /v1/compile: the same runtime and
// classpath resolution as a local VUser, with missing classpath entries left
// out and reported.
func compileLocally(ctx context.Context, operator config.JavaVuserOperatorConfig, request javasidecar.CompileRequest) (javasidecar.CompileResponse, error) {
	// The same overall bound as the sidecar's: waiting for a process slot
	// counts against it.
	ctx, cancel := context.WithTimeout(ctx, javarunner.DefaultCompileTimeout+30*time.Second)
	defer cancel()
	sources := make([]javarunner.CompileSource, 0, len(request.Sources))
	for _, source := range request.Sources {
		sources = append(sources, javarunner.CompileSource{Path: source.Path, Content: source.Content})
	}
	if len(sources) == 0 || len(sources) > javarunner.MaxCompileSources || !javarunner.ValidPooledTargetClass(request.TargetClass) {
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusBadRequest, javarunner.CompileSourcesInvalid)
	}
	if err := subprocessguard.ValidateAmbientCredentials(); err != nil {
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusServiceUnavailable, "ambient_credentials_detected")
	}
	select {
	case localCompileSlot <- struct{}{}:
		defer func() { <-localCompileSlot }()
	default:
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusServiceUnavailable, "compile_capacity_unavailable")
	}
	workDir, err := resolveLocalWorkDir(operator.WorkDir)
	if err != nil {
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusServiceUnavailable, "work_dir_unavailable")
	}
	selection := javarunner.JDKSelection(strings.TrimSpace(request.Runtime.JDKSelection))
	if selection == "" {
		selection = javarunner.JDKSelectionInternal
	}
	if selection != javarunner.JDKSelectionInternal && selection != javarunner.JDKSelectionSpecified {
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusConflict, "jdk_selection_invalid")
	}
	if selection == javarunner.JDKSelectionSpecified && strings.TrimSpace(request.Runtime.JDKHome) == "" {
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusConflict, "jdk_home_required")
	}
	explicitCommand := ""
	if operator.RunnerCommandExplicit {
		explicitCommand = operator.RunnerCommand
	}
	jdk, refusal := resolveLocalCompileJDK(ctx, javarunner.ResolveOptions{
		Selection:           selection,
		SpecifiedHome:       request.Runtime.JDKHome,
		ExplicitJavaCommand: explicitCommand,
		DefaultHome:         operator.DefaultJDKHome,
		AllowLegacyJava:     operator.AllowLegacyJava,
	}, "jdk_unavailable")
	if refusal != nil {
		return javasidecar.CompileResponse{}, refusal
	}
	harness := resolveRunnerPath(workDir, strings.TrimSpace(operator.HarnessJar))
	if info, err := os.Stat(harness); strings.TrimSpace(operator.HarnessJar) == "" || err != nil || !info.Mode().IsRegular() {
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusConflict, "harness_unavailable")
	}
	classpath := append([]string(nil), request.Runtime.Classpath...)
	if operator.ClasspathUploadEnabled && len(request.Runtime.ClasspathAssets) > 0 {
		assets := make([]javarunner.ClasspathAsset, 0, len(request.Runtime.ClasspathAssets))
		for _, asset := range request.Runtime.ClasspathAssets {
			assets = append(assets, javarunner.ClasspathAsset{Entry: asset.Entry, SHA256: asset.SHA256, StorageRef: asset.StorageRef})
		}
		materialized, issue := javarunner.MaterializeClasspathAssets(classpath, assets, workDir)
		if issue != nil {
			return javasidecar.CompileResponse{}, compileRefusal(http.StatusConflict, issue.Category)
		}
		classpath = materialized
	}
	scriptClasspath, missing, issue := javarunner.ResolveCompileClasspath(classpath, workDir)
	if issue != nil {
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusConflict, "classpath_entry_invalid")
	}
	sharedClasspath, sharedIssue := javarunner.ResolveClasspath(operator.Classpath, workDir)
	if sharedIssue != nil {
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusConflict, "shared_classpath_unavailable")
	}
	var compiler *javarunner.ResolvedJDK
	if !javarunner.JavaCompilerAvailable(jdk) && strings.TrimSpace(operator.CompilerJDKHome) != "" {
		resolved, refusal := resolveLocalCompileJDK(ctx, javarunner.ResolveOptions{
			Selection:       javarunner.JDKSelectionSpecified,
			SpecifiedHome:   operator.CompilerJDKHome,
			AllowLegacyJava: operator.AllowLegacyJava,
		}, javarunner.CompileCompilerUnavailable)
		if refusal != nil {
			return javasidecar.CompileResponse{}, refusal
		}
		compiler = &resolved
	}
	// javac is a JVM too: it holds a VUser process slot while it runs.
	releaseSlot, err := acquireProcessSlot(ctx)
	if err != nil {
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusServiceUnavailable, "process_capacity_unavailable")
	}
	result, err := javarunner.CompileJavaSources(ctx, javarunner.CompileSpec{
		JDK:         jdk,
		Compiler:    compiler,
		Sources:     sources,
		Classpath:   append(scriptClasspath, sharedClasspath...),
		APIJar:      harness,
		TargetClass: request.TargetClass,
		WorkDir:     workDir,
	})
	response := javasidecar.CompileResponse{Contract: javasidecar.ContractVersion, UnavailableClasspath: missing}
	if errors.Is(err, javarunner.ErrProcessContainmentFailed) {
		// The slot stays held: it quarantines the unproven process tree.
		finishLocalProcessCapacity(releaseSlot, err)
		return javasidecar.CompileResponse{}, compileRefusal(http.StatusServiceUnavailable, "process_containment_unavailable")
	}
	releaseSlot()
	if err != nil {
		var compileErr *javarunner.CompileError
		if !errors.As(err, &compileErr) {
			return javasidecar.CompileResponse{}, compileRefusal(http.StatusServiceUnavailable, "execution_canceled")
		}
		response.ErrorCode, response.Diagnostics = compileErr.Category, compileErr.Diagnostics
		return response, nil
	}
	response.Jar, response.SHA256, response.Classes = result.Jar, result.SHA256, result.Classes
	response.Diagnostics, response.CompilerVersion, response.Release = result.Diagnostics, result.CompilerVersion, result.Release
	return response, nil
}

// resolveLocalCompileJDK probes a JDK under the local probe gate, closing the
// local process gate on a containment failure as a VUser's probe does.
func resolveLocalCompileJDK(ctx context.Context, options javarunner.ResolveOptions, unavailableCode string) (javarunner.ResolvedJDK, error) {
	release, err := acquireLocalRuntimeProbe(ctx)
	if err != nil {
		return javarunner.ResolvedJDK{}, compileRefusal(http.StatusServiceUnavailable, "probe_capacity_unavailable")
	}
	jdk, err := resolveJavaVuserJDK(ctx, options)
	if err != nil {
		if errors.Is(err, javarunner.ErrProcessContainmentFailed) {
			finishLocalProcessCapacity(release, err)
			return javarunner.ResolvedJDK{}, compileRefusal(http.StatusServiceUnavailable, "process_containment_unavailable")
		}
		finishLocalProcessCapacity(release, nil)
		return javarunner.ResolvedJDK{}, compileRefusal(http.StatusConflict, unavailableCode)
	}
	finishLocalProcessCapacity(release, nil)
	return jdk, nil
}
