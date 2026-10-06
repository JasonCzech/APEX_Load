package duckdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const workerChildArgument = "--apexload-native-worker-v1"

type workerChildExecutor interface {
	execute(context.Context, *workerAttempt, workerRequest) (workerResult, error)
}

// DispatchNativeWorker must be the first statement in controller/desktop main.
// A requested but malformed/unavailable mode is handled and never falls through
// into dotenv, storage, listeners or Wails. Imported init effects still require
// actual packaged-platform qualification.
func DispatchNativeWorker() (bool, int) {
	return dispatchNativeWorker(os.Args[1:], newWorkerNativeExecutor)
}
func dispatchNativeWorker(args []string, factory func() (workerChildExecutor, error)) (bool, int) {
	requested := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "--apexload-native-worker") {
			requested = true
		}
	}
	if !requested {
		pinWorkerExecutableIdentity()
		return false, 0
	}
	if len(args) != 1 || args[0] != workerChildArgument {
		return true, 78
	}
	bootstrap, root, commands, err := workerInheritedChannels()
	if err != nil {
		return true, 78
	}
	defer bootstrap.Close()
	defer root.Close()
	defer commands.Close()
	executor, err := factory()
	if err != nil || executor == nil {
		return true, 78
	}
	return true, runWorkerChild(bootstrap, root, commands, os.Stdout, executor)
}

type workerExecutableApproval struct {
	once   sync.Once
	digest string
	err    error
}

func (a *workerExecutableApproval) pin(read func() (string, error)) {
	a.once.Do(func() { a.digest, a.err = read() })
}

var workerPinnedExecutable workerExecutableApproval

func pinWorkerExecutableIdentity() {
	workerPinnedExecutable.once.Do(func() { workerPinnedExecutable.digest, workerPinnedExecutable.err = workerExecutableDigest() })
}
func workerExpectedExecutableDigest() (string, error) {
	pinWorkerExecutableIdentity()
	return workerPinnedExecutable.digest, workerPinnedExecutable.err
}
func workerExecutableDigest() (retDigest string, retErr error) {
	path, err := workerRunningExecutableSourcePath()
	if err != nil {
		return "", errWorkerProtocol
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errWorkerProtocol
	}
	defer func() {
		if file.Close() != nil {
			workerExecutableQuarantine.Store(file, file)
			retDigest = ""
			retErr = errWorkerOwnership
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 2<<30 {
		return "", errWorkerProtocol
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, info.Size()+1))
	if err != nil || n != info.Size() {
		return "", errWorkerProtocol
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type workerChildWrite struct {
	mu       sync.Mutex
	out      io.Writer
	identity workerIdentity
}

func (w *workerChildWrite) send(f workerFrame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	f.Identity = w.identity
	return writeWorkerFrame(w.out, f)
}

func runWorkerChild(bootstrap *os.File, directory *os.File, commands *os.File, out io.Writer, executor workerChildExecutor) int {
	b, err := readWorkerBootstrap(bootstrap)
	if err != nil {
		return 78
	}
	// The parent closes bootstrap after one immutable manifest. Extra data is
	// invalid, and EOF is observed before any native executor is entered.
	var extra [1]byte
	if n, err := bootstrap.Read(extra[:]); n != 0 || err != io.EOF {
		return 78
	}
	digest, err := workerExecutableDigest()
	if err != nil || digest != b.Identity.BuildID {
		return 78
	}
	path, err := workerInheritedRootPath(directory)
	if err != nil {
		return 78
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return 78
	}
	defer root.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(b.Limits.RuntimeMS)*time.Millisecond)
	defer cancel()
	a := &workerAttempt{bootstrap: b, ctx: ctx, path: path, root: root, directory: directory}
	if a.verifyInputs(ctx) != nil {
		return 78
	}
	writes := &workerChildWrite{out: out, identity: b.Identity}
	if writes.send(workerFrame{Kind: "hello"}) != nil {
		return 78
	}
	request, err := readWorkerFrame(commands)
	if err != nil || request.Identity != b.Identity || request.Kind != "request" || request.Request.validate(b) != nil {
		return 78
	}
	// One reader and one operation are owned and joined. Parent channel EOF
	// cancels the operation. A stuck native call remains alive for parent kill.
	type commandResult struct {
		cancel *workerCancel
		err    error
	}
	commandDone := make(chan commandResult, 1)
	go func() {
		f, err := readWorkerFrame(commands)
		if err == nil && (f.Identity != b.Identity || f.Kind != "cancel") {
			err = errWorkerProtocol
		}
		commandDone <- commandResult{cancel: f.Cancel, err: err}
	}()
	type operationResult struct {
		result workerResult
		err    error
	}
	operationDone := make(chan operationResult, 1)
	go func() {
		result, err := executor.execute(ctx, a, *request.Request)
		operationDone <- operationResult{result, err}
	}()
	var cancelSequence uint64
	var commandErr error
	var operation operationResult
	commandJoined := false
	for {
		select {
		case command := <-commandDone:
			commandJoined = true
			commandErr = command.err
			if command.err != nil {
				cancel()
			} else {
				cancelSequence = command.cancel.Sequence
				cancel()
				if writes.send(workerFrame{Kind: "cancel_ack", CancelAck: &workerCancelAck{Sequence: cancelSequence}}) != nil {
					return 78
				}
			}
			commandDone = nil
		case operation = <-operationDone:
			goto finished
		case <-ctx.Done():
			// A native executor that ignores ctx cannot claim completion. This
			// process remains until it returns or the parent physically kills it.
			operation = <-operationDone
			goto finished
		}
	}
finished:
	// Close unblocks the sole owned command reader; never leak it on success.
	if commands.Close() != nil {
		return 78
	}
	if !commandJoined {
		command := <-commandDone
		commandErr = command.err
		if command.cancel != nil && command.err == nil {
			cancelSequence = command.cancel.Sequence
			cancel()
			if writes.send(workerFrame{Kind: "cancel_ack", CancelAck: &workerCancelAck{Sequence: cancelSequence}}) != nil {
				return 78
			}
		}
	}
	if cancelSequence != 0 {
		if writes.send(workerFrame{Kind: "result", Result: &workerResult{Status: "cancelled", Code: "cancelled", CancelSequence: cancelSequence}}) != nil {
			return 78
		}
		return 0
	}
	if workerContextError(ctx) != nil || commandErr != nil && !errors.Is(commandErr, os.ErrClosed) {
		return 78
	}
	if operation.err != nil {
		code := "internal"
		if errors.Is(operation.err, ErrUnavailable) {
			code = "unavailable"
		}
		if errors.Is(operation.err, errWorkerResourceLimit) || workerNativeResourceError(operation.err) {
			code = "resource_limit"
		}
		if errors.Is(operation.err, errWorkerProtocol) {
			code = "invalid_input"
		}
		if errors.Is(operation.err, ErrIncompatibleHistogramBounds) {
			code = "incompatible_histogram_bounds"
		}
		if writes.send(workerFrame{Kind: "result", Result: &workerResult{Status: "error", Code: code}}) != nil {
			return 78
		}
		return 0
	}
	if a.verifyInputs(ctx) != nil || operation.result.validate(b, *request.Request) != nil {
		return 78
	}
	if writes.send(workerFrame{Kind: "result", Result: &operation.result}) != nil {
		return 78
	}
	return 0
}

var workerExecutableQuarantine sync.Map
