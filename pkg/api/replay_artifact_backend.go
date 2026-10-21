package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"
)

const archiveReplayDirectory = ".apexload-replays"

// Reserve Windows aliases too, even when the controller runs on Unix.
func isReplayArtifactNamespace(name string) bool {
	name, _, _ = strings.Cut(name, ":")
	return strings.EqualFold(strings.TrimRight(name, ". "), archiveReplayDirectory)
}

// replayArtifactBackend is a passive transport adapter. The caller must reserve
// an exact generation before staging, CAS its receipt after Stage succeeds, and
// authorize each read against the current project and unexpired receipt. It does
// not infer ownership from a session ID, URI, timeout or missing process state.
type replayArtifactBackend struct {
	backend archiveBackendRuntime
	bucket  *blob.Bucket
}

func openReplayArtifactBackend(ctx context.Context, backend archiveBackendRuntime) (*replayArtifactBackend, error) {
	if err := backend.validate(); err != nil {
		return nil, err
	}
	if backend.Backend == archiveBackendFilesystem {
		if strings.TrimSpace(backend.LocalRoot) == "" {
			return nil, errors.New("replay archive root is required")
		}
		absolute, err := filepath.Abs(backend.LocalRoot)
		if err != nil {
			return nil, err
		}
		backend.LocalRoot = absolute
		root, err := openArchiveDirectoryNoSymlink(backend.LocalRoot)
		if err != nil {
			return nil, err
		}
		if err := root.Close(); err != nil {
			return nil, err
		}
	}
	bucket, err := backend.openBucket(ctx)
	if err != nil {
		return nil, err
	}
	return &replayArtifactBackend{backend: backend, bucket: bucket}, nil
}
func (b *replayArtifactBackend) Close() error {
	if b.bucket != nil {
		return b.bucket.Close()
	}
	return nil
}
func (b *replayArtifactBackend) sourceSHA256() string {
	identity := archiveObjectNamespace(b.backend)
	if b.backend.Backend == archiveBackendFilesystem {
		// A different filesystem root is a different authority, unlike a changed
		// local materialization root for the same object backend.
		identity = "filesystem\x00" + filepath.Clean(b.backend.LocalRoot)
	}
	return sha256BytesHex([]byte("replay-artifacts.v1\x00" + identity))
}
func replayArtifactHex(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func (b *replayArtifactBackend) validate(spec storage.ReplayArtifactSpec) error {
	if spec.Version != 1 || !replayArtifactHex(spec.Generation) || spec.SourceSHA256 != b.sourceSHA256() || !replayArtifactHex(spec.HARSHA256) || !replayArtifactHex(spec.ReportSHA256) || spec.HARBytes <= 0 || spec.HARBytes > 256<<20 || spec.ReportBytes <= 0 || spec.ReportBytes > 16<<20 {
		return errors.New("invalid replay artifact specification or changed backend")
	}
	return nil
}
func (b *replayArtifactBackend) key(spec storage.ReplayArtifactSpec, name string) string {
	prefix := archiveReplayDirectory + "/v1"
	if b.backend.Backend != archiveBackendFilesystem {
		prefix = joinArchiveURI(b.backend.Prefix, prefix)
	}
	return joinArchiveURI(prefix, spec.Generation, name)
}

// Stage never overwrites or deletes on failure. A failed or ambiguous upload
// leaves the reservation unjoined; only successful writes AND verified reads of
// both objects supply this adapter's writer-completion proof.
func (b *replayArtifactBackend) Stage(ctx context.Context, spec storage.ReplayArtifactSpec, har, report io.Reader) error {
	if err := b.validate(spec); err != nil {
		return err
	}
	if har == nil || report == nil {
		return errors.New("replay artifact readers are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.bucket == nil {
		parentPath, err := ensureArchiveRelativeDirectory(b.backend.LocalRoot, archiveReplayDirectory+"/v1", 0700)
		if err != nil {
			return err
		}
		parent, err := openArchiveDirectoryNoSymlink(parentPath)
		if err != nil {
			return err
		}
		err = parent.Mkdir(spec.Generation, 0700)
		closeErr := parent.Close()
		if err := errors.Join(err, closeErr); err != nil {
			return err
		}
	}
	for _, v := range []struct {
		name   string
		reader io.Reader
		size   int64
		digest string
	}{{"replay.har", har, spec.HARBytes, spec.HARSHA256}, {"report.json", report, spec.ReportBytes, spec.ReportSHA256}} {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := b.key(spec, v.name)
		var writer io.WriteCloser
		var err error
		if b.bucket != nil {
			writer, err = b.bucket.NewWriter(ctx, key, &blob.WriterOptions{IfNotExist: true})
		} else {
			writer, err = createArchiveArtifactExclusive(b.backend.LocalRoot, key, 0600, true)
		}
		if err != nil {
			return err
		}
		copyErr := copyReplayArtifact(ctx, writer, v.reader, v.size, v.digest)
		if f, ok := writer.(*os.File); ok && copyErr == nil {
			copyErr = f.Sync()
		}
		closeErr := writer.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
	}
	for _, v := range []struct {
		name   string
		size   int64
		digest string
	}{{"replay.har", spec.HARBytes, spec.HARSHA256}, {"report.json", spec.ReportBytes, spec.ReportSHA256}} {
		r, err := b.open(ctx, b.key(spec, v.name))
		if err != nil {
			return err
		}
		verifyErr := copyReplayArtifact(ctx, io.Discard, r, v.size, v.digest)
		if err := errors.Join(verifyErr, r.Close()); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func copyReplayArtifact(ctx context.Context, dst io.Writer, src io.Reader, size int64, digest string) error {
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(archiveContextReader{ctx: ctx, r: src}, size))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var extra [1]byte
	extraN, extraErr := io.ReadFull(archiveContextReader{ctx: ctx, r: src}, extra[:])
	if extraErr != nil && extraErr != io.EOF {
		return extraErr
	}
	if n != size || extraN != 0 || hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("replay artifact length or digest mismatch")
	}
	return nil
}
func (b *replayArtifactBackend) open(ctx context.Context, key string) (io.ReadCloser, error) {
	if b.bucket != nil {
		return b.bucket.NewReader(ctx, key, nil)
	}
	f, _, err := openArchiveArtifactForRead(b.backend.LocalRoot, key)
	return f, err
}

type replayArtifactRead struct {
	HAR       *os.File
	Report    *os.File
	directory string
	once      sync.Once
	closeErr  error
}

func (r *replayArtifactRead) Close() error {
	r.once.Do(func() { r.closeErr = errors.Join(r.HAR.Close(), r.Report.Close(), os.RemoveAll(r.directory)) })
	return r.closeErr
}

// Read verifies complete bytes into private disk staging before returning any
// successful reader. Callers must take bounded download admission first and
// close the result. It neither buffers a 256 MiB HAR nor synthesizes lost bytes.
func (b *replayArtifactBackend) Read(ctx context.Context, receipt storage.ReplayArtifactReceipt) (*replayArtifactRead, error) {
	if receipt.State != "ready" || !receipt.WriterJoined {
		return nil, errors.New("replay artifact is not published")
	}
	if err := b.validate(receipt.Spec); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "apex-replay-artifact-")
	if err != nil {
		return nil, err
	}
	result := &replayArtifactRead{directory: dir}
	success := false
	defer func() {
		if !success {
			_ = result.Close()
		}
	}()
	for _, v := range []struct {
		name   string
		size   int64
		digest string
		target **os.File
	}{{"replay.har", receipt.Spec.HARBytes, receipt.Spec.HARSHA256, &result.HAR}, {"report.json", receipt.Spec.ReportBytes, receipt.Spec.ReportSHA256, &result.Report}} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(filepath.Join(dir, v.name), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		*v.target = f
		r, err := b.open(ctx, b.key(receipt.Spec, v.name))
		if err != nil {
			return nil, err
		}
		verifyErr := copyReplayArtifact(ctx, f, r, v.size, v.digest)
		if err := errors.Join(verifyErr, r.Close()); err != nil {
			return nil, err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
	}
	success = true
	return result, nil
}

// Cleanup removes only the two immutable names of a joined cleanup tombstone.
// The caller may release the reservation only after success. No prefix sweep
// or adoption of another generation is permitted.
func (b *replayArtifactBackend) Cleanup(ctx context.Context, receipt storage.ReplayArtifactReceipt) error {
	if receipt.State != "cleanup" || !receipt.WriterJoined {
		return errors.New("replay artifact cleanup lacks writer proof")
	}
	if err := b.validate(receipt.Spec); err != nil {
		return err
	}
	for _, name := range []string{"replay.har", "report.json"} {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := b.key(receipt.Spec, name)
		if b.bucket != nil {
			if err := b.bucket.Delete(ctx, key); err != nil && gcerrors.Code(err) != gcerrors.NotFound {
				return err
			}
		} else {
			root, err := openArchiveDirectoryNoSymlink(b.backend.LocalRoot)
			if err != nil {
				return err
			}
			parent, leaf, release, openErr := openArchiveRelativeParent(root, key)
			err = openErr
			if err == nil {
				_, err = inspectArchiveArtifactTarget(parent, leaf, true)
				if err == nil {
					err = parent.Remove(leaf)
				}
			}
			release()
			closeErr := root.Close()
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove replay artifact: %w", err)
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	if b.bucket == nil {
		root, err := openArchiveDirectoryNoSymlink(b.backend.LocalRoot)
		if err != nil {
			return err
		}
		defer root.Close()
		parent, leaf, release, err := openArchiveRelativeParent(root, archiveReplayDirectory+"/v1/"+receipt.Spec.Generation)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		defer release()
		info, err := parent.Lstat(leaf)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("replay generation is not a directory")
		}
		if err := parent.Remove(leaf); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}
