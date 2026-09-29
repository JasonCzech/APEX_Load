// Package archivequota admits incremental archive growth against retained data.
// Callers must route every writer in the counted namespace through a Budget.
package archivequota

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/jasonczech/apexload/pkg/internal/filelock"
)

// DirectoryName is reserved inside each admitted archive namespace. Retention
// and run-ID validation must never treat it as an archive.
const DirectoryName = ".apexload-backlog"

// IsReservedName also rejects case and trailing-dot aliases used by common
// Windows/macOS filesystems. Callers still verify existing directory identities.
func IsReservedName(name string) bool {
	name, _, _ = strings.Cut(name, ":")
	return strings.EqualFold(strings.TrimRight(name, ". "), DirectoryName)
}

const namespace = DirectoryName
const maxChunk int64 = 4 << 20
const maxEntries = 100000

var ErrCapacity = errors.New("archive backlog capacity exceeded")

type Options struct {
	MaxBytes int64
	// Exclude lists explicitly separate top-level entries. All processes using
	// a root must use the same exclusions. Unknown payload is always counted.
	Exclude []string
}

type Budget struct {
	mu             sync.Mutex
	root, ledger   *os.Root
	max, remaining int64
	excluded       map[string]bool
	token          string
	identity       os.FileInfo
	closed         bool
	err            error
}

// New duplicates an already safely opened namespace. It does not reserve the
// configured maximum, delete data, or reconcile another process's reservations.
func New(root *os.Root, options Options) (*Budget, error) {
	if root == nil || options.MaxBytes <= 0 || options.MaxBytes > 1<<50 {
		return nil, errors.New("invalid archive backlog configuration")
	}
	excluded := map[string]bool{}
	for _, name := range options.Exclude {
		if name == "" || name == "." || name == ".." || IsReservedName(name) || strings.ContainsAny(name, "/\\") {
			return nil, errors.New("invalid archive backlog exclusion")
		}
		excluded[name] = true
	}
	copy, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	if err := copy.Mkdir(namespace, 0700); err != nil && !os.IsExist(err) {
		copy.Close()
		return nil, err
	}
	ledger, err := openDir(copy, namespace)
	if err != nil {
		copy.Close()
		return nil, err
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		ledger.Close()
		copy.Close()
		return nil, err
	}
	return &Budget{root: copy, ledger: ledger, max: options.MaxBytes, excluded: excluded, token: "lease-" + hex.EncodeToString(token)}, nil
}

// Write wraps physical writes to files in the admitted namespace; buffering
// must sit above this layer. It charges durable reservations before writing.
// Keeping the whole
// current chunk charged until refill conservatively counts up to one chunk
// twice, but avoids inventory and fsync on every physical write. A crash leaves
// its reservation charged; elapsed time is never proof that its holder stopped.
func (b *Budget) Write(ctx context.Context, destination io.Writer, data []byte) (total int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, os.ErrClosed
	}
	if b.err != nil {
		return 0, b.err
	}
	if destination == nil {
		return 0, errors.New("archive backlog destination is missing")
	}
	defer func() {
		if err != nil {
			b.err = err
		}
	}()
	for len(data) > 0 {
		if err = ctx.Err(); err != nil {
			return total, err
		}
		if b.remaining == 0 {
			if err = b.refill(ctx); err != nil {
				return total, err
			}
		}
		if err = b.unchanged(); err != nil {
			return total, err
		}
		count := len(data)
		if count > 64<<10 {
			count = 64 << 10
		}
		if int64(count) > b.remaining {
			count = int(b.remaining)
		}
		n, writeErr := destination.Write(data[:count])
		if n < 0 || n > count {
			return total, errors.New("archive backlog writer returned invalid byte count")
		}
		total += n
		b.remaining -= int64(n)
		data = data[n:]
		if writeErr != nil {
			return total, writeErr
		}
		if n != count {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (b *Budget) refill(ctx context.Context) error {
	release, err := b.lock(ctx)
	if err != nil {
		return err
	}
	defer release()
	if b.identity != nil {
		if err := b.unchanged(); err != nil {
			return err
		}
	}
	used, err := b.usage(ctx)
	if err != nil {
		return err
	}
	available := b.max - used
	if available <= 0 {
		return ErrCapacity
	}
	if available > maxChunk {
		available = maxChunk
	}
	// The old reservation remains charged until the replacement is durable.
	pending := strings.Replace(b.token, "lease-", "pending-", 1)
	f, err := b.ledger.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	info, statErr := f.Stat()
	defer func() {
		if info != nil {
			now, e := b.ledger.Lstat(pending)
			if e == nil && os.SameFile(info, now) {
				_ = b.ledger.Remove(pending)
			}
		}
	}()
	_, writeErr := io.WriteString(f, strconv.FormatInt(available, 10)+"\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(statErr, writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.identity != nil {
		if err := b.unchanged(); err != nil {
			return err
		}
	} else if _, err := b.ledger.Lstat(b.token); !os.IsNotExist(err) {
		return errors.New("archive backlog reservation already exists")
	}
	if err := b.ledger.Rename(pending, b.token); err != nil {
		return err
	}
	if err := syncDir(b.ledger); err != nil {
		return err
	}
	if err := syncDir(b.root); err != nil {
		return err
	}
	b.identity = info
	b.remaining = available
	return nil
}

// Close is only for a joined writer. If the marker was replaced, leave it alone.
// A failed cleanup keeps its reservation charged; it never removes source data.
func (b *Budget) Close(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	defer b.root.Close()
	defer b.ledger.Close()
	if b.identity == nil {
		return nil
	}
	release, err := b.lock(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := b.unchanged(); err != nil {
		return err
	}
	if err := b.ledger.Remove(b.token); err != nil {
		return err
	}
	return syncDir(b.ledger)
}
func (b *Budget) ledgerUnchanged() error {
	current, err := b.root.Lstat(namespace)
	opened, openErr := b.ledger.Stat(".")
	if err != nil || openErr != nil || !current.IsDir() || !os.SameFile(current, opened) {
		return errors.New("archive backlog ledger identity changed")
	}
	return nil
}
func (b *Budget) unchanged() error {
	if err := b.ledgerUnchanged(); err != nil {
		return err
	}
	info, err := b.ledger.Lstat(b.token)
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(info, b.identity) {
		return errors.New("archive backlog reservation identity changed")
	}
	return nil
}

func (b *Budget) lock(ctx context.Context) (func(), error) {
	if err := b.ledgerUnchanged(); err != nil {
		return nil, err
	}
	f, err := b.ledger.OpenFile("capacity.lock", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if os.IsExist(err) {
		f, err = openFile(b.ledger, "capacity.lock", os.O_RDWR)
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	unlock, err := filelock.LockContext(ctx, f)
	if err != nil {
		f.Close()
		return nil, err
	}
	if err := b.ledgerUnchanged(); err != nil {
		unlock()
		f.Close()
		return nil, err
	}
	current, err := b.ledger.Lstat("capacity.lock")
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(current, info) {
		unlock()
		f.Close()
		return nil, errors.New("archive backlog lock identity changed")
	}
	return func() { _ = unlock(); _ = f.Close() }, nil
}

// Inventory is bounded, descriptor-relative and fail-closed for links, special
// files, unknown ledger records and changing entries. Actual data plus every
// other reservation is charged; only this joined writer's old chunk is replaced.
func (b *Budget) usage(ctx context.Context) (int64, error) {
	ledgerIdentity, err := b.ledger.Stat(".")
	if err != nil {
		return 0, err
	}
	entries := 0
	var walk func(*os.Root, int, bool) (int64, error)
	walk = func(root *os.Root, depth int, ledger bool) (int64, error) {
		if depth > 16 {
			return 0, errors.New("archive backlog inventory depth exceeded")
		}
		dir, err := root.Open(".")
		if err != nil {
			return 0, err
		}
		defer dir.Close()
		var total int64
		for {
			batch, readErr := dir.ReadDir(128)
			for _, entry := range batch {
				entries++
				if entries > maxEntries {
					return 0, errors.New("archive backlog inventory entry limit exceeded")
				}
				if err := ctx.Err(); err != nil {
					return 0, err
				}
				name := entry.Name()
				info, err := root.Lstat(name)
				if err != nil {
					return 0, err
				}
				isLedger := depth == 0 && info.IsDir() && os.SameFile(info, ledgerIdentity)
				if depth == 0 && b.excluded[name] && !isLedger {
					continue
				}
				var size int64
				if info.IsDir() {
					if ledger {
						return 0, errors.New("unexpected archive backlog ledger directory")
					}
					child, err := openDir(root, name)
					if err != nil {
						return 0, err
					}
					size, err = walk(child, depth+1, isLedger)
					child.Close()
					if err != nil {
						return 0, err
					}
				} else if info.Mode().IsRegular() {
					size = info.Size()
					if ledger {
						if name == "capacity.lock" {
							if size != 0 {
								return 0, errors.New("invalid archive backlog lock")
							}
							continue
						}
						prefix, token, ok := strings.Cut(name, "-")
						decoded, decodeErr := hex.DecodeString(token)
						if !ok || (prefix != "lease" && prefix != "pending") || decodeErr != nil || len(decoded) != 16 || size <= 0 || size > 32 {
							return 0, errors.New("invalid archive backlog reservation")
						}
						f, err := openFile(root, name, os.O_RDONLY)
						if err != nil {
							return 0, err
						}
						data, err := io.ReadAll(io.LimitReader(f, 33))
						f.Close()
						if err != nil {
							return 0, err
						}
						n, err := strconv.ParseInt(strings.TrimSuffix(string(data), "\n"), 10, 64)
						if err != nil || n <= 0 || n > maxChunk || string(data) != strconv.FormatInt(n, 10)+"\n" {
							return 0, errors.New("invalid archive backlog reservation")
						}
						size = n
						if name == b.token {
							if b.identity == nil || !os.SameFile(info, b.identity) {
								return 0, errors.New("archive backlog reservation changed")
							}
							size = 0
						}
					}
				} else {
					return 0, fmt.Errorf("archive backlog inventory contains non-regular entry %q", name)
				}
				if size < 0 || size > b.max-total {
					return 0, ErrCapacity
				}
				total += size
			}
			if readErr == io.EOF {
				return total, nil
			}
			if readErr != nil {
				return 0, readErr
			}
		}
	}
	return walk(b.root, 0, false)
}

func openDir(root *os.Root, name string) (*os.Root, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, errors.New("archive backlog directory is not a directory")
	}
	child, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	after, afterErr := root.Lstat(name)
	if err != nil || afterErr != nil || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(after, opened) {
		child.Close()
		return nil, errors.New("archive backlog directory identity changed")
	}
	return child, nil
}
func openFile(root *os.Root, name string, flags int) (*os.File, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("archive backlog entry is not regular")
	}
	f, err := root.OpenFile(name, flags, 0)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	after, afterErr := root.Lstat(name)
	if err != nil || afterErr != nil || !after.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(after, opened) {
		f.Close()
		return nil, errors.New("archive backlog file identity changed")
	}
	return f, nil
}
func syncDir(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	syncErr := f.Sync()
	if runtime.GOOS == "windows" && os.IsPermission(syncErr) {
		syncErr = nil
	}
	return errors.Join(syncErr, f.Close())
}
