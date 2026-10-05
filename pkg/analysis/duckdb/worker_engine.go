//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Public Engine operations always enter the parent supervisor. No request-
// facing method can call the private native executor or withDB directly.
func (e *Engine) Preview(ctx context.Context, path string) (Preview, error) {
	a, request, err := e.prepareWorkerBounded(ctx, []string{path}, nil, nil)
	if err != nil {
		return Preview{}, err
	}
	result, err := runWorkerParent(a, request, e.workerOptions())
	if err != nil {
		_ = workerFinishAttempt(a)
		return Preview{}, err
	}
	defer result.release()
	return workerPublicPreview(*result.result.Preview), nil
}
func (e *Engine) Series(ctx context.Context, files []string, query SeriesQuery) ([]analysis.MetricPoint, error) {
	if len(files) < 1 || len(files) > 1024 {
		return nil, errWorkerProtocol
	}
	files = append([]string{}, files...)
	if query.Validate() != nil {
		return nil, errWorkerProtocol
	}
	query.Dimensions = maps.Clone(query.Dimensions)
	a, request, err := e.prepareWorkerBounded(ctx, files, nil, &query)
	if err != nil {
		return nil, err
	}
	result, err := runWorkerParent(a, request, e.workerOptions())
	if err != nil {
		_ = workerFinishAttempt(a)
		return nil, err
	}
	defer result.release()
	return *result.result.Points, nil
}
func (e *Engine) Normalize(ctx context.Context, input NormalizeRequest) (NormalizeResult, error) {
	var cloneErr error
	input, cloneErr = workerCloneNormalizeInput(input)
	if cloneErr != nil {
		return NormalizeResult{}, cloneErr
	}
	a, request, err := e.prepareWorkerBounded(ctx, input.Files, &input, nil)
	if err != nil {
		return NormalizeResult{}, err
	}
	result, err := runWorkerParent(a, request, e.workerOptions())
	if err != nil {
		_ = workerFinishAttempt(a)
		return NormalizeResult{}, err
	}
	// Hold the physical attempt through API copy and authoritative publication.
	// Public callers must OpenOutput and ReleaseOutputs, including error exits.
	out := NormalizeResult{Accepted: result.result.Normalize.Accepted, Rejected: result.result.Normalize.Rejected, InputRows: result.result.Normalize.InputRows, Rejections: result.result.Normalize.Rejections, Files: []string{}, RejectionFiles: []string{}}
	e.workerMu.Lock()
	if e.workerResult != nil {
		e.workerMu.Unlock()
		_ = result.release()
		return NormalizeResult{}, errWorkerOwnership
	}
	e.workerResult = result
	e.workerOutput = map[string]workerOutput{}
	for _, descriptor := range result.result.Normalize.Outputs {
		for _, handle := range a.bootstrap.Handles {
			if handle.Handle == descriptor.Handle {
				path := a.nativePath(handle)
				e.workerOutput[path] = descriptor
				if handle.Role == "normalized" {
					out.Files = append(out.Files, path)
				} else {
					out.RejectionFiles = append(out.RejectionFiles, path)
				}
			}
		}
	}
	e.workerMu.Unlock()
	return out, nil
}
func (e *Engine) OpenOutput(path string) (io.ReadCloser, error) {
	e.workerMu.Lock()
	descriptor, ok := e.workerOutput[path]
	result := e.workerResult
	e.workerMu.Unlock()
	if !ok || result == nil {
		return nil, errWorkerProtocol
	}
	return result.attempt.openOutputBounded(descriptor, 6*time.Second)
}
func (e *Engine) ReleaseOutputs() error {
	e.workerMu.Lock()
	defer e.workerMu.Unlock()
	if e.workerResult == nil {
		return nil
	}
	if err := e.workerResult.release(); err != nil {
		return err
	}
	e.workerResult = nil
	clear(e.workerOutput)
	return nil
}
func (e *Engine) workerOptions() workerRunOptions {
	return workerRunOptions{profile: e.workerProfile, probe: workerObserveResources}
}
func workerRandomHex(size int) (string, error) {
	body := make([]byte, size)
	if _, err := rand.Read(body); err != nil {
		return "", errWorkerProtocol
	}
	return hex.EncodeToString(body), nil
}

// A blocked filesystem hash/open/close remains one admitted parent owner. The
// caller may return after its actual deadline/proof window without detaching a
// replacement job or losing the exact goroutine/descriptors still held there.
func (e *Engine) prepareWorkerBounded(ctx context.Context, paths []string, normalize *NormalizeRequest, series *SeriesQuery) (*workerAttempt, workerRequest, error) {
	if e == nil || e.Timeout < time.Millisecond || e.Timeout > 15*time.Minute {
		return nil, workerRequest{}, errWorkerProtocol
	}
	ctx, cancel := context.WithTimeout(ctx, e.Timeout)
	type result struct {
		attempt *workerAttempt
		request workerRequest
		err     error
	}
	done := make(chan result, 1)
	go func() { a, r, err := e.prepareWorker(ctx, paths, normalize, series); done <- result{a, r, err} }()
	proof := time.NewTimer(e.Timeout + 6*time.Second)
	if deadline, ok := ctx.Deadline(); ok {
		proof.Stop()
		proof.Reset(max(time.Duration(0), time.Until(deadline)) + 6*time.Second)
	}
	defer proof.Stop()
	ctxDone := ctx.Done()
	for {
		select {
		case result := <-done:
			if result.err != nil {
				cancel()
			} else {
				result.attempt.preparationCancel = cancel
			}
			return result.attempt, result.request, result.err
		case <-ctxDone:
			ctxDone = nil
			if !proof.Stop() {
				select {
				case <-proof.C:
				default:
				}
			}
			proof.Reset(6 * time.Second)
		case <-proof.C:
			cancel()
			go func() {
				late := <-done
				if late.attempt != nil {
					_ = workerFinishAttempt(late.attempt)
				}
			}()
			return nil, workerRequest{}, errWorkerOwnership
		}
	}
}

func (e *Engine) prepareWorker(ctx context.Context, paths []string, normalize *NormalizeRequest, series *SeriesQuery) (retAttempt *workerAttempt, retRequest workerRequest, retErr error) {
	if e == nil || len(paths) < 1 || len(paths) > 1024 || workerContextError(ctx) != nil {
		return nil, workerRequest{}, errWorkerProtocol
	}
	memory, err := workerParseBudget(e.MemoryLimit, 2<<30)
	if err != nil {
		return nil, workerRequest{}, err
	}
	spill, err := workerParseBudget(e.SpillLimit, 8<<30)
	if err != nil {
		return nil, workerRequest{}, err
	}
	if e.Timeout <= 0 || e.Timeout > 15*time.Minute || e.Timeout < time.Millisecond {
		return nil, workerRequest{}, errWorkerProtocol
	}
	var root *os.Root
	files := make([]*os.File, 0, len(paths))
	pipeline := workerPipelineForContext(ctx)
	if pipeline != nil {
		if err := pipeline.admitEngineResourceLimits(e, memory, spill); err != nil {
			return nil, workerRequest{}, err
		}
	} else if e.borrowedParent != nil {
		return nil, workerRequest{}, errWorkerOwnership
	}
	var sourceLease *NativeIOLease
	owned, failed := false, true
	// Reserve the approved memory/spill profile before opening metadata owners.
	// The exact input/output geometry is admitted after bounded stat preflight and
	// before the first source read; a failed Close remains charged even then.
	if pipeline == nil {
		ctx, pipeline, err = ReserveNativePipeline(ctx, e.root, PipelinePlan{InputBytes: 12, OutputBytes: 12, MemoryBytes: memory, SpillBytes: spill, Runtime: e.Timeout})
		if err != nil {
			return nil, workerRequest{}, err
		}
		owned = true
	}
	sourceLease, err = pipeline.AcquireIO()
	if err != nil {
		if owned {
			_ = pipeline.Release()
		}
		return nil, workerRequest{}, err
	}
	defer func() {
		physicallyClosed := true
		for _, file := range files {
			if sourceLease.CloseOwner(file) != nil {
				physicallyClosed = false
			}
		}
		if root != nil && sourceLease.CloseOwner(root) != nil {
			physicallyClosed = false
		}
		sourceLease.Joined()
		if !physicallyClosed {
			retErr = errWorkerOwnership
			if retAttempt != nil {
				_ = workerFinishAttempt(retAttempt)
				retAttempt = nil
				retRequest = workerRequest{}
			}
		}
		if pipeline != nil && owned && (failed || !physicallyClosed) {
			_ = pipeline.Release()
		}
	}()
	root, err = os.OpenRoot(e.root)
	if err != nil {
		return nil, workerRequest{}, errWorkerProtocol
	}
	sourceLease.TrackOwner(root)
	sizes := make([]int64, len(paths))
	var inputBytes int64
	for i, path := range paths {
		absolute, err := e.authorizedPath(path)
		if err != nil {
			return nil, workerRequest{}, errWorkerProtocol
		}
		relative, err := filepath.Rel(e.root, absolute)
		if err != nil {
			return nil, workerRequest{}, errWorkerProtocol
		}
		file, err := root.Open(relative)
		if err != nil {
			return nil, workerRequest{}, errWorkerProtocol
		}
		sourceLease.TrackOwner(file)
		files = append(files, file)
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() < 12 {
			return nil, workerRequest{}, errWorkerProtocol
		}
		sizes[i] = info.Size()
		inputBytes, err = workerAdd(inputBytes, info.Size())
		if err != nil || inputBytes > 20<<30 {
			return nil, workerRequest{}, errWorkerResourceLimit
		}
	}
	slots := 0
	if normalize != nil {
		if len(normalize.Mapping.Measures) < 1 || len(normalize.Mapping.Measures) > 64 || len(paths)*len(normalize.Mapping.Measures) > 1024 {
			return nil, workerRequest{}, errWorkerProtocol
		}
		slots = len(paths) * len(normalize.Mapping.Measures)
		if normalize.AllowRejected {
			slots *= 2
		}
	}
	outputBytes := int64(12)
	if slots > 0 {
		if inputBytes > (20<<30)/4 {
			outputBytes = 20 << 30
		} else {
			outputBytes = max(int64(slots)*(1<<20), inputBytes*4)
		}
	}
	if owned {
		if err = pipeline.approveSourceGeometry(e.root, inputBytes, outputBytes); err != nil {
			return nil, workerRequest{}, err
		}
	} else {
		outputBytes = min(outputBytes, pipeline.limits.OutputBytes)
	}
	build, err := workerExpectedExecutableDigest()
	if err != nil {
		return nil, workerRequest{}, errWorkerProtocol
	}
	attempt, err := workerRandomHex(16)
	if err != nil {
		return nil, workerRequest{}, err
	}
	nonce, err := workerRandomHex(32)
	if err != nil {
		return nil, workerRequest{}, err
	}
	bootstrap := workerBootstrap{Identity: workerIdentity{Version: workerProtocolVersion, BuildID: build, AttemptID: attempt, Nonce: nonce}, Limits: workerLimits{Threads: 2, MemoryBytes: memory, SpillBytes: spill, InputBytes: inputBytes, OutputBytes: outputBytes, RuntimeMS: e.Timeout.Milliseconds()}, Handles: []workerHandle{}}
	if e.workerBeforeHash != nil {
		e.workerBeforeHash()
	}
	inputIDs := make([]string, len(paths))
	for i, file := range files {
		before, err := file.Stat()
		if err != nil {
			return nil, workerRequest{}, errWorkerProtocol
		}
		digest := sha256.New()
		n, copyErr := workerCopyContext(ctx, digest, io.LimitReader(file, sizes[i]+1))
		after, statErr := file.Stat()
		if copyErr != nil || statErr != nil || n != sizes[i] || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || after.Size() != sizes[i] {
			return nil, workerRequest{}, errWorkerProtocol
		}
		id, err := workerRandomHex(16)
		if err != nil {
			return nil, workerRequest{}, err
		}
		inputIDs[i] = id
		bootstrap.Handles = append(bootstrap.Handles, workerHandle{Handle: id, Role: "input", RelativeName: "input-" + strconv.Itoa(i) + ".parquet", Bytes: sizes[i], SHA256: hex.EncodeToString(digest.Sum(nil))})
	}
	request := workerRequest{}
	if normalize == nil && series == nil {
		request.Preview = &workerPreviewOperation{InputHandle: inputIDs[0]}
		if len(paths) != 1 {
			return nil, workerRequest{}, errWorkerProtocol
		}
	}
	if series != nil {
		copyQuery := *series
		request.Series = &workerSeriesOperation{InputHandles: inputIDs, Query: copyQuery}
	}
	if normalize != nil {
		op := workerNormalizeOperation{ProjectID: normalize.ProjectID, DatasetID: normalize.DatasetID, InputHandles: inputIDs, Mapping: normalize.Mapping, AllowRejected: normalize.AllowRejected, OutputHandles: []string{}}
		quota := outputBytes / int64(slots)
		if quota < 12 {
			return nil, workerRequest{}, errWorkerResourceLimit
		}
		for i, input := range inputIDs {
			for measure := range op.Mapping.Measures {
				for _, role := range []string{"normalized", "rejection"} {
					if role == "rejection" && !op.AllowRejected {
						continue
					}
					id, err := workerRandomHex(16)
					if err != nil {
						return nil, workerRequest{}, err
					}
					op.OutputHandles = append(op.OutputHandles, id)
					bootstrap.Handles = append(bootstrap.Handles, workerHandle{Handle: id, Role: role, RelativeName: workerNativePartName(i, measure, role), Bytes: quota, InputHandle: input, Measure: measure})
				}
			}
		}
		request.Normalize = &op
	}
	bootstrap.Identity.ManifestDigest, err = bootstrap.manifestDigest()
	if err != nil || bootstrap.validate() != nil || request.validate(bootstrap) != nil {
		return nil, workerRequest{}, errWorkerProtocol
	}
	a, _, err := workerPipelineAttempt(ctx, e.root, bootstrap)
	if err != nil {
		return nil, workerRequest{}, err
	}
	if owned {
		a.privatePipeline = pipeline
	}
	for i, file := range files {
		if err = a.stageInput(inputIDs[i], file); err != nil {
			_ = workerFinishAttempt(a)
			return nil, workerRequest{}, err
		}
	}
	failed = false
	return a, request, nil
}
func workerPublicPreview(source workerPreviewResult) Preview {
	result := Preview{Schema: source.Schema, Samples: make([]map[string]any, len(source.Samples))}
	for i, cells := range source.Samples {
		row := map[string]any{}
		for j, cell := range cells {
			var value any
			switch cell.Kind {
			case "bool":
				value = *cell.Boolean
			case "number":
				value = json.Number(*cell.Number)
			case "timestamp":
				value = *cell.Timestamp
			case "redacted":
				value = "[redacted]"
			case "non_finite":
				value = "non-finite"
			}
			row[source.Schema.Columns[j].Path] = value
		}
		result.Samples[i] = row
	}
	return result
}

func workerCloneNormalizeInput(input NormalizeRequest) (NormalizeRequest, error) {
	if len(input.Files) < 1 || len(input.Files) > 1024 || validateWorkerMapping(input.Mapping) != nil {
		return NormalizeRequest{}, errWorkerProtocol
	}
	input.Files = append([]string{}, input.Files...)
	input.Mapping.Timestamp = append(Column{}, input.Mapping.Timestamp...)
	input.Mapping.Measures = append([]Measure{}, input.Mapping.Measures...)
	for i := range input.Mapping.Measures {
		measure := &input.Mapping.Measures[i]
		measure.Value = append(Column{}, measure.Value...)
		measure.NameColumn = append(Column{}, measure.NameColumn...)
	}
	if input.Mapping.Identity != nil {
		identity := *input.Mapping.Identity
		identity.Source = append(Column{}, identity.Source...)
		identity.Host = append(Column{}, identity.Host...)
		identity.Service = append(Column{}, identity.Service...)
		identity.Dimensions = append([]analysis.ParquetDimensionMapping{}, identity.Dimensions...)
		for i := range identity.Dimensions {
			identity.Dimensions[i].Value = append(Column{}, identity.Dimensions[i].Value...)
		}
		input.Mapping.Identity = &identity
	}
	return input, nil
}
