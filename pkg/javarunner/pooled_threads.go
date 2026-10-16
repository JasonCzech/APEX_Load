package javarunner

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/zipbounds"
)

// LoadRunner thread mode.
//
// LoadRunner runs a Java Vuser script's VUsers as threads of one JVM with one
// class loader (UseThreads=1, enable_CL_per_vuser=0), so libraries, their
// static state and their native code load once. Thread mode keeps that model
// on the shared-JVM host: libraries go to the host's shared loader, and only
// the jars holding the script's own classes, which call the per-VUser lr API,
// load per slot. A library loaded once is no different from LoadRunner's, so
// the isolated shared mode's refusals of native code, legacy runtimes and
// large or manifest-linked jars do not apply to libraries. Per-slot jars keep
// every check, since they load once per VUser.

// SharedJVMReasonLoadRunnerThreads is the stable reason a thread-mode claim
// stays dedicated on a runner whose operator has not enabled thread mode.
const SharedJVMReasonLoadRunnerThreads = "shared_jvm_loadrunner_threads_disabled"

// threadLibraryJarLimits bounds a library jar thread mode loads once. Whole
// middleware client jars exceed the per-slot limits: WebLogic 9.2's holds more
// than 34,000 entries.
var threadLibraryJarLimits = zipbounds.Limits{MaxArchiveBytes: 2 << 30, MaxEntries: 65535, MaxCentralDirectoryBytes: 32 << 20, MaxNameBytes: 4096, MaxExtraBytes: 4096, MaxCommentBytes: 4096}

const (
	// threadScanBytes bounds the class bytes thread mode reads to find the
	// jars that call the lr API.
	threadScanBytes = 1 << 30
	// threadScanEntryBytes bounds one class file.
	threadScanEntryBytes = 16 << 20
)

// loadRunnerThreadsRuntimeSupported admits any HotSpot or OpenJ9 runtime the
// runner resolved; its own floor (17, or 8 under the legacy opt-in) applied
// when it resolved the JDK.
func loadRunnerThreadsRuntimeSupported(jdk ResolvedJDK) bool {
	return jdk.Major >= MinimumLegacyJavaMajor && (jdk.VMFamily == JavaVMHotSpot || jdk.VMFamily == JavaVMOpenJ9)
}

// sharedRuntimeSupported is the runtime policy of a shared launch.
func sharedRuntimeSupported(spec PooledLaunchSpec, isolated func(ResolvedJDK) bool) bool {
	if spec.LoadRunnerThreads {
		return loadRunnerThreadsRuntimeSupported(spec.JDK)
	}
	return isolated(spec.JDK)
}

// SplitThreadModeClasspath keeps per slot the jars that hold the target class
// or refer to the lr API, since each slot has its own lr; every other jar is a
// library loaded once. With dedicatedLoader, LoadRunner's per-VUser class
// loader, every jar stays per slot. Every entry must be a jar the scan can
// read completely.
func SplitThreadModeClasspath(ctx context.Context, entries []string, target string, dedicatedLoader bool) (private, shared []string, reason string) {
	if len(entries) == 0 || !ValidPooledTargetClass(target) {
		return nil, nil, SharedJVMReasonClasspath
	}
	targetEntry := strings.ReplaceAll(target, ".", "/") + ".class"
	budget := int64(threadScanBytes)
	for _, path := range entries {
		if ctx.Err() != nil {
			return nil, nil, SharedJVMReasonIncomplete
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil, SharedJVMReasonClasspath
		}
		perSlot, scanned := dedicatedLoader, true
		if !perSlot {
			perSlot, scanned = jarIsScriptCode(ctx, path, targetEntry, &budget)
		}
		switch {
		case !scanned:
			return nil, nil, SharedJVMReasonIncomplete
		case perSlot:
			private = append(private, path)
		default:
			shared = append(shared, path)
		}
	}
	if len(private) == 0 {
		return nil, nil, SharedJVMReasonClasspath
	}
	return private, shared, ""
}

// jarIsScriptCode reports whether a jar holds the target class or any class
// whose constant pool names the lr API. ok is false when the jar cannot be
// read within the bounds.
func jarIsScriptCode(ctx context.Context, path, targetEntry string, budget *int64) (script, ok bool) {
	if zipbounds.ValidateFile(path, threadLibraryJarLimits) != nil {
		return false, false
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return false, false
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if entry.Name == targetEntry {
			return true, true
		}
	}
	lrAPI := []byte("lrapi/")
	for _, entry := range archive.File {
		if ctx.Err() != nil {
			return false, false
		}
		if !strings.HasSuffix(entry.Name, ".class") || entry.FileInfo().IsDir() {
			continue
		}
		if entry.UncompressedSize64 > threadScanEntryBytes || int64(entry.UncompressedSize64) > *budget {
			return false, false
		}
		reader, err := entry.Open()
		if err != nil {
			return false, false
		}
		data, err := io.ReadAll(io.LimitReader(reader, threadScanEntryBytes+1))
		_ = reader.Close()
		if err != nil || len(data) > threadScanEntryBytes {
			return false, false
		}
		*budget -= int64(len(data))
		if *budget < 0 {
			return false, false
		}
		if bytes.Contains(data, lrAPI) {
			return true, true
		}
	}
	return false, true
}

// validateThreadLibraryJar is validateQualificationJar for a library jar in
// thread mode: larger bounds, and a manifest Class-Path is accepted when none
// of the files it names exists, so the hashed jars are still all that loads.
func validateThreadLibraryJar(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := zipbounds.ValidateFile(path, threadLibraryJarLimits); err != nil {
		return qualificationError(ctx)
	}
	archive, err := zip.OpenReader(path)
	if archive != nil {
		defer archive.Close()
	}
	if err != nil {
		return qualificationError(ctx)
	}
	manifests := 0
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.EqualFold(entry.Name, "META-INF/INDEX.LIST") {
			return ErrPooledQualification
		}
		if !strings.EqualFold(entry.Name, "META-INF/MANIFEST.MF") {
			continue
		}
		manifests++
		if manifests > 1 || entry.UncompressedSize64 > 64<<10 {
			return ErrPooledQualification
		}
		reader, err := entry.Open()
		if err != nil {
			return ErrPooledQualification
		}
		body, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
		closeErr := reader.Close()
		if err != nil || closeErr != nil || len(body) > 64<<10 {
			return ErrPooledQualification
		}
		for _, reference := range manifestClassPath(string(body)) {
			if !classPathReferenceAbsent(path, reference) {
				return ErrPooledQualification
			}
		}
	}
	return ctx.Err()
}

// manifestClassPath returns the Class-Path references of every manifest
// section. Continuation lines start with one space; CR and LF both end lines,
// as in Java's manifest reader.
func manifestClassPath(manifest string) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(strings.ReplaceAll(manifest, "\r\n", "\n"), "\r", "\n"), "\n") {
		if strings.HasPrefix(line, " ") && len(lines) > 0 {
			lines[len(lines)-1] += line[1:]
			continue
		}
		lines = append(lines, line)
	}
	var references []string
	for _, line := range lines {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Class-Path") {
			references = append(references, strings.Fields(value)...)
		}
	}
	return references
}

// classPathReferenceAbsent reports whether a manifest Class-Path reference,
// a URL relative to the jar, names nothing that exists. A reference with a
// scheme or that cannot be decoded is treated as present.
func classPathReferenceAbsent(jar, reference string) bool {
	parsed, err := url.Parse(reference)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Path == "" {
		return false
	}
	target := parsed.Path
	if !filepath.IsAbs(filepath.FromSlash(target)) {
		target = filepath.Join(filepath.Dir(jar), filepath.FromSlash(target))
	}
	_, err = os.Lstat(target)
	return os.IsNotExist(err)
}
