package javarunner

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
)

// Compiling Java Vuser source.
//
// VuGen compiles a Java Vuser's .java files with the configured JDK before it
// replays them, so a LoadRunner script arrives as source. CompileJavaSources
// does the same on the runner: it writes the sources to a private directory,
// runs javac with annotation processing off (the one way javac runs code from
// its classpath), and packs the class files into a deterministic jar. Nothing
// it compiles is executed here.

const (
	// MaxCompileSources bounds the files of one compile.
	MaxCompileSources = 512
	// MaxCompileSourceBytes bounds all sources of one compile together.
	MaxCompileSourceBytes = 16 << 20
	// MaxCompiledJarBytes bounds the jar a compile may produce.
	MaxCompiledJarBytes = 16 << 20
	// MaxCompileDiagnostics bounds the diagnostics a compile reports.
	MaxCompileDiagnostics = 200
	// DefaultCompileTimeout is the compile deadline when the caller sets none.
	DefaultCompileTimeout = 2 * time.Minute

	maxCompileOutputBytes     = 1 << 20
	maxCompileDiagnosticBytes = 1000
	compileDirPrefix          = ".apexload-compile-"
)

// Stable, value-free compile failure categories.
const (
	CompileCompilerUnavailable = "java_compiler_unavailable"
	CompileSourcesInvalid      = "compile_sources_invalid"
	CompileFailed              = "compile_failed"
	CompileTargetMissing       = "compile_target_missing"
	CompileOutputTooLarge      = "compile_output_too_large"
	CompileTimedOut            = "compile_timeout"
	CompileInternal            = "compile_internal_error"
)

// CompileSource is one .java file at its relative, slash-separated path.
type CompileSource struct {
	Path    string
	Content []byte
}

// CompileSpec describes one compile.
type CompileSpec struct {
	// JDK is the runtime the classes will run on. Its own javac compiles them
	// when it has one, exactly as VuGen does; otherwise Compiler must be a newer
	// or equal JDK, whose javac then targets JDK.Major with --release.
	JDK      ResolvedJDK
	Compiler *ResolvedJDK
	Sources  []CompileSource
	// Classpath holds resolved, existing jars or class directories.
	Classpath []string
	// APIJar provides the lrapi shim the sources compile against.
	APIJar string
	// TargetClass is the binary name the compile must produce.
	TargetClass string
	// WorkDir holds the private compile directory, removed afterwards.
	WorkDir string
	Timeout time.Duration
}

// CompileDiagnostic is one javac message. File is the source's relative path.
type CompileDiagnostic struct {
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// CompileResult is a successful compile.
type CompileResult struct {
	Jar             []byte
	SHA256          string
	Classes         []string
	Diagnostics     []CompileDiagnostic
	CompilerVersion string
	Release         int
}

// CompileError is a failed compile: a stable category plus javac's messages.
// A containment failure is returned as ErrProcessContainmentFailed instead,
// so callers close their process gate exactly as for a failed JDK probe.
type CompileError struct {
	Category    string
	Diagnostics []CompileDiagnostic
}

func (e *CompileError) Error() string { return "Java source compile failed (" + e.Category + ")" }

func compileError(category string, diagnostics ...CompileDiagnostic) error {
	return &CompileError{Category: category, Diagnostics: diagnostics}
}

var compileSourcePath = regexp.MustCompile(`^[A-Za-z0-9_$][A-Za-z0-9_$. -]*(/[A-Za-z0-9_$][A-Za-z0-9_$. -]*)*\.java$`)

// validCompileSources checks paths are distinct, relative and safe, and that
// the files fit the bounds.
func validCompileSources(sources []CompileSource) bool {
	if len(sources) == 0 || len(sources) > MaxCompileSources {
		return false
	}
	seen := make(map[string]bool, len(sources))
	total := 0
	for _, source := range sources {
		key := strings.ToLower(source.Path)
		if !compileSourcePath.MatchString(source.Path) || len(source.Path) > 1024 || seen[key] || strings.Contains(source.Path, "..") {
			return false
		}
		seen[key] = true
		total += len(source.Content)
		if total > MaxCompileSourceBytes {
			return false
		}
	}
	return true
}

// javacFor finds the javac beside a JDK's java. Java 8 reports java.home as
// the jre directory inside the JDK, so its parent's bin is tried too.
func javacFor(jdk ResolvedJDK) (string, bool) {
	name := "javac"
	if runtime.GOOS == "windows" {
		name = "javac.exe"
	}
	candidates := []string{filepath.Join(jdk.Home, "bin", name)}
	if strings.EqualFold(filepath.Base(jdk.Home), "jre") {
		candidates = append(candidates, filepath.Join(filepath.Dir(jdk.Home), "bin", name))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, true
		}
	}
	return "", false
}

// JavaCompilerAvailable reports whether a JDK carries its own javac.
func JavaCompilerAvailable(jdk ResolvedJDK) bool {
	_, ok := javacFor(jdk)
	return ok
}

// ResolveCompileClasspath resolves each entry on its own, so a compile still
// runs when some dependency is missing; unavailable lists those entries'
// indexes. An invalid entry fails the whole classpath, as at run time.
func ResolveCompileClasspath(entries []string, workDir string) (resolved []string, unavailable []int, issue *ClasspathValidationIssue) {
	if issues := ValidateClasspath(entries); len(issues) > 0 {
		return nil, nil, &issues[0]
	}
	for index, entry := range entries {
		one, entryIssue := ResolveClasspath([]string{entry}, workDir)
		if entryIssue != nil {
			unavailable = append(unavailable, index)
			continue
		}
		resolved = append(resolved, one[0])
	}
	return resolved, unavailable, nil
}

// compilerFor picks the javac and its release flags: the runtime's own javac
// with none, or the operator's compiler JDK with --release for the runtime.
func compilerFor(spec CompileSpec) (javac string, compiler ResolvedJDK, flags []string, ok bool) {
	if javac, found := javacFor(spec.JDK); found {
		return javac, spec.JDK, nil, true
	}
	if spec.Compiler == nil || spec.Compiler.Major < spec.JDK.Major {
		return "", ResolvedJDK{}, nil, false
	}
	javac, found := javacFor(*spec.Compiler)
	if !found {
		return "", ResolvedJDK{}, nil, false
	}
	if spec.Compiler.Major == spec.JDK.Major {
		return javac, *spec.Compiler, nil, true
	}
	if spec.Compiler.Major < 9 {
		return "", ResolvedJDK{}, nil, false
	}
	return javac, *spec.Compiler, []string{"--release", strconv.Itoa(spec.JDK.Major)}, true
}

// CompileJavaSources compiles spec.Sources against the API jar and classpath
// and returns the class files as a jar. The caller bounds concurrency.
func CompileJavaSources(ctx context.Context, spec CompileSpec) (CompileResult, error) {
	if !validCompileSources(spec.Sources) || !ValidPooledTargetClass(spec.TargetClass) || spec.APIJar == "" || spec.WorkDir == "" || spec.JDK.Major < MinimumLegacyJavaMajor {
		return CompileResult{}, compileError(CompileSourcesInvalid)
	}
	javac, compiler, releaseFlags, ok := compilerFor(spec)
	if !ok {
		return CompileResult{}, compileError(CompileCompilerUnavailable)
	}
	dir, err := makeCompileDir(spec.WorkDir)
	if err != nil {
		return CompileResult{}, compileError(CompileInternal)
	}
	defer os.RemoveAll(dir)
	srcDir, outDir := filepath.Join(dir, "src"), filepath.Join(dir, "classes")
	encoding := "UTF-8"
	for _, source := range spec.Sources {
		if !utf8.Valid(source.Content) {
			// VuGen compiles with the Windows platform encoding.
			encoding = "windows-1252"
			break
		}
	}
	names := make([]string, 0, len(spec.Sources))
	for _, source := range spec.Sources {
		target := filepath.Join(srcDir, filepath.FromSlash(source.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return CompileResult{}, compileError(CompileInternal)
		}
		// javac rejects a byte-order mark as an illegal character.
		if err := os.WriteFile(target, bytes.TrimPrefix(source.Content, []byte("\xef\xbb\xbf")), 0o600); err != nil {
			return CompileResult{}, compileError(CompileInternal)
		}
		names = append(names, source.Path)
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return CompileResult{}, compileError(CompileInternal)
	}
	classpath := append([]string{spec.APIJar}, spec.Classpath...)
	args := []string{"-proc:none", "-implicit:none", "-g", "-nowarn", "-Xlint:-options", "-encoding", encoding, "-Xmaxerrs", strconv.Itoa(MaxCompileDiagnostics), "-d", outDir, "-classpath", strings.Join(classpath, string(filepath.ListSeparator))}
	args = append(append(args, releaseFlags...), names...)
	argFile := filepath.Join(dir, "javac.args")
	if err := os.WriteFile(argFile, []byte(javacArgFile(args)), 0o600); err != nil {
		return CompileResult{}, compileError(CompileInternal)
	}

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = DefaultCompileTimeout
	}
	compileCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output := &boundedBuffer{max: maxCompileOutputBytes}
	// -J options are only honored on the command line. English messages keep
	// the diagnostics parseable whatever the runner's locale.
	cmd := exec.CommandContext(compileCtx, javac, "-J-Xmx768m", "-J-Duser.language=en", "-J-Duser.country=US", "@"+argFile)
	ConfigureProcessTree(cmd)
	cmd.WaitDelay = time.Second
	cmd.Dir = srcDir
	cmd.Env = subprocessguard.Environment(compiler.EnvironmentOverrides(os.Getenv("PATH")))
	cmd.Stdout = output
	cmd.Stderr = output
	runErr := executeJavaProbeCommand(cmd, StartProcessTree, KillProcessTree, ReapFailedStart)
	if errors.Is(runErr, ErrProcessContainmentFailed) {
		return CompileResult{}, runErr
	}
	diagnostics := parseJavacDiagnostics(output.String(), dir, classpath)
	if runErr != nil {
		if compileCtx.Err() != nil && ctx.Err() == nil {
			return CompileResult{}, compileError(CompileTimedOut, diagnostics...)
		}
		if ctx.Err() != nil {
			return CompileResult{}, ctx.Err()
		}
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return CompileResult{}, compileError(CompileInternal, diagnostics...)
		}
		if len(diagnostics) == 0 {
			diagnostics = []CompileDiagnostic{{Kind: "error", Message: "javac failed without a diagnostic"}}
		}
		return CompileResult{}, compileError(CompileFailed, diagnostics...)
	}
	jar, classes, err := jarClassDirectory(outDir)
	if err != nil {
		if errors.Is(err, errCompiledJarTooLarge) {
			return CompileResult{}, compileError(CompileOutputTooLarge)
		}
		return CompileResult{}, compileError(CompileInternal)
	}
	if !slices.Contains(classes, spec.TargetClass) {
		return CompileResult{}, compileError(CompileTargetMissing, diagnostics...)
	}
	sum := sha256.Sum256(jar)
	return CompileResult{Jar: jar, SHA256: hex.EncodeToString(sum[:]), Classes: classes, Diagnostics: diagnostics, CompilerVersion: compiler.Version, Release: spec.JDK.Major}, nil
}

func makeCompileDir(workDir string) (string, error) {
	suffix := make([]byte, 12)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	dir := filepath.Join(workDir, compileDirPrefix+hex.EncodeToString(suffix))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// javacArgFile quotes every argument for javac's @file reader, which in every
// JDK splits on whitespace outside quotes and unescapes backslashes inside.
func javacArgFile(args []string) string {
	var builder strings.Builder
	for _, arg := range args {
		builder.WriteByte('"')
		for _, char := range arg {
			if char == '\\' || char == '"' {
				builder.WriteByte('\\')
			}
			builder.WriteRune(char)
		}
		builder.WriteString("\"\n")
	}
	return builder.String()
}

var (
	javacDiagnosticLine = regexp.MustCompile(`^(.+?\.java):(\d+): (error|warning): (.*)$`)
	// javacDetailLine is a detail javac prints under a message, such as the
	// symbol a "cannot find symbol" error could not find.
	javacDetailLine = regexp.MustCompile(`^\s+(symbol|location|required|found|reason):\s+(.*)$`)
)

// parseJavacDiagnostics keeps javac's file messages and its general errors.
// Runner paths in messages become placeholders: classpath entries may come
// from resolved templates and must not be echoed.
func parseJavacDiagnostics(output, compileDir string, classpath []string) []CompileDiagnostic {
	replacer := []string{compileDir + string(filepath.Separator) + "src" + string(filepath.Separator), "", compileDir, "<compile dir>"}
	for index, entry := range classpath {
		if index == 0 {
			replacer = append(replacer, entry, "<APEX lr API>")
			continue
		}
		replacer = append(replacer, entry, fmt.Sprintf("<classpath entry %d>", index-1))
	}
	redact := strings.NewReplacer(replacer...)
	var diagnostics []CompileDiagnostic
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := redact.Replace(strings.TrimRight(raw, "\r"))
		if detail := javacDetailLine.FindStringSubmatch(line); detail != nil && len(diagnostics) > 0 {
			last := &diagnostics[len(diagnostics)-1]
			last.Message = truncateDiagnostic(last.Message + "; " + detail[1] + ": " + strings.Join(strings.Fields(detail[2]), " "))
			continue
		}
		var diagnostic CompileDiagnostic
		if match := javacDiagnosticLine.FindStringSubmatch(line); match != nil {
			number, _ := strconv.Atoi(match[2])
			diagnostic = CompileDiagnostic{File: filepath.ToSlash(match[1]), Line: number, Kind: match[3], Message: match[4]}
		} else if rest, ok := strings.CutPrefix(line, "error: "); ok {
			diagnostic = CompileDiagnostic{Kind: "error", Message: rest}
		} else if rest, ok := strings.CutPrefix(line, "javac: "); ok {
			diagnostic = CompileDiagnostic{Kind: "error", Message: rest}
		} else {
			continue
		}
		diagnostic.Message = truncateDiagnostic(diagnostic.Message)
		diagnostic.File = strings.ToValidUTF8(diagnostic.File, "\uFFFD")
		diagnostics = append(diagnostics, diagnostic)
		if len(diagnostics) == MaxCompileDiagnostics {
			break
		}
	}
	return diagnostics
}

// truncateDiagnostic bounds a message and makes it valid UTF-8 for JSON.
func truncateDiagnostic(message string) string {
	if len(message) > maxCompileDiagnosticBytes {
		message = strings.ToValidUTF8(message[:maxCompileDiagnosticBytes], "") + "…"
	}
	return strings.ToValidUTF8(message, "\uFFFD")
}

var errCompiledJarTooLarge = errors.New("compiled jar exceeds its limit")

// compiledJarTime is the fixed entry time that keeps a jar's bytes, and so
// its hash, the same for the same classes.
var compiledJarTime = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

// jarClassDirectory packs every class file under dir, sorted, after a fixed
// manifest. It returns the jar and the binary class names.
func jarClassDirectory(dir string) ([]byte, []string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("compile output holds a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, current)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	add := func(name string, data []byte) error {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: compiledJarTime}
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err := entry.Write(data); err != nil {
			return err
		}
		if buffer.Len() > MaxCompiledJarBytes {
			return errCompiledJarTooLarge
		}
		return nil
	}
	if err := add("META-INF/MANIFEST.MF", []byte("Manifest-Version: 1.0\r\nCreated-By: APEX Load Java source compile\r\n\r\n")); err != nil {
		return nil, nil, err
	}
	var classes []string
	total := 0
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return nil, nil, err
		}
		total += len(data)
		if total > MaxCompiledJarBytes {
			return nil, nil, errCompiledJarTooLarge
		}
		if err := add(name, data); err != nil {
			return nil, nil, err
		}
		if strings.HasSuffix(name, ".class") {
			classes = append(classes, strings.ReplaceAll(strings.TrimSuffix(name, ".class"), "/", "."))
		}
	}
	if err := writer.Close(); err != nil {
		return nil, nil, err
	}
	if buffer.Len() > MaxCompiledJarBytes {
		return nil, nil, errCompiledJarTooLarge
	}
	return buffer.Bytes(), classes, nil
}
