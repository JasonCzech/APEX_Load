package javasidecar

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

// DedicatedPodProfile is operator-owned and baked into the immutable execution
// image. The broker binds the exact profile file SHA256, image digest and memory
// reservation. Paths refer only to read-only image assets, never downloaded code.
// Initial pod preparation requires private script loading; ordinary dedicated
// execution continues to support its existing broader configuration surface.
type DedicatedPodProfile struct {
	JDK          javarunner.ResolvedJDK `json:"jdk"`
	JavaSHA256   string                 `json:"java_sha256"`
	Harness      PodAsset               `json:"harness"`
	Classpath    []PodAsset             `json:"classpath"`
	VMParameters []string               `json:"vm_parameters"`
	Command      ActionCommand          `json:"command"`
}
type PodAsset struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

var ErrPodRuntime = errors.New("java_pod_runtime_invalid")

// DedicatedPodRuntime never reports termination proof. Its owner MUST exit PID1
// when Done closes. The broker keeps reservations until separate attestation.
type DedicatedPodRuntime struct {
	profile   DedicatedPodProfile
	workDir   string
	mu        sync.Mutex
	started   bool
	session   *persistentSession
	done      chan struct{}
	once      sync.Once
	rootReady chan struct{}
}

func NewDedicatedPodRuntime(profile DedicatedPodProfile, workDir string) (*DedicatedPodRuntime, error) {
	if err := javarunner.RequirePID1Isolation(); err != nil {
		return nil, err
	}
	if err := ValidateCredentialIsolation(); err != nil {
		return nil, ErrPodRuntime
	}
	r, err := newDedicatedPodRuntime(profile, workDir)
	if err != nil {
		return nil, err
	}
	p := r.profile
	for _, a := range append([]PodAsset{{p.JDK.JavaBin, p.JavaSHA256}, p.Harness}, p.Classpath...) {
		if javarunner.RequireReadOnlyAsset(a.Path) != nil {
			return nil, ErrPodRuntime
		}
	}
	return r, nil
}
func newDedicatedPodRuntime(profile DedicatedPodProfile, workDir string) (*DedicatedPodRuntime, error) {
	// Clone input: no caller mutation of an admitted configuration or parameters.
	data, err := json.Marshal(profile)
	if err != nil || len(data) > 512<<10 {
		return nil, ErrPodRuntime
	}
	var frozen DedicatedPodProfile
	if json.Unmarshal(data, &frozen) != nil {
		return nil, ErrPodRuntime
	}
	p := frozen
	if !filepath.IsAbs(workDir) || !filepath.IsAbs(p.JDK.Home) || !filepath.IsAbs(p.JDK.JavaBin) || filepath.Base(p.JDK.JavaBin) != "java" || p.JDK.Version == "" || p.JDK.Major < 17 || p.JDK.VMFamily != javarunner.JavaVMHotSpot && p.JDK.VMFamily != javarunner.JavaVMOpenJ9 || !javarunner.ValidPooledTargetClass(p.Command.ClassName) || p.Command.Method != "" || len(p.Command.Params) != 0 || len(p.Classpath) < 1 || len(p.Classpath) > 256 || len(p.VMParameters) > 256 || len(javarunner.ValidateVMParametersForExecution(p.VMParameters)) != 0 {
		return nil, ErrPodRuntime
	}
	st, err := os.Stat(workDir)
	if err != nil || !st.IsDir() {
		return nil, ErrPodRuntime
	}
	resolvedWork, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		return nil, ErrPodRuntime
	}
	for _, a := range append([]PodAsset{{p.JDK.JavaBin, p.JavaSHA256}, p.Harness}, p.Classpath...) {
		if !filepath.IsAbs(a.Path) || strings.TrimSpace(a.Path) != a.Path || len(a.SHA256) != 64 {
			return nil, ErrPodRuntime
		}
		raw, e := hex.DecodeString(a.SHA256)
		if e != nil || len(raw) != 32 || strings.ToLower(a.SHA256) != a.SHA256 {
			return nil, ErrPodRuntime
		}
		resolvedAsset, e := filepath.EvalSymlinks(a.Path)
		if e != nil {
			return nil, ErrPodRuntime
		}
		rel, e := filepath.Rel(resolvedWork, resolvedAsset)
		if e != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, ErrPodRuntime
		}
	}
	return &DedicatedPodRuntime{profile: p, workDir: workDir, done: make(chan struct{}), rootReady: make(chan struct{})}, nil
}
func (r *DedicatedPodRuntime) Done() <-chan struct{}      { return r.done }
func (r *DedicatedPodRuntime) Close()                     { r.once.Do(func() { close(r.done) }) }
func (r *DedicatedPodRuntime) RootReady() <-chan struct{} { return r.rootReady }
func (r *DedicatedPodRuntime) RootPID() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session == nil || r.session.cmd.Process == nil {
		return 0
	}
	return r.session.cmd.Process.Pid
}

func verifyPodAsset(ctx context.Context, a PodAsset) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	before, err := os.Stat(a.Path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > 1<<30 {
		return ErrPodRuntime
	}
	f, err := os.Open(a.Path)
	if err != nil {
		return ErrPodRuntime
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return ErrPodRuntime
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, e := f.Read(buf)
		total += int64(n)
		if total > 1<<30 {
			return ErrPodRuntime
		}
		_, _ = h.Write(buf[:n])
		if e == io.EOF {
			break
		}
		if e != nil {
			return ErrPodRuntime
		}
	}
	after, err := f.Stat()
	if err != nil || total != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return ErrPodRuntime
	}
	return nil
}

func (r *DedicatedPodRuntime) Ready(ctx context.Context) (err error) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return ErrPodRuntime
	}
	r.started = true
	r.mu.Unlock()
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	p := r.profile
	for _, a := range append([]PodAsset{{p.JDK.JavaBin, p.JavaSHA256}, p.Harness}, p.Classpath...) {
		if err = verifyPodAsset(ctx, a); err != nil {
			return err
		}
	}
	select {
	case <-r.done:
		return ErrPodRuntime
	default:
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	server := NewServer(ServerOptions{Operator: config.JavaVuserOperatorConfig{MaxProcesses: 1}, supportsStrongProcessContainment: func() bool { return true }, startStrongProcessTree: func(cmd *exec.Cmd) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-r.done:
			return ErrPodRuntime
		default:
		}
		return cmd.Start()
	}})
	// These private hooks are reachable in production only after the PID1 guard.
	// Kill means "exit the pod", never "release the process/memory reservation".
	server.persistentReadyNonce = hex.EncodeToString(nonce[:])
	server.persistentKill = func(*exec.Cmd) error { r.Close(); return javarunner.ErrProcessContainmentFailed }
	cp := make([]string, len(p.Classpath))
	for i, a := range p.Classpath {
		cp[i] = a.Path
	}
	prepared := preparedRuntime{jdk: p.JDK, harnessJar: p.Harness.Path, scriptClasspath: cp, vmParameters: p.VMParameters, dedicatedLoader: true, workDir: r.workDir}
	request := ExecutionRequest{Runtime: RuntimeSettings{DedicatedClassLoader: true}, Command: p.Command}
	request.Command.Method = "init"
	session, failure := server.startPersistentSession(ctx, prepared, request, nil)
	if failure != nil {
		return errors.New(failure.code)
	}
	r.mu.Lock()
	r.session = session
	r.mu.Unlock()
	select {
	case <-session.readyDone:
		close(r.rootReady)
	case <-session.waitDone:
		return ErrPodRuntime
	case <-session.stdoutDone:
		return ErrPodRuntime
	case <-r.done:
		return ErrPodRuntime
	case <-ctx.Done():
		return ctx.Err()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (r *DedicatedPodRuntime) Invoke(ctx context.Context, id, method string, params map[string]string) ([]byte, error) {
	r.mu.Lock()
	session := r.session
	r.mu.Unlock()
	select {
	case <-r.rootReady:
	default:
		return nil, ErrPodRuntime
	}
	select {
	case <-r.done:
		return nil, ErrPodRuntime
	default:
	}
	if session == nil || !executionIDPattern.MatchString(id) || method != "init" && method != "action" && method != "end" {
		return nil, ErrPodRuntime
	}
	command := r.profile.Command
	command.Method = method
	command.Params = map[string]any{"lr_parameters": params}
	started := time.Now()
	outcome := session.invoke(ctx, ExecutionRequest{ExecutionID: id, Runtime: RuntimeSettings{DedicatedClassLoader: true}, Command: command})
	if persistentOutcomePoisonsSession(outcome.errorCode) || outcome.containmentErr != nil {
		r.Close()
		return nil, errors.New("java_pod_transport_failed")
	}
	result := ExecutionResponse{Contract: ContractVersion, ExecutionID: id, Success: outcome.success, ErrorCode: outcome.errorCode, Error: outcome.errorMessage, Stdout: outcome.stdout, Stderr: outcome.stderr, Events: outcome.events, StdoutTruncated: outcome.stdoutTruncated, StderrTruncated: outcome.stderrTruncated, EventsTruncated: outcome.eventsTruncated, DurationMS: time.Since(started).Milliseconds()}
	return json.Marshal(result)
}
