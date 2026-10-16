package javasidecar

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// handleCompile compiles a Java Vuser's source with the runtime its settings
// select, or with the operator's compiler JDK when that runtime is a JRE,
// against the lr API and the script and operator classpaths. Classpath
// entries the runner cannot provide are left out and reported: javac's own
// errors then say what they were needed for. One compile runs at a time, and
// javac, a JVM itself, holds a process slot and its memory charge as a VUser
// JVM would.
func (s *Server) handleCompile(w http.ResponseWriter, r *http.Request) {
	var request CompileRequest
	if !decodeRequestLimited(w, r, &request, MaxCompileRequestBytes) {
		return
	}
	if request.Contract != ContractVersion {
		writeError(w, http.StatusPreconditionFailed, "contract_version_mismatch")
		return
	}
	sources := make([]javarunner.CompileSource, 0, len(request.Sources))
	for _, source := range request.Sources {
		sources = append(sources, javarunner.CompileSource{Path: source.Path, Content: source.Content})
	}
	if len(sources) == 0 || len(sources) > javarunner.MaxCompileSources || !javarunner.ValidPooledTargetClass(request.TargetClass) {
		writeError(w, http.StatusBadRequest, javarunner.CompileSourcesInvalid)
		return
	}
	select {
	case s.compileSlots <- struct{}{}:
		defer func() { <-s.compileSlots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "compile_capacity_unavailable")
		return
	}
	operationCtx, _, finishOperation, operationError := s.beginProcessOperation(r.Context())
	if operationError != "" {
		writeError(w, http.StatusServiceUnavailable, operationError)
		return
	}
	defer finishOperation()
	ctx, cancel := context.WithTimeout(operationCtx, javarunner.DefaultCompileTimeout+30*time.Second)
	defer cancel()
	prepared, failure := s.prepareRuntimeFor(ctx, request.Runtime, true)
	if failure != nil {
		writeError(w, http.StatusConflict, failure.code)
		return
	}
	var compiler *javarunner.ResolvedJDK
	if !javarunner.JavaCompilerAvailable(prepared.jdk) && s.operator.CompilerJDKHome != "" {
		if compiler, failure = s.resolveCompilerJDK(ctx); failure != nil {
			writeError(w, http.StatusConflict, failure.code)
			return
		}
	}
	releaseSlot, failure := s.acquireProcessSlot(ctx)
	if failure != nil {
		writeError(w, http.StatusServiceUnavailable, failure.code)
		return
	}
	result, err := javarunner.CompileJavaSources(ctx, javarunner.CompileSpec{
		JDK:         prepared.jdk,
		Compiler:    compiler,
		Sources:     sources,
		Classpath:   append(append([]string(nil), prepared.scriptClasspath...), prepared.sharedClasspath...),
		APIJar:      prepared.harnessJar,
		TargetClass: request.TargetClass,
		WorkDir:     prepared.workDir,
	})
	response := CompileResponse{Contract: ContractVersion, UnavailableClasspath: prepared.missingClasspath}
	if errors.Is(err, javarunner.ErrProcessContainmentFailed) {
		// The slot stays held: it quarantines the unproven process tree.
		if reapDone := javarunner.FailedStartReapDone(err); reapDone != nil {
			s.trackFailedStartReapDone(reapDone, nil)
		}
		s.finishProcessCapacity(releaseSlot, err)
		writeError(w, http.StatusServiceUnavailable, "process_containment_unavailable")
		return
	}
	releaseSlot()
	if err != nil {
		var compileErr *javarunner.CompileError
		if !errors.As(err, &compileErr) {
			writeError(w, http.StatusServiceUnavailable, contextErrorCode(ctx))
			return
		}
		response.ErrorCode, response.Diagnostics = compileErr.Category, compileErr.Diagnostics
		writeJSON(w, http.StatusUnprocessableEntity, response)
		return
	}
	response.Jar, response.SHA256, response.Classes = result.Jar, result.SHA256, result.Classes
	response.Diagnostics, response.CompilerVersion, response.Release = result.Diagnostics, result.CompilerVersion, result.Release
	writeJSON(w, http.StatusOK, response)
}

// resolveCompilerJDK probes the operator's compiler JDK under the probe gate,
// with the same containment handling as a runtime probe.
func (s *Server) resolveCompilerJDK(ctx context.Context) (*javarunner.ResolvedJDK, *safeFailure) {
	release, failure := s.acquireProbeSlot(ctx)
	if failure != nil {
		return nil, failure
	}
	jdk, err := s.resolveJDK(ctx, javarunner.ResolveOptions{
		Selection:       javarunner.JDKSelectionSpecified,
		SpecifiedHome:   s.operator.CompilerJDKHome,
		AllowLegacyJava: s.operator.AllowLegacyJava,
		ProbeTimeout:    s.probeTimeout,
	})
	if err != nil {
		if errors.Is(err, javarunner.ErrProcessContainmentFailed) {
			if reapDone := javarunner.FailedStartReapDone(err); reapDone != nil {
				s.trackFailedStartReapDone(reapDone, nil)
			}
			s.finishProcessCapacity(release, err)
			return nil, &safeFailure{code: "process_containment_unavailable"}
		}
		release()
		return nil, &safeFailure{code: javarunner.CompileCompilerUnavailable}
	}
	release()
	return &jdk, nil
}
