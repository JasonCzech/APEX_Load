package javarunner

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
)

const (
	// ShallowObjectMethodV1 identifies JVM Instrumentation.getObjectSize shallow measurement.
	ShallowObjectMethodV1 = "jvm_instrumentation_shallow.v1"
	// ReachableGraphMethodV1 identifies bounded IdentityHashMap graph traversal measurement.
	ReachableGraphMethodV1 = "jvm_bounded_graph.v1"
	// FieldLayoutFallbackMethodV1 identifies reflective field-layout fallback estimation when -javaagent is unavailable.
	FieldLayoutFallbackMethodV1 = "jvm_field_layout_fallback.v1"
	// ObjectFootprintRetainedNote states that neither shallow nor reachable-graph bytes is retained heap size.
	ObjectFootprintRetainedNote = "neither java_shallow_object_bytes nor java_reachable_graph_bytes is retained heap size"

	DefaultFootprintMaxNodes    = 4096
	MaxFootprintMaxNodes        = 65536
	DefaultFootprintMaxDepth    = 32
	MaxFootprintMaxDepth        = 256
	DefaultFootprintTimeout     = 15 * time.Second
	MaxFootprintTimeout         = 60 * time.Second
	MaxFootprintIPCPayloadBytes = 64 * 1024
	DefaultFootprintHeap        = "64m"

	footprintMainClass  = "io.apexload.javarunner.footprint.FootprintMain"
	footprintAgentClass = "io.apexload.javarunner.footprint.FootprintAgent"
)

// ShallowObjectFootprint reports the shallow size of the root Java object participating in an approved codec/fixture execution.
type ShallowObjectFootprint struct {
	Metric                    string `json:"metric"`
	Method                    string `json:"method"`
	JVMVersion                string `json:"jvm_version,omitempty"`
	Available                 bool   `json:"available"`
	UnavailableReason         string `json:"unavailable_reason,omitempty"`
	Bytes                     *int64 `json:"bytes,omitempty"`
	Unit                      string `json:"unit"`
	FieldLayoutFallbackBytes  *int64 `json:"field_layout_fallback_bytes,omitempty"`
	FieldLayoutFallbackMethod string `json:"field_layout_fallback_method,omitempty"`
	RetainedHeapNote          string `json:"retained_heap_note"`
}

// ReachableGraphFootprint reports the bounded reachable object graph size deduplicated with IdentityHashMap.
type ReachableGraphFootprint struct {
	Metric                    string `json:"metric"`
	Method                    string `json:"method"`
	JVMVersion                string `json:"jvm_version,omitempty"`
	Available                 bool   `json:"available"`
	UnavailableReason         string `json:"unavailable_reason,omitempty"`
	Bytes                     *int64 `json:"bytes,omitempty"`
	Unit                      string `json:"unit"`
	UniqueObjects             int64  `json:"unique_objects"`
	MaxNodes                  int    `json:"max_nodes"`
	MaxDepth                  int    `json:"max_depth"`
	TraversalExhausted        bool   `json:"traversal_exhausted"`
	FieldLayoutFallbackBytes  *int64 `json:"field_layout_fallback_bytes,omitempty"`
	FieldLayoutFallbackMethod string `json:"field_layout_fallback_method,omitempty"`
	RetainedHeapNote          string `json:"retained_heap_note"`
}

// ObjectFootprintResult holds the contained JVM object-footprint and optional codec timing measurements.
type ObjectFootprintResult struct {
	FixtureKind           string                  `json:"fixture_kind"`
	JVMVersion            string                  `json:"jvm_version,omitempty"`
	ShallowObject         ShallowObjectFootprint  `json:"java_shallow_object_bytes"`
	ReachableGraph        ReachableGraphFootprint `json:"java_reachable_graph_bytes"`
	CodecExecuted         bool                    `json:"codec_executed"`
	SerializedBytes       *int64                  `json:"serialized_bytes,omitempty"`
	JavaSerializationMs   *float64                `json:"java_serialization_ms,omitempty"`
	JavaDeserializationMs *float64                `json:"java_deserialization_ms,omitempty"`
}

// FootprintRequest configures an opt-in contained JVM object-footprint measurement.
type FootprintRequest struct {
	JavaHome           string        `json:"java_home,omitempty"`
	FixtureKind        string        `json:"fixture_kind"`
	ArrayLength        int           `json:"array_length,omitempty"`
	ChainDepth         int           `json:"chain_depth,omitempty"`
	MaxNodes           int           `json:"max_nodes,omitempty"`
	MaxDepth           int           `json:"max_depth,omitempty"`
	RunCodec           bool          `json:"run_codec,omitempty"`
	DisableJavaAgent   bool          `json:"disable_java_agent,omitempty"`
	MaxHeap            string        `json:"max_heap,omitempty"`
	Timeout            time.Duration `json:"timeout,omitempty"`
	UntrustedWireBytes []byte        `json:"-"`
}

type footprintIPCRequest struct {
	FixtureKind string `json:"fixture_kind"`
	ArrayLength int    `json:"array_length"`
	ChainDepth  int    `json:"chain_depth"`
	MaxNodes    int    `json:"max_nodes"`
	MaxDepth    int    `json:"max_depth"`
	RunCodec    bool   `json:"run_codec"`
}

type footprintHelperWireResponse struct {
	Status                string   `json:"status"`
	Error                 string   `json:"error,omitempty"`
	FixtureKind           string   `json:"fixture_kind"`
	JVMVersion            string   `json:"jvm_version"`
	AgentAvailable        bool     `json:"agent_available"`
	ShallowBytes          *int64   `json:"shallow_bytes,omitempty"`
	ShallowFallbackBytes  *int64   `json:"shallow_fallback_bytes,omitempty"`
	GraphBytes            *int64   `json:"graph_bytes,omitempty"`
	GraphFallbackBytes    *int64   `json:"graph_fallback_bytes,omitempty"`
	UniqueObjects         int64    `json:"unique_objects"`
	MaxNodes              int      `json:"max_nodes"`
	MaxDepth              int      `json:"max_depth"`
	TraversalExhausted    bool     `json:"traversal_exhausted"`
	CodecExecuted         bool     `json:"codec_executed"`
	SerializedBytes       *int64   `json:"serialized_bytes,omitempty"`
	JavaSerializationMs   *float64 `json:"java_serialization_ms,omitempty"`
	JavaDeserializationMs *float64 `json:"java_deserialization_ms,omitempty"`
}

var (
	footprintCacheMu sync.Mutex
	footprintCache   = map[string]cachedFootprintJar{}
)

type cachedFootprintJar struct {
	dir    string
	jar    string
	sha256 string
}

// ApprovedFootprintFixtures returns the allowlisted fixture/codec shapes accepted by MeasureApprovedObjectFootprint.
func ApprovedFootprintFixtures() []string {
	return []string{
		"primitives",
		"arrays",
		"shared_diamond",
		"cyclic_ring",
		"deep_chain",
		"approved_serializable_record",
		"codec_exception",
		"sleep_timeout",
	}
}

func isApprovedFootprintFixture(kind string) bool {
	switch kind {
	case "primitives", "arrays", "shared_diamond", "cyclic_ring", "deep_chain", "approved_serializable_record", "codec_exception", "sleep_timeout":
		return true
	default:
		return false
	}
}

// NormalizeFootprintRequest validates and applies deterministic bounds to a FootprintRequest.
func NormalizeFootprintRequest(req FootprintRequest) (FootprintRequest, error) {
	if len(req.UntrustedWireBytes) > 0 {
		return FootprintRequest{}, errors.New("untrusted wire bytes are forbidden in contained JVM object-footprint helper")
	}
	req.FixtureKind = strings.TrimSpace(req.FixtureKind)
	if !isApprovedFootprintFixture(req.FixtureKind) {
		return FootprintRequest{}, fmt.Errorf("unapproved footprint fixture kind %q", req.FixtureKind)
	}
	if req.MaxNodes == 0 {
		req.MaxNodes = DefaultFootprintMaxNodes
	}
	if req.MaxNodes < 1 || req.MaxNodes > MaxFootprintMaxNodes {
		return FootprintRequest{}, fmt.Errorf("max_nodes must be between 1 and %d", MaxFootprintMaxNodes)
	}
	if req.MaxDepth == 0 {
		req.MaxDepth = DefaultFootprintMaxDepth
	}
	if req.MaxDepth < 1 || req.MaxDepth > MaxFootprintMaxDepth {
		return FootprintRequest{}, fmt.Errorf("max_depth must be between 1 and %d", MaxFootprintMaxDepth)
	}
	if req.ArrayLength < 0 || req.ArrayLength > 1048576 {
		return FootprintRequest{}, errors.New("array_length must be between 0 and 1048576")
	}
	if req.ArrayLength == 0 && (req.FixtureKind == "arrays" || req.FixtureKind == "approved_serializable_record") {
		req.ArrayLength = 256
	}
	if req.ChainDepth < 0 || req.ChainDepth > 4096 {
		return FootprintRequest{}, errors.New("chain_depth must be between 0 and 4096")
	}
	if req.ChainDepth == 0 && (req.FixtureKind == "cyclic_ring" || req.FixtureKind == "deep_chain") {
		req.ChainDepth = 8
	}
	if req.MaxHeap == "" {
		req.MaxHeap = DefaultFootprintHeap
	}
	if issues := ValidateVMParameters([]string{"-Xms16m", "-Xmx" + req.MaxHeap}); len(issues) > 0 {
		return FootprintRequest{}, issues[0]
	}
	if req.Timeout == 0 {
		req.Timeout = DefaultFootprintTimeout
	}
	if req.Timeout < 0 || req.Timeout > MaxFootprintTimeout {
		return FootprintRequest{}, fmt.Errorf("timeout must be positive and at most %s", MaxFootprintTimeout)
	}
	return req, nil
}

// MeasureApprovedObjectFootprint runs the contained JVM object-footprint helper on an operator-approved fixture/codec spec.
func MeasureApprovedObjectFootprint(ctx context.Context, req FootprintRequest) (*ObjectFootprintResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized, err := NormalizeFootprintRequest(req)
	if err != nil {
		return nil, err
	}
	resolveOpts := ResolveOptions{Selection: JDKSelectionInternal}
	if strings.TrimSpace(normalized.JavaHome) != "" {
		resolveOpts = ResolveOptions{
			Selection:     JDKSelectionSpecified,
			SpecifiedHome: strings.TrimSpace(normalized.JavaHome),
		}
	}
	jdk, err := ResolveJDK(ctx, resolveOpts)
	if err != nil {
		return nil, err
	}
	jarPath, err := ensureFootprintHelperJar(ctx, jdk)
	if err != nil {
		return nil, err
	}
	runDir, err := os.MkdirTemp("", "apexload-footprint-run-*")
	if err != nil {
		return nil, fmt.Errorf("create footprint run dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(runDir) }()
	if err := os.Chmod(runDir, 0o700); err != nil {
		return nil, fmt.Errorf("chmod footprint run dir: %w", err)
	}

	ipcPayload, err := json.Marshal(footprintIPCRequest{
		FixtureKind: normalized.FixtureKind,
		ArrayLength: normalized.ArrayLength,
		ChainDepth:  normalized.ChainDepth,
		MaxNodes:    normalized.MaxNodes,
		MaxDepth:    normalized.MaxDepth,
		RunCodec:    normalized.RunCodec,
	})
	if err != nil {
		return nil, fmt.Errorf("encode footprint request: %w", err)
	}
	if len(ipcPayload) > MaxFootprintIPCPayloadBytes {
		return nil, errors.New("footprint IPC request exceeds 64 KiB limit")
	}

	built, err := BuildCommand(CommandSpec{
		ResolvedJDK: &jdk,
		JavaMajor:   jdk.Major,
		VMFamily:    jdk.VMFamily,
		VMParameters: []string{
			"-Xms16m",
			"-Xmx" + normalized.MaxHeap,
			"-Djava.awt.headless=true",
			"-Djava.io.tmpdir=" + runDir,
		},
		ScriptClasspath: []string{jarPath},
		HarnessMain:     footprintMainClass,
	})
	if err != nil {
		return nil, err
	}

	args := built.Args
	if !normalized.DisableJavaAgent {
		injected := make([]string, 0, len(args)+1)
		inserted := false
		for _, arg := range args {
			if !inserted && arg == "-cp" {
				injected = append(injected, "-javaagent:"+jarPath)
				inserted = true
			}
			injected = append(injected, arg)
		}
		if !inserted {
			return nil, errors.New("failed to position -javaagent argument before -cp")
		}
		args = injected
	}

	runCtx, cancel := context.WithTimeout(ctx, normalized.Timeout)
	defer cancel()

	cmd := exec.Command(jdk.JavaBin, args...)
	cmd.Dir = runDir
	cmd.Env = subprocessguard.Environment(nil)
	ConfigureProcessTree(cmd)

	cmd.Stdin = bytes.NewReader(append(ipcPayload, '\n'))
	var stdout, stderr limitedBuffer
	stdout.limit = MaxFootprintIPCPayloadBytes + 1
	stderr.limit = MaxFootprintIPCPayloadBytes + 1
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := StartProcessTree(cmd); err != nil {
		return nil, fmt.Errorf("start footprint helper: %w", err)
	}
	defer func() { _ = KillProcessTree(cmd) }()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-runCtx.Done():
		_ = KillProcessTree(cmd)
		<-waitCh
		return nil, fmt.Errorf("footprint helper terminated after context/timeout cancellation: %w", runCtx.Err())
	}

	if stdout.exceeded || stderr.exceeded {
		return nil, errors.New("footprint helper IPC output exceeded 64 KiB bound")
	}
	if waitErr != nil {
		errText := strings.TrimSpace(stderr.buf.String())
		if errText == "" {
			errText = strings.TrimSpace(stdout.buf.String())
		}
		if errText != "" {
			return nil, fmt.Errorf("footprint helper failed (%v): %s", waitErr, errText)
		}
		return nil, fmt.Errorf("footprint helper failed: %w", waitErr)
	}

	rawLine := strings.TrimSpace(stdout.buf.String())
	if rawLine == "" {
		return nil, errors.New("footprint helper returned empty IPC output")
	}
	var wire footprintHelperWireResponse
	dec := json.NewDecoder(strings.NewReader(rawLine))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return nil, fmt.Errorf("decode footprint helper output: %w", err)
	}
	if wire.Status != "ok" {
		if wire.Error != "" {
			return nil, fmt.Errorf("footprint helper reported error: %s", wire.Error)
		}
		return nil, fmt.Errorf("footprint helper status %q", wire.Status)
	}
	return translateFootprintWireResponse(normalized, wire)
}

func translateFootprintWireResponse(req FootprintRequest, wire footprintHelperWireResponse) (*ObjectFootprintResult, error) {
	if wire.JVMVersion == "" {
		return nil, errors.New("footprint helper omitted jvm_version")
	}
	if wire.UniqueObjects < 1 || wire.MaxNodes != req.MaxNodes || wire.MaxDepth != req.MaxDepth {
		return nil, errors.New("footprint helper returned inconsistent traversal bounds")
	}
	shallow := ShallowObjectFootprint{
		Metric:           "java_shallow_object_bytes",
		Method:           ShallowObjectMethodV1,
		JVMVersion:       wire.JVMVersion,
		Unit:             "bytes",
		RetainedHeapNote: ObjectFootprintRetainedNote,
	}
	graph := ReachableGraphFootprint{
		Metric:             "java_reachable_graph_bytes",
		Method:             ReachableGraphMethodV1,
		JVMVersion:         wire.JVMVersion,
		Unit:               "bytes",
		UniqueObjects:      wire.UniqueObjects,
		MaxNodes:           wire.MaxNodes,
		MaxDepth:           wire.MaxDepth,
		TraversalExhausted: wire.TraversalExhausted,
		RetainedHeapNote:   ObjectFootprintRetainedNote,
	}
	if wire.AgentAvailable {
		if wire.ShallowBytes == nil || *wire.ShallowBytes <= 0 {
			return nil, errors.New("footprint helper reported agent_available without positive shallow_bytes")
		}
		if wire.GraphBytes == nil || *wire.GraphBytes < *wire.ShallowBytes || wire.UniqueObjects < 1 {
			return nil, errors.New("footprint helper reported invalid reachable graph bytes")
		}
		s := *wire.ShallowBytes
		g := *wire.GraphBytes
		shallow.Available = true
		shallow.Bytes = &s
		graph.Available = true
		graph.Bytes = &g
	} else {
		shallow.Available = false
		shallow.UnavailableReason = "jvm_instrumentation_agent_unavailable"
		if wire.ShallowFallbackBytes != nil && *wire.ShallowFallbackBytes > 0 {
			fb := *wire.ShallowFallbackBytes
			shallow.FieldLayoutFallbackBytes = &fb
			shallow.FieldLayoutFallbackMethod = FieldLayoutFallbackMethodV1
		}
		graph.Available = false
		graph.UnavailableReason = "jvm_instrumentation_agent_unavailable"
		if wire.GraphFallbackBytes != nil && *wire.GraphFallbackBytes > 0 {
			fb := *wire.GraphFallbackBytes
			graph.FieldLayoutFallbackBytes = &fb
			graph.FieldLayoutFallbackMethod = FieldLayoutFallbackMethodV1
		}
	}

	out := &ObjectFootprintResult{
		FixtureKind:    req.FixtureKind,
		JVMVersion:     wire.JVMVersion,
		ShallowObject:  shallow,
		ReachableGraph: graph,
		CodecExecuted:  wire.CodecExecuted,
	}
	if wire.CodecExecuted {
		if wire.SerializedBytes == nil || *wire.SerializedBytes <= 0 {
			return nil, errors.New("footprint helper reported codec_executed without positive serialized_bytes")
		}
		if wire.JavaSerializationMs == nil || *wire.JavaSerializationMs < 0 || math.IsNaN(*wire.JavaSerializationMs) || math.IsInf(*wire.JavaSerializationMs, 0) {
			return nil, errors.New("footprint helper reported invalid java_serialization_ms")
		}
		if wire.JavaDeserializationMs == nil || *wire.JavaDeserializationMs < 0 || math.IsNaN(*wire.JavaDeserializationMs) || math.IsInf(*wire.JavaDeserializationMs, 0) {
			return nil, errors.New("footprint helper reported invalid java_deserialization_ms")
		}
		sb := *wire.SerializedBytes
		ser := *wire.JavaSerializationMs
		deser := *wire.JavaDeserializationMs
		out.SerializedBytes = &sb
		out.JavaSerializationMs = &ser
		out.JavaDeserializationMs = &deser
	} else if wire.JavaSerializationMs != nil || wire.JavaDeserializationMs != nil || wire.SerializedBytes != nil {
		return nil, errors.New("footprint helper reported codec timings when codec was not executed")
	}
	return out, nil
}

type limitedBuffer struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.limit {
		remaining := l.limit - l.buf.Len()
		if remaining > 0 {
			_, _ = l.buf.Write(p[:remaining])
		}
		l.exceeded = true
		return len(p), nil
	}
	return l.buf.Write(p)
}

func ensureFootprintHelperJar(ctx context.Context, jdk ResolvedJDK) (string, error) {
	javacPath, ok := javacFor(jdk)
	if !ok {
		return "", errors.New("javac compiler unavailable in resolved JDK")
	}
	key := jdk.JavaBin + "|" + javacPath + "|" + footprintHelperSourceDigest()
	footprintCacheMu.Lock()
	defer footprintCacheMu.Unlock()

	if cached, ok := footprintCache[key]; ok {
		if sum, err := fileSHA256Hex(cached.jar); err == nil && sum == cached.sha256 {
			return cached.jar, nil
		}
		_ = os.RemoveAll(cached.dir)
		delete(footprintCache, key)
	}

	dir, err := os.MkdirTemp("", "apexload-footprint-helper-*")
	if err != nil {
		return "", fmt.Errorf("create footprint helper build dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("chmod footprint helper build dir: %w", err)
	}
	srcDir := filepath.Join(dir, "src", "io", "apexload", "javarunner", "footprint")
	classDir := filepath.Join(dir, "classes")
	if err := os.MkdirAll(srcDir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := os.MkdirAll(classDir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	agentJava := filepath.Join(srcDir, "FootprintAgent.java")
	mainJava := filepath.Join(srcDir, "FootprintMain.java")
	if err := os.WriteFile(agentJava, []byte(footprintAgentJavaSource), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := os.WriteFile(mainJava, []byte(footprintMainJavaSource), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}

	compileCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(compileCtx, javacPath, "-proc:none", "-encoding", "UTF-8", "-g", "-d", classDir, agentJava, mainJava)
	cmd.Dir = dir
	cmd.Env = subprocessguard.Environment(nil)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("compile footprint helper: %w (%s)", err, strings.TrimSpace(out.String()))
	}

	jarPath := filepath.Join(dir, "footprint-helper.jar")
	if err := writeFootprintAgentJar(classDir, jarPath); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	sum, err := fileSHA256Hex(jarPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	footprintCache[key] = cachedFootprintJar{dir: dir, jar: jarPath, sha256: sum}
	return jarPath, nil
}

func writeFootprintAgentJar(classDir, outputJar string) error {
	var files []string
	if err := filepath.WalkDir(classDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk compiled footprint classes: %w", err)
	}
	if len(files) == 0 {
		return errors.New("javac produced no class files for footprint helper")
	}
	sort.Strings(files)

	out, err := os.OpenFile(outputJar, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create footprint jar: %w", err)
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	manifestHeader := &zip.FileHeader{Name: "META-INF/MANIFEST.MF", Method: zip.Deflate}
	manifestHeader.SetModTime(time.Unix(0, 0).UTC())
	mw, err := zw.CreateHeader(manifestHeader)
	if err != nil {
		_ = zw.Close()
		return err
	}
	manifest := "Manifest-Version: 1.0\r\n" +
		"Premain-Class: " + footprintAgentClass + "\r\n" +
		"Main-Class: " + footprintMainClass + "\r\n\r\n"
	if _, err := io.WriteString(mw, manifest); err != nil {
		_ = zw.Close()
		return err
	}
	for _, file := range files {
		rel, err := filepath.Rel(classDir, file)
		if err != nil {
			_ = zw.Close()
			return err
		}
		header := &zip.FileHeader{Name: filepath.ToSlash(rel), Method: zip.Deflate}
		header.SetModTime(time.Unix(0, 0).UTC())
		fw, err := zw.CreateHeader(header)
		if err != nil {
			_ = zw.Close()
			return err
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			_ = zw.Close()
			return err
		}
		if _, err := fw.Write(raw); err != nil {
			_ = zw.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return out.Close()
}

func fileSHA256Hex(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func footprintHelperSourceDigest() string {
	sum := sha256.Sum256([]byte(footprintAgentJavaSource + "\n---\n" + footprintMainJavaSource))
	return hex.EncodeToString(sum[:])
}

const footprintAgentJavaSource = `package io.apexload.javarunner.footprint;

import java.lang.instrument.Instrumentation;

public final class FootprintAgent {
    private static volatile Instrumentation instrumentation;

    private FootprintAgent() {}

    public static void premain(String agentArgs, Instrumentation inst) {
        instrumentation = inst;
    }

    public static Instrumentation instrumentation() {
        return instrumentation;
    }
}
`

const footprintMainJavaSource = `package io.apexload.javarunner.footprint;

import java.io.BufferedReader;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStreamReader;
import java.io.ObjectInputFilter;
import java.io.ObjectInputStream;
import java.io.ObjectOutputStream;
import java.io.Serializable;
import java.lang.instrument.Instrumentation;
import java.lang.reflect.Array;
import java.lang.reflect.Field;
import java.lang.reflect.Modifier;
import java.nio.charset.StandardCharsets;
import java.util.ArrayDeque;
import java.util.IdentityHashMap;

public final class FootprintMain {
    static final class PrimitiveBoxFixture implements Serializable {
        private static final long serialVersionUID = 1L;
        long count = 42L;
        int code = 7;
        double ratio = 1.25;
        boolean active = true;
        Long boxedLong = Long.valueOf(9001L);
        Integer boxedInt = Integer.valueOf(31415);
    }

    static final class ArrayFixture implements Serializable {
        private static final long serialVersionUID = 1L;
        final byte[] payload;
        final int[] offsets;

        ArrayFixture(int length) {
            this.payload = new byte[length];
            for (int i = 0; i < length; i++) {
                this.payload[i] = (byte) (i & 0x7f);
            }
            this.offsets = new int[]{0, length / 2, length};
        }
    }

    static final class SharedLeaf implements Serializable {
        private static final long serialVersionUID = 1L;
        final byte[] sharedPayload = new byte[128];
        long tag = 99L;
    }

    static final class DiamondBranch implements Serializable {
        private static final long serialVersionUID = 1L;
        final SharedLeaf shared;
        final int branchId;

        DiamondBranch(SharedLeaf shared, int branchId) {
            this.shared = shared;
            this.branchId = branchId;
        }
    }

    static final class DiamondRoot implements Serializable {
        private static final long serialVersionUID = 1L;
        final DiamondBranch left;
        final DiamondBranch right;

        DiamondRoot() {
            SharedLeaf leaf = new SharedLeaf();
            this.left = new DiamondBranch(leaf, 1);
            this.right = new DiamondBranch(leaf, 2);
        }
    }

    static final class CyclicNode implements Serializable {
        private static final long serialVersionUID = 1L;
        final int index;
        CyclicNode next;

        CyclicNode(int index) {
            this.index = index;
        }

        static CyclicNode buildRing(int count) {
            CyclicNode first = new CyclicNode(0);
            CyclicNode prev = first;
            for (int i = 1; i < count; i++) {
                CyclicNode next = new CyclicNode(i);
                prev.next = next;
                prev = next;
            }
            prev.next = first;
            return first;
        }
    }

    static final class ChainNode implements Serializable {
        private static final long serialVersionUID = 1L;
        final int depth;
        final byte[] block = new byte[32];
        final ChainNode child;

        ChainNode(int depth, ChainNode child) {
            this.depth = depth;
            this.child = child;
        }

        static ChainNode buildChain(int depth) {
            ChainNode cur = null;
            for (int i = depth - 1; i >= 0; i--) {
                cur = new ChainNode(i, cur);
            }
            return cur;
        }
    }

    static final class ApprovedSerializableRecord implements Serializable {
        private static final long serialVersionUID = 1L;
        final byte[] body;
        final long sequence;
        final DiamondRoot diamond;

        ApprovedSerializableRecord(int bodyLength) {
            this.body = new byte[bodyLength];
            for (int i = 0; i < bodyLength; i++) {
                this.body[i] = (byte) ((i * 31) & 0xff);
            }
            this.sequence = 1001L;
            this.diamond = new DiamondRoot();
        }
    }

    static final class BrokenCodecFixture implements Serializable {
        private static final long serialVersionUID = 1L;
        private void writeObject(ObjectOutputStream out) throws IOException {
            throw new IOException("simulated approved codec serialization failure");
        }
    }

    static final class QueueNode {
        final Object obj;
        final int depth;

        QueueNode(Object obj, int depth) {
            this.obj = obj;
            this.depth = depth;
        }
    }

    public static void main(String[] args) {
        try {
            BufferedReader reader = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8));
            String line = reader.readLine();
            if (line == null || line.isEmpty()) {
                writeError("missing IPC request line");
                System.exit(1);
                return;
            }
            String fixtureKind = extractString(line, "fixture_kind");
            int arrayLength = extractInt(line, "array_length", 256);
            int chainDepth = extractInt(line, "chain_depth", 8);
            int maxNodes = extractInt(line, "max_nodes", 4096);
            int maxDepth = extractInt(line, "max_depth", 32);
            boolean runCodec = extractBool(line, "run_codec");

            if ("sleep_timeout".equals(fixtureKind)) {
                Thread.sleep(30_000L);
            }

            Object root = buildFixture(fixtureKind, arrayLength, chainDepth);
            Long serializedBytes = null;
            Double serMs = null;
            Double deserMs = null;
            if (runCodec || "codec_exception".equals(fixtureKind)) {
                long serStart = System.nanoTime();
                ByteArrayOutputStream baos = new ByteArrayOutputStream(512);
                try (ObjectOutputStream oos = new ObjectOutputStream(baos)) {
                    oos.writeObject(root);
                    oos.flush();
                }
                long serElapsedNs = System.nanoTime() - serStart;
                byte[] wire = baos.toByteArray();
                if (wire.length > 1048576) {
                    throw new IOException("serialized fixture exceeded 1 MiB limit");
                }
                serializedBytes = Long.valueOf(wire.length);
                serMs = Double.valueOf(serElapsedNs / 1_000_000.0);

                long deserStart = System.nanoTime();
                try (ObjectInputStream ois = new ObjectInputStream(new ByteArrayInputStream(wire))) {
                    ois.setObjectInputFilter(createAllowlistFilter());
                    root = ois.readObject();
                }
                long deserElapsedNs = System.nanoTime() - deserStart;
                deserMs = Double.valueOf(deserElapsedNs / 1_000_000.0);
            }

            Instrumentation inst = FootprintAgent.instrumentation();
            boolean agentAvailable = inst != null;
            long shallowFallback = estimateShallowSize(root);
            Long shallowBytes = agentAvailable ? Long.valueOf(inst.getObjectSize(root)) : null;

            IdentityHashMap<Object, Boolean> visited = new IdentityHashMap<>();
            ArrayDeque<QueueNode> queue = new ArrayDeque<>();
            visited.put(root, Boolean.TRUE);
            queue.add(new QueueNode(root, 0));

            long graphBytesSum = 0L;
            long graphFallbackSum = 0L;
            boolean exhausted = false;

            while (!queue.isEmpty()) {
                QueueNode current = queue.removeFirst();
                Object node = current.obj;
                long nodeFallback = estimateShallowSize(node);
                graphFallbackSum += nodeFallback;
                if (agentAvailable) {
                    graphBytesSum += inst.getObjectSize(node);
                }

                for (Object child : childReferences(node)) {
                    if (child == null || visited.containsKey(child)) {
                        continue;
                    }
                    if (current.depth >= maxDepth) {
                        exhausted = true;
                        continue;
                    }
                    if (visited.size() >= maxNodes) {
                        exhausted = true;
                        continue;
                    }
                    visited.put(child, Boolean.TRUE);
                    queue.addLast(new QueueNode(child, current.depth + 1));
                }
            }

            StringBuilder sb = new StringBuilder(512);
            sb.append("{\"status\":\"ok\"");
            sb.append(",\"fixture_kind\":\"").append(escapeJson(fixtureKind)).append("\"");
            sb.append(",\"jvm_version\":\"").append(escapeJson(System.getProperty("java.version", "unknown"))).append("\"");
            sb.append(",\"agent_available\":").append(agentAvailable);
            if (shallowBytes != null) {
                sb.append(",\"shallow_bytes\":").append(shallowBytes.longValue());
            } else {
                sb.append(",\"shallow_fallback_bytes\":").append(shallowFallback);
            }
            if (agentAvailable) {
                sb.append(",\"graph_bytes\":").append(graphBytesSum);
            } else {
                sb.append(",\"graph_fallback_bytes\":").append(graphFallbackSum);
            }
            sb.append(",\"unique_objects\":").append(visited.size());
            sb.append(",\"max_nodes\":").append(maxNodes);
            sb.append(",\"max_depth\":").append(maxDepth);
            sb.append(",\"traversal_exhausted\":").append(exhausted);
            boolean codecDone = serializedBytes != null;
            sb.append(",\"codec_executed\":").append(codecDone);
            if (codecDone) {
                sb.append(",\"serialized_bytes\":").append(serializedBytes.longValue());
                sb.append(",\"java_serialization_ms\":").append(serMs.doubleValue());
                sb.append(",\"java_deserialization_ms\":").append(deserMs.doubleValue());
            }
            sb.append("}");
            System.out.println(sb.toString());
        } catch (Throwable t) {
            writeError(t.getClass().getSimpleName() + ": " + String.valueOf(t.getMessage()));
            System.exit(2);
        }
    }

    private static Object buildFixture(String kind, int arrayLength, int chainDepth) {
        switch (kind) {
            case "primitives":
                return new PrimitiveBoxFixture();
            case "arrays":
                return new ArrayFixture(arrayLength);
            case "shared_diamond":
                return new DiamondRoot();
            case "cyclic_ring":
                return CyclicNode.buildRing(Math.max(2, chainDepth));
            case "deep_chain":
                return ChainNode.buildChain(Math.max(1, chainDepth));
            case "approved_serializable_record":
                return new ApprovedSerializableRecord(arrayLength);
            case "codec_exception":
                return new BrokenCodecFixture();
            default:
                throw new IllegalArgumentException("unsupported fixture_kind: " + kind);
        }
    }

    private static ObjectInputFilter createAllowlistFilter() {
        return info -> {
            if (info.depth() > 256 || info.references() > 65536 || info.streamBytes() > 1048576 || info.arrayLength() > 1048576) {
                return ObjectInputFilter.Status.REJECTED;
            }
            Class<?> serialClass = info.serialClass();
            if (serialClass == null) {
                return ObjectInputFilter.Status.UNDECIDED;
            }
            while (serialClass.isArray()) {
                serialClass = serialClass.getComponentType();
            }
            if (serialClass.isPrimitive()) {
                return ObjectInputFilter.Status.ALLOWED;
            }
            String name = serialClass.getName();
            if (name.startsWith("io.apexload.javarunner.footprint.") ||
                name.equals("java.lang.Number") ||
                name.equals("java.lang.Long") ||
                name.equals("java.lang.Integer") ||
                name.equals("java.lang.Double") ||
                name.equals("java.lang.Boolean") ||
                name.equals("java.lang.String")) {
                return ObjectInputFilter.Status.ALLOWED;
            }
            return ObjectInputFilter.Status.REJECTED;
        };
    }

    private static Object[] childReferences(Object node) {
        Class<?> cls = node.getClass();
        if (cls.isArray()) {
            if (cls.getComponentType().isPrimitive()) {
                return new Object[0];
            }
            int len = Array.getLength(node);
            Object[] out = new Object[len];
            for (int i = 0; i < len; i++) {
                out[i] = Array.get(node, i);
            }
            return out;
        }
        String pkg = cls.getPackageName();
        if (pkg.startsWith("java.") || pkg.startsWith("jdk.") || pkg.startsWith("sun.")) {
            return new Object[0];
        }
        java.util.ArrayList<Object> refs = new java.util.ArrayList<>();
        for (Class<?> cur = cls; cur != null && cur != Object.class; cur = cur.getSuperclass()) {
            String curPkg = cur.getPackageName();
            if (curPkg.startsWith("java.") || curPkg.startsWith("jdk.") || curPkg.startsWith("sun.")) {
                break;
            }
            for (Field f : cur.getDeclaredFields()) {
                if (Modifier.isStatic(f.getModifiers()) || f.getType().isPrimitive()) {
                    continue;
                }
                try {
                    f.setAccessible(true);
                    Object val = f.get(node);
                    if (val != null) {
                        refs.add(val);
                    }
                } catch (Throwable ignored) {
                }
            }
        }
        return refs.toArray();
    }

    private static long estimateShallowSize(Object node) {
        Class<?> cls = node.getClass();
        if (cls.isArray()) {
            int len = Array.getLength(node);
            Class<?> comp = cls.getComponentType();
            int scale = referenceOrPrimitiveSize(comp);
            return align8(16L + (long) len * scale);
        }
        long size = 12L;
        for (Class<?> cur = cls; cur != null && cur != Object.class; cur = cur.getSuperclass()) {
            for (Field f : cur.getDeclaredFields()) {
                if (Modifier.isStatic(f.getModifiers())) {
                    continue;
                }
                size += referenceOrPrimitiveSize(f.getType());
            }
        }
        return align8(size);
    }

    private static int referenceOrPrimitiveSize(Class<?> type) {
        if (!type.isPrimitive()) {
            return 4;
        }
        if (type == long.class || type == double.class) {
            return 8;
        }
        if (type == int.class || type == float.class) {
            return 4;
        }
        if (type == short.class || type == char.class) {
            return 2;
        }
        return 1;
    }

    private static long align8(long bytes) {
        return (bytes + 7L) & ~7L;
    }

    private static String extractString(String json, String key) {
        String needle = "\"" + key + "\":\"";
        int idx = json.indexOf(needle);
        if (idx < 0) {
            return "";
        }
        int start = idx + needle.length();
        int end = json.indexOf('"', start);
        return end > start ? json.substring(start, end) : "";
    }

    private static int extractInt(String json, String key, int fallback) {
        String needle = "\"" + key + "\":";
        int idx = json.indexOf(needle);
        if (idx < 0) {
            return fallback;
        }
        int start = idx + needle.length();
        int end = start;
        while (end < json.length() && (Character.isDigit(json.charAt(end)) || json.charAt(end) == '-')) {
            end++;
        }
        try {
            return Integer.parseInt(json.substring(start, end));
        } catch (NumberFormatException e) {
            return fallback;
        }
    }

    private static boolean extractBool(String json, String key) {
        String needle = "\"" + key + "\":";
        int idx = json.indexOf(needle);
        if (idx < 0) {
            return false;
        }
        int start = idx + needle.length();
        return json.regionMatches(start, "true", 0, 4);
    }

    private static void writeError(String message) {
        System.out.println("{\"status\":\"error\",\"error\":\"" + escapeJson(message) + "\"}");
    }

    private static String escapeJson(String s) {
        if (s == null) {
            return "";
        }
        return s.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", " ").replace("\r", " ");
    }
}
`
