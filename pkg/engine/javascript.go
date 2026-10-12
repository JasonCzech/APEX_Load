package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const nativeJSWorkerArgument = "--apexload-native-javascript-worker"
const nativeJSFrameBytes = protocol.NativeDataResultBytes + 8192

var nativeJSWorkers = make(chan struct{}, 4)
var nativeJSOutputKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

type nativeJSFrame struct {
	Kind     string                     `json:"kind"`
	Sequence uint64                     `json:"sequence,omitempty"`
	Source   string                     `json:"source,omitempty"`
	Inputs   json.RawMessage            `json:"inputs,omitempty"`
	Request  json.RawMessage            `json:"request,omitempty"`
	Result   *protocol.NativeDataResult `json:"result,omitempty"`
	Error    *protocol.NativeDataError  `json:"error,omitempty"`
	Output   json.RawMessage            `json:"output,omitempty"`
}

// NativeJavaScriptAvailable reports an actual hard-memory enforcement backend,
// not merely the interpreter's presence in the binary. The child checks again.
func NativeJavaScriptAvailable() bool { return nativeJSContainmentAvailable() }

func (vu *VUser) executeNativeJavaScript(ctx context.Context, action dsl.Action) (outErr error) {
	parsed, err := dsl.ParseNativeJavaScriptAction(action.Params)
	if err != nil {
		return err
	}
	defer func() {
		if outErr != nil {
			vu.clearLiveOutput(parsed.ResultPrefix)
		}
	}()
	if !NativeJavaScriptAvailable() {
		return nativeServiceUnavailable("native_javascript_v1 requires Linux hard memory and CPU limits")
	}
	if err := vu.prepareLiveTables(); err != nil {
		return err
	}
	if err := vu.prepareNativeServices(); err != nil {
		return err
	}
	// Only structured inputs pass through the ordinary resolver. In particular,
	// source never reaches interpolateAction/interpolateAnyMapWithSecrets.
	inputs, _ := vu.interpolateAnyMapWithSecrets(parsed.Inputs)
	encoded, err := json.Marshal(inputs)
	if err != nil || len(encoded) > dsl.NativeJavaScriptValueBytes {
		return nativeError("inputs_too_large")
	}
	call, cancel := context.WithTimeout(ctx, time.Duration(parsed.TimeoutMS)*time.Millisecond)
	defer cancel()
	invocation, err := vu.nativeServices.BeginInvocation(call, "javascript:"+uuid.NewString())
	if err != nil {
		return err
	}
	defer invocation.Close()
	output, err := runNativeJavaScript(call, invocation, parsed.Source, encoded)
	if err != nil {
		return err
	}
	if vu.liveTableLeaseError() != nil {
		return nativeError("continuity_lost")
	}
	vu.clearLiveOutput(parsed.ResultPrefix)
	var fields map[string]json.RawMessage
	if len(output) > dsl.NativeJavaScriptValueBytes || json.Unmarshal(output, &fields) != nil || fields == nil || len(fields) > 128 {
		return nativeError("output_invalid")
	}
	if parsed.ResultPrefix == "" {
		return nil
	}
	values := make(map[string]string, len(fields))
	for key, value := range fields {
		if !nativeJSOutputKey.MatchString(key) {
			return nativeError("output_invalid")
		}
		var text string
		if json.Unmarshal(value, &text) != nil {
			text = string(value)
		}
		values[parsed.ResultPrefix+"."+key] = text
	}
	return vu.mergeExtractions(&protocol.Result{Extractions: values})
}

func nativeJSScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), nativeJSFrameBytes)
	return scanner
}

func runNativeJavaScript(ctx context.Context, invocation protocol.NativeInvocation, source string, inputs json.RawMessage) (json.RawMessage, error) {
	select {
	case nativeJSWorkers <- struct{}{}:
		defer func() { <-nativeJSWorkers }()
	default:
		return nil, nativeError("worker_capacity")
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, nativeError("worker_unavailable")
	}
	work, err := os.MkdirTemp("", "apexload-native-js-")
	if err != nil {
		return nil, nativeError("worker_unavailable")
	}
	defer os.RemoveAll(work)
	call, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(call, executable, nativeJSWorkerArgument)
	cmd.Env = []string{"GOMEMLIMIT=64MiB", "GOMAXPROCS=1"}
	cmd.Dir = work
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nativeError("worker_unavailable")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, nativeError("worker_unavailable")
	}
	// Diagnostics from guest syntax/errors are never published: they may contain
	// resolved input values. Discarding them also avoids an unbounded pipe.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, nativeError("worker_unavailable")
	}
	defer func() { cancel(); stdin.Close(); stdout.Close(); _ = cmd.Wait() }()
	go func() {
		select {
		case <-call.Done():
		case <-invocation.Done():
			cancel()
		}
	}()
	encoder := json.NewEncoder(stdin)
	scanner := nativeJSScanner(stdout)
	read := func() (nativeJSFrame, error) {
		var frame nativeJSFrame
		if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &frame) != nil {
			return frame, nativeError("worker_protocol_failed")
		}
		return frame, nil
	}
	ready, err := read()
	if err != nil || ready.Kind != "ready" {
		return nil, nativeError("worker_containment_unavailable")
	}
	if err := encoder.Encode(nativeJSFrame{Kind: "execute", Source: source, Inputs: inputs}); err != nil {
		return nil, nativeError("worker_protocol_failed")
	}
	var sequence uint64
	for {
		frame, err := read()
		if err != nil {
			return nil, nativeError("worker_failed")
		}
		switch frame.Kind {
		case "call":
			if frame.Sequence != sequence+1 || frame.Sequence > 1024 {
				return nil, nativeError("worker_sequence_invalid")
			}
			sequence = frame.Sequence
			request, err := protocol.DecodeNativeDataRequest(frame.Request)
			var result protocol.NativeDataResult
			if err == nil {
				result, err = invocation.Call(call, sequence, request)
			}
			reply := nativeJSFrame{Kind: "reply", Sequence: sequence}
			if err != nil {
				typed, ok := err.(*protocol.NativeDataError)
				if !ok {
					typed = nativeError("operation_rejected")
				}
				reply.Error = typed
			} else {
				reply.Result = &result
			}
			if invocation.Err() != nil {
				return nil, nativeError("continuity_lost")
			}
			if err := encoder.Encode(reply); err != nil {
				return nil, nativeError("worker_protocol_failed")
			}
		case "result":
			if frame.Error != nil {
				return nil, nativeError("javascript_failed")
			}
			if len(frame.Output) > dsl.NativeJavaScriptValueBytes || !json.Valid(frame.Output) {
				return nil, nativeError("output_invalid")
			}
			return frame.Output, nil
		default:
			return nil, nativeError("worker_protocol_failed")
		}
	}
}

func nativeJSResultError(err error) nativeJSFrame {
	return nativeJSFrame{Kind: "result", Error: nativeError("javascript_failed")}
}
