//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

type workerNativeExecutor struct{}

func newWorkerNativeExecutor() (workerChildExecutor, error) { return workerNativeExecutor{}, nil }
func (workerNativeExecutor) execute(ctx context.Context, a *workerAttempt, r workerRequest) (workerResult, error) {
	if r.validate(a.bootstrap) != nil || a.verifyInputs(ctx) != nil {
		return workerResult{}, errWorkerProtocol
	}
	e := &Engine{root: a.path, MemoryLimit: strconv.FormatInt(a.bootstrap.Limits.MemoryBytes, 10) + "B", SpillLimit: strconv.FormatInt(a.bootstrap.Limits.SpillBytes, 10) + "B", Timeout: time.Duration(a.bootstrap.Limits.RuntimeMS) * time.Millisecond, workerThreads: a.bootstrap.Limits.Threads}
	paths := func(ids []string) []string {
		out := make([]string, len(ids))
		for i, id := range ids {
			h, _ := a.bootstrap.handle(id, "input")
			out[i] = a.nativePath(h)
		}
		return out
	}
	if r.Preview != nil {
		preview, err := e.childNativePreview(ctx, paths([]string{r.Preview.InputHandle})[0])
		if err != nil {
			return workerResult{}, err
		}
		typed, err := workerTypedPreview(preview)
		return workerResult{Status: "ok", Preview: &typed}, err
	}
	if r.Series != nil {
		points, bounds, err := e.childNativeSeriesBounds(ctx, paths(r.Series.InputHandles), r.Series.Query)
		return workerResult{Status: "ok", Points: &points, SeriesBounds: &bounds}, err
	}
	for _, id := range r.Normalize.OutputHandles {
		for _, h := range a.bootstrap.Handles {
			if h.Handle == id {
				if _, err := a.root.Lstat(h.RelativeName); !errors.Is(err, os.ErrNotExist) {
					return workerResult{}, errWorkerProtocol
				}
			}
		}
	}
	result, err := e.childNativeNormalize(ctx, NormalizeRequest{ProjectID: r.Normalize.ProjectID, DatasetID: r.Normalize.DatasetID, Files: paths(r.Normalize.InputHandles), Mapping: r.Normalize.Mapping, AllowRejected: r.Normalize.AllowRejected})
	if err != nil {
		return workerResult{}, err
	}
	typed := workerNormalizeResult{Accepted: result.Accepted, Rejected: result.Rejected, InputRows: result.InputRows, Rejections: result.Rejections, Outputs: []workerOutput{}}
	for i, id := range r.Normalize.InputHandles {
		for measure := range r.Normalize.Mapping.Measures {
			for _, role := range []string{"normalized", "rejection"} {
				var plan workerHandle
				for _, h := range a.bootstrap.Handles {
					if h.Role == role && h.InputHandle == id && h.Measure == measure {
						plan = h
					}
				}
				if role == "rejection" && !r.Normalize.AllowRejected {
					continue
				}
				name := workerNativePartName(i, measure, role)
				files := result.Files
				if role == "rejection" {
					files = result.RejectionFiles
				}
				var source string
				for _, path := range files {
					if filepath.Base(path) == name {
						if source != "" {
							return workerResult{}, errWorkerProtocol
						}
						source = path
					}
				}
				if source == "" {
					if role == "rejection" {
						continue
					}
					return workerResult{}, errWorkerProtocol
				}
				out, err := workerInstallNativeOutput(ctx, a, plan, source)
				if err != nil {
					return workerResult{}, err
				}
				if role == "normalized" {
					for _, part := range result.Partitions {
						if part.File == source {
							if out.Partition != nil {
								return workerResult{}, errWorkerProtocol
							}
							stats := part.Stats.Clone()
							out.Partition = &stats
						}
					}
					if out.Partition == nil {
						return workerResult{}, errWorkerProtocol
					}
				}
				typed.Outputs = append(typed.Outputs, out)
			}
		}
	}
	if err := workerBoundPartitionMetadata(typed.Outputs, r.Normalize.ProjectID, r.Normalize.DatasetID, r.Normalize.Mapping.Version); err != nil {
		return workerResult{}, err
	}
	out := workerResult{Status: "ok", Normalize: &typed}
	if out.validate(a.bootstrap, r) != nil {
		return workerResult{}, errWorkerProtocol
	}
	return out, nil
}
func workerNativePartName(file, measure int, role string) string {
	prefix := "part-"
	if role == "rejection" {
		prefix = "reject-"
	}
	left := strconv.Itoa(file)
	left = strings.Repeat("0", max(0, 4-len(left))) + left
	right := strconv.Itoa(measure)
	right = strings.Repeat("0", max(0, 2-len(right))) + right
	return prefix + left + "-" + right + ".parquet"
}
func workerInstallNativeOutput(ctx context.Context, a *workerAttempt, h workerHandle, path string) (workerOutput, error) {
	rel, err := filepath.Rel(a.path, path)
	if err != nil || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || h.Handle == "" {
		return workerOutput{}, errWorkerProtocol
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "normalized-") || !workerPortableName(parts[1]) {
		return workerOutput{}, errWorkerProtocol
	}
	dir, err := a.root.Lstat(parts[0])
	if err != nil || !dir.IsDir() || dir.Mode()&os.ModeSymlink != 0 {
		return workerOutput{}, errWorkerProtocol
	}
	info, err := a.root.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 12 || info.Size() > h.Bytes {
		return workerOutput{}, errWorkerProtocol
	}
	// Link is no-replace; a native output can never overwrite an approved input.
	if err := a.root.Link(rel, h.RelativeName); err != nil {
		return workerOutput{}, errWorkerProtocol
	}
	if err := a.root.Remove(rel); err != nil {
		return workerOutput{}, errWorkerProtocol
	}
	schema, err := analysis.InspectParquet(a.nativePath(h))
	if err != nil {
		return workerOutput{}, errWorkerProtocol
	}
	f, err := a.root.Open(h.RelativeName)
	if err != nil {
		return workerOutput{}, errWorkerProtocol
	}
	a.trackDescriptor(f)
	hash := sha256.New()
	n, readErr := workerCopyContext(ctx, hash, io.LimitReader(f, h.Bytes+1))
	closeErr := a.closeDescriptor(f)
	if readErr != nil || closeErr != nil || n != schema.FileSize || n > h.Bytes {
		return workerOutput{}, errWorkerProtocol
	}
	out := workerOutput{Handle: h.Handle, SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: n, Rows: schema.Rows}
	if workerVerifyFile(ctx, a, a.root, h, out.Bytes, out.SHA256) != nil {
		return workerOutput{}, errWorkerProtocol
	}
	return out, nil
}
func workerTypedPreview(preview Preview) (workerPreviewResult, error) {
	result := workerPreviewResult{Schema: preview.Schema, Samples: [][]workerCell{}}
	for _, sample := range preview.Samples {
		if len(sample) != len(preview.Schema.Columns) {
			return workerPreviewResult{}, errWorkerProtocol
		}
		row := make([]workerCell, len(preview.Schema.Columns))
		for i, col := range preview.Schema.Columns {
			value, present := sample[col.Path]
			if !present {
				return workerPreviewResult{}, errWorkerProtocol
			}
			switch v := value.(type) {
			case nil:
				row[i] = workerCell{Kind: "null"}
			case bool:
				copy := v
				row[i] = workerCell{Kind: "bool", Boolean: &copy}
			case time.Time:
				copy := v.UTC()
				row[i] = workerCell{Kind: "timestamp", Timestamp: &copy}
			case string:
				if v == "[redacted]" {
					row[i] = workerCell{Kind: "redacted"}
				} else if v == "non-finite" {
					row[i] = workerCell{Kind: "non_finite"}
				} else {
					return workerPreviewResult{}, errWorkerProtocol
				}
			case float32:
				if math.IsInf(float64(v), 0) || math.IsNaN(float64(v)) {
					return workerPreviewResult{}, errWorkerProtocol
				}
				text := strconv.FormatFloat(float64(v), 'g', -1, 32)
				row[i] = workerCell{Kind: "number", Number: &text}
			case float64:
				if math.IsInf(v, 0) || math.IsNaN(v) {
					return workerPreviewResult{}, errWorkerProtocol
				}
				text := strconv.FormatFloat(v, 'g', -1, 64)
				row[i] = workerCell{Kind: "number", Number: &text}
			case int8, int16, int32, int64, uint8, uint16, uint32, uint64:
				body, err := json.Marshal(v)
				if err != nil {
					return workerPreviewResult{}, errWorkerProtocol
				}
				text := string(body)
				row[i] = workerCell{Kind: "number", Number: &text}
			default:
				return workerPreviewResult{}, errWorkerProtocol
			}
		}
		result.Samples = append(result.Samples, row)
	}
	return result, nil
}

func workerNativeResourceError(err error) bool { return errors.Is(err, errResourceLimit) }
