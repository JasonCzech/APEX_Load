package api

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"
)

const analysisCleanupPhysicalLimit = 16
const analysisCleanupPhysicalWait = time.Second
const analysisCleanupPhysicalTimeout = 2 * time.Minute

var errAnalysisCleanupPhysical = errors.New("analytical cleanup physical ownership remains unproven")

type analysisCleanupRoot interface {
	io.Closer
	Remove(string) error
	RemoveAll(string) error
}
type analysisCleanupBucket interface {
	io.Closer
	Delete(context.Context, string) error
	List(*blob.ListOptions) *blob.ListIterator
}
type analysisCleanupPhysicalOps struct {
	openRoot   func(string) (analysisCleanupRoot, error)
	openBucket func(context.Context, archiveBackendRuntime) (analysisCleanupBucket, error)
}

var defaultAnalysisCleanupPhysicalOps = analysisCleanupPhysicalOps{
	openRoot: func(path string) (analysisCleanupRoot, error) {
		root, err := os.OpenRoot(path)
		if root == nil {
			return nil, err
		}
		return root, err
	},
	openBucket: func(ctx context.Context, b archiveBackendRuntime) (analysisCleanupBucket, error) {
		bucket, err := b.openBucket(ctx)
		if bucket == nil {
			return nil, err
		}
		return bucket, err
	},
}

type analysisCleanupIdentity struct {
	backend   archiveBackendRuntime
	kind, key string
}
type analysisCleanupDescriptor struct {
	closer io.Closer
	once   sync.Once
	done   chan struct{}
	err    error
	joined bool
}
type analysisCleanupPhysicalOwner struct {
	mu                     sync.Mutex
	registry               *analysisCleanupPhysicalRegistry
	identity               analysisCleanupIdentity
	descriptors            []*analysisCleanupDescriptor
	bodyDone, bodyUnknown  bool
	bodyErr                error
	done, unproven         chan struct{}
	doneOnce, unprovenOnce sync.Once
}
type analysisCleanupPhysicalRegistry struct {
	mu     sync.Mutex
	limit  int
	owners map[analysisCleanupIdentity]*analysisCleanupPhysicalOwner
}

func newAnalysisCleanupPhysicalRegistry(limit int) *analysisCleanupPhysicalRegistry {
	return &analysisCleanupPhysicalRegistry{limit: limit, owners: map[analysisCleanupIdentity]*analysisCleanupPhysicalOwner{}}
}

var analysisCleanupPhysicalOwners = newAnalysisCleanupPhysicalRegistry(analysisCleanupPhysicalLimit)

// Cleanup registers a bounded exact parent before any filesystem/cloud open.
// An expired waiter, nil logical Remove or missing object cannot prove that an
// entered operation, root, or bucket has physically joined. Retrying one exact
// source/key rejoins the same parent rather than creating a second deletion.
func (r *analysisCleanupPhysicalRegistry) acquire(identity analysisCleanupIdentity) (*analysisCleanupPhysicalOwner, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if owner := r.owners[identity]; owner != nil {
		return owner, false, nil
	}
	if len(r.owners) >= r.limit {
		return nil, false, errAnalysisCleanupPhysical
	}
	owner := &analysisCleanupPhysicalOwner{registry: r, identity: identity, done: make(chan struct{}), unproven: make(chan struct{})}
	r.owners[identity] = owner
	return owner, true, nil
}
func (o *analysisCleanupPhysicalOwner) track(c io.Closer) {
	if c == nil {
		return
	}
	o.mu.Lock()
	o.descriptors = append(o.descriptors, &analysisCleanupDescriptor{closer: c, done: make(chan struct{})})
	o.mu.Unlock()
}
func (o *analysisCleanupPhysicalOwner) closeDescriptor(d *analysisCleanupDescriptor) {
	d.once.Do(func() {
		go func() {
			var err error
			func() {
				defer func() {
					if recover() != nil {
						err = errAnalysisCleanupPhysical
					}
				}()
				err = d.closer.Close()
			}()
			o.mu.Lock()
			d.err, d.joined = err, err == nil
			close(d.done)
			o.mu.Unlock()
			if err != nil {
				o.unprovenOnce.Do(func() { close(o.unproven) })
			}
			o.progress()
		}()
	})
}
func (o *analysisCleanupPhysicalOwner) progress() {
	o.mu.Lock()
	joined := o.bodyDone && !o.bodyUnknown
	for _, d := range o.descriptors {
		joined = joined && d.joined
	}
	o.mu.Unlock()
	if !joined {
		return
	}
	o.doneOnce.Do(func() {
		// Keep the registry charge until body and every sole closer are actually
		// joined. No timer or cancellation path can remove this exact owner.
		o.registry.mu.Lock()
		if o.registry.owners[o.identity] == o {
			delete(o.registry.owners, o.identity)
		}
		close(o.done)
		o.registry.mu.Unlock()
	})
}
func (o *analysisCleanupPhysicalOwner) run(ctx context.Context, body func(context.Context, *analysisCleanupPhysicalOwner) error) {
	ctx, cancel := context.WithTimeout(ctx, analysisCleanupPhysicalTimeout)
	defer cancel()
	var err error
	unknown := false
	func() {
		defer func() {
			if recover() != nil {
				err = errAnalysisCleanupPhysical
				unknown = true
			}
		}()
		err = body(ctx, o)
	}()
	o.mu.Lock()
	o.bodyDone, o.bodyErr, o.bodyUnknown = true, err, unknown
	descriptors := append([]*analysisCleanupDescriptor(nil), o.descriptors...)
	o.mu.Unlock()
	for _, d := range descriptors {
		o.closeDescriptor(d)
	}
	if unknown {
		o.unprovenOnce.Do(func() { close(o.unproven) })
	}
	o.progress()
}
func (o *analysisCleanupPhysicalOwner) wait(ctx context.Context) error {
	timer := time.NewTimer(analysisCleanupPhysicalWait)
	defer timer.Stop()
	select {
	case <-o.done:
		o.mu.Lock()
		err := o.bodyErr
		o.mu.Unlock()
		return err
	case <-o.unproven:
		return errAnalysisCleanupPhysical
	case <-ctx.Done():
		return errors.Join(errAnalysisCleanupPhysical, ctx.Err())
	case <-timer.C:
		return errAnalysisCleanupPhysical
	}
}

// Private injection exists for adjacent source/physical failure oracles only;
// HTTP callers cannot provide a registry, opener or physical completion claim.
type analysisCleanupPhysicalRuntime struct {
	registry *analysisCleanupPhysicalRegistry
	ops      analysisCleanupPhysicalOps
}
type analysisCleanupPhysicalRuntimeKey struct{}

func analysisCleanupRuntime(ctx context.Context) analysisCleanupPhysicalRuntime {
	if runtime, ok := ctx.Value(analysisCleanupPhysicalRuntimeKey{}).(analysisCleanupPhysicalRuntime); ok {
		return runtime
	}
	return analysisCleanupPhysicalRuntime{analysisCleanupPhysicalOwners, defaultAnalysisCleanupPhysicalOps}
}
func runAnalysisCleanupPhysical(ctx context.Context, backend archiveBackendRuntime, kind, key string, body func(context.Context, *analysisCleanupPhysicalOwner, analysisCleanupPhysicalOps) error) error {
	if ctx == nil || ctx.Err() != nil {
		return errAnalysisCleanupPhysical
	}
	// All retained source identity scalars are bounded before map admission. This
	// snapshot prevents later environment changes from redirecting an old owner.
	for _, scalar := range []string{backend.Backend, backend.LocalRoot, backend.URIPrefix, backend.Bucket, backend.Container, backend.Prefix, backend.Region, backend.Endpoint, backend.Account, kind, key} {
		if len(scalar) > 8192 {
			return errors.New("analytical cleanup source exceeds bounds")
		}
	}
	if err := backend.validate(); err != nil {
		return err
	}
	runtime := analysisCleanupRuntime(ctx)
	if runtime.registry == nil || runtime.ops.openRoot == nil || runtime.ops.openBucket == nil {
		return errAnalysisCleanupPhysical
	}
	owner, start, err := runtime.registry.acquire(analysisCleanupIdentity{backend, kind, key})
	if err != nil {
		return err
	}
	if start {
		go owner.run(ctx, func(operationCtx context.Context, o *analysisCleanupPhysicalOwner) error {
			return body(operationCtx, o, runtime.ops)
		})
	}
	return owner.wait(ctx)
}
func deleteAnalysisObjectPhysical(ctx context.Context, backend archiveBackendRuntime, key string) error {
	if !analysisObjectPattern.MatchString(key) && !validMonitorArtifactKey(key) {
		return errors.New("invalid exact analytical cleanup key")
	}
	return runAnalysisCleanupPhysical(ctx, backend, "object", key, func(ctx context.Context, o *analysisCleanupPhysicalOwner, ops analysisCleanupPhysicalOps) error {
		if backend.Backend == archiveBackendFilesystem {
			root, err := ops.openRoot(filepath.Join(backend.LocalRoot, "_analysis"))
			if root != nil {
				o.track(root)
			}
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			err = root.Remove(key)
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		bucket, err := ops.openBucket(ctx, backend)
		if bucket != nil {
			o.track(bucket)
		}
		if err != nil {
			return err
		}
		err = bucket.Delete(ctx, filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", key)))
		if gcerrors.Code(err) == gcerrors.NotFound {
			return nil
		}
		return err
	})
}
func deleteAnalysisNamespacePhysical(ctx context.Context, backend archiveBackendRuntime, prefix string) error {
	canonical := filepath.ToSlash(prefix)
	if !analysisObjectPattern.MatchString(canonical+"/validation") && !validMonitorArtifactKey(canonical+"/0000000000000000000000000000000000000000000000000000000000000000.json") {
		return errors.New("invalid exact analytical cleanup namespace")
	}
	return runAnalysisCleanupPhysical(ctx, backend, "namespace", prefix, func(ctx context.Context, o *analysisCleanupPhysicalOwner, ops analysisCleanupPhysicalOps) error {
		if backend.Backend == archiveBackendFilesystem {
			root, err := ops.openRoot(filepath.Join(backend.LocalRoot, "_analysis"))
			if root != nil {
				o.track(root)
			}
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			return root.RemoveAll(prefix)
		}
		bucket, err := ops.openBucket(ctx, backend)
		if bucket != nil {
			o.track(bucket)
		}
		if err != nil {
			return err
		}
		objectPrefix := filepath.ToSlash(filepath.Join(backend.Prefix, "_analysis", prefix)) + "/"
		iterator := bucket.List(&blob.ListOptions{Prefix: objectPrefix})
		for range 40000 {
			item, err := iterator.Next(ctx)
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			if item.IsDir {
				continue
			}
			if err = bucket.Delete(ctx, item.Key); err != nil && gcerrors.Code(err) != gcerrors.NotFound {
				return err
			}
		}
		return errors.New("analytical cleanup will continue on next sweep")
	})
}
